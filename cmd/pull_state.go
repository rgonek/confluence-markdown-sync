package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

func loadPullStateWithHealing(
	ctx context.Context,
	out io.Writer,
	remote syncflow.PullRemote,
	space confluence.Space,
	spaceDir string,
) (fs.SpaceState, error) {
	state, err := fs.LoadState(spaceDir)
	if err == nil {
		return state, nil
	}
	if !fs.IsStateConflictError(err) {
		return fs.SpaceState{}, fmt.Errorf("load state: %w", err)
	}

	_, _ = fmt.Fprintf(out, "WARNING: Git conflict detected in %q. Rebuilding state from Confluence and local IDs...\n", fs.StateFileName)

	healedState, diagnostics, healErr := rebuildStateFromConfluenceAndLocal(ctx, remote, space, spaceDir)
	if healErr != nil {
		return fs.SpaceState{}, fmt.Errorf("heal corrupted state: %w", healErr)
	}
	if err := fs.SaveState(spaceDir, healedState); err != nil {
		return fs.SpaceState{}, fmt.Errorf("save healed state: %w", err)
	}

	for _, diag := range diagnostics {
		if err := writeSyncDiagnostic(out, diag); err != nil {
			return fs.SpaceState{}, fmt.Errorf("write diagnostic output: %w", err)
		}
	}

	_, _ = fmt.Fprintln(out, "State file healed successfully.")
	return healedState, nil
}

func rebuildStateFromConfluenceAndLocal(
	ctx context.Context,
	remote syncflow.PullRemote,
	space confluence.Space,
	spaceDir string,
) (fs.SpaceState, []syncflow.PullDiagnostic, error) {
	pages, err := listAllPullPagesForEstimate(ctx, remote, confluence.PageListOptions{
		SpaceID:  space.ID,
		SpaceKey: space.Key,
		Status:   "current",
		Limit:    100,
	}, nil)
	if err != nil {
		return fs.SpaceState{}, nil, fmt.Errorf("list pages for state healing: %w", err)
	}

	remotePageByID := make(map[string]confluence.Page, len(pages))
	for _, page := range pages {
		remotePageByID[strings.TrimSpace(page.ID)] = page
	}

	localPathByPageID, err := scanLocalMarkdownIDs(spaceDir)
	if err != nil {
		return fs.SpaceState{}, nil, err
	}

	for pageID := range localPathByPageID {
		if _, exists := remotePageByID[pageID]; exists {
			continue
		}
		page, getErr := remote.GetPage(ctx, pageID)
		if getErr != nil {
			if errors.Is(getErr, confluence.ErrNotFound) || errors.Is(getErr, confluence.ErrArchived) {
				continue
			}
			return fs.SpaceState{}, nil, fmt.Errorf("fetch page %s during state healing: %w", pageID, getErr)
		}
		if page.SpaceID != space.ID || !syncflow.IsSyncableRemotePageStatus(page.Status) {
			continue
		}
		remotePageByID[pageID] = page
		pages = append(pages, page)
	}

	pagePathIndex := map[string]string{}
	for pageID, relPath := range localPathByPageID {
		if _, exists := remotePageByID[pageID]; !exists {
			continue
		}
		pagePathIndex[relPath] = pageID
	}

	folderPathIndex, diagnostics, err := syncflow.ResolveFolderPathIndex(ctx, remote, pages)
	if err != nil {
		return fs.SpaceState{}, nil, fmt.Errorf("rebuild folder path index: %w", err)
	}

	state := fs.NewSpaceState()
	state.SpaceKey = strings.TrimSpace(space.Key)
	if state.SpaceKey == "" {
		state.SpaceKey = strings.TrimSpace(space.ID)
	}
	state.PagePathIndex = pagePathIndex
	state.FolderPathIndex = folderPathIndex
	state.LastPullHighWatermark = ""
	return state, diagnostics, nil
}

func scanLocalMarkdownIDs(spaceDir string) (map[string]string, error) {
	localPathByPageID := map[string]string{}
	err := filepath.WalkDir(spaceDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == "assets" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		fm, err := fs.ReadFrontmatter(path)
		if err != nil {
			return nil
		}
		pageID := strings.TrimSpace(fm.ID)
		if pageID == "" {
			return nil
		}

		relPath, err := filepath.Rel(spaceDir, path)
		if err != nil {
			return nil
		}
		relPath = normalizeRepoRelPath(relPath)
		if relPath == "" {
			return nil
		}

		if existing, exists := localPathByPageID[pageID]; exists {
			if relPath < existing {
				localPathByPageID[pageID] = relPath
			}
			return nil
		}
		localPathByPageID[pageID] = relPath
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan local markdown for page IDs: %w", err)
	}
	return localPathByPageID, nil
}

func listDirtyMarkdownPathsForScope(repoRoot, scopePath string) (map[string]struct{}, error) {
	out, err := runGit(repoRoot, "status", "--porcelain", "-z", "--", scopePath)
	if err != nil {
		return nil, err
	}

	normalizedScope := normalizeRepoRelPath(scopePath)
	result := map[string]struct{}{}
	tokens := strings.Split(out, "\x00")
	for i := 0; i < len(tokens); i++ {
		token := strings.TrimRight(tokens[i], "\r\n")
		if token == "" || len(token) < 4 {
			continue
		}

		status := token[:2]
		pathField := strings.TrimSpace(token[3:])
		if pathField == "" {
			continue
		}

		candidatePaths := []string{pathField}
		if strings.Contains(status, "R") || strings.Contains(status, "C") {
			if i+1 < len(tokens) {
				nextPath := strings.TrimSpace(tokens[i+1])
				if nextPath != "" {
					candidatePaths = append(candidatePaths, nextPath)
					i++
				}
			}
		}

		for _, candidate := range candidatePaths {
			repoRelPath := normalizeRepoRelPath(candidate)
			if repoRelPath == "" {
				continue
			}

			spaceRelPath := repoRelPath
			if normalizedScope != "" {
				if !strings.HasPrefix(repoRelPath, normalizedScope+"/") {
					continue
				}
				spaceRelPath = strings.TrimPrefix(repoRelPath, normalizedScope+"/")
			}
			spaceRelPath = normalizeRepoRelPath(spaceRelPath)
			if !strings.HasSuffix(strings.ToLower(spaceRelPath), ".md") {
				continue
			}
			result[spaceRelPath] = struct{}{}
		}
	}

	return result, nil
}

func warnSkippedDirtyDeletions(out io.Writer, deletedMarkdown []string, dirtyBeforePull map[string]struct{}) {
	if len(deletedMarkdown) == 0 || len(dirtyBeforePull) == 0 {
		return
	}

	for _, relPath := range deletedMarkdown {
		relPath = normalizeRepoRelPath(relPath)
		if relPath == "" {
			continue
		}
		if _, dirty := dirtyBeforePull[relPath]; !dirty {
			continue
		}
		_, _ = fmt.Fprintf(out, "WARNING: Skipped local deletion of '%s' because it contains uncommitted edits. Please resolve manually or run with --discard-local.\n", relPath)
	}
}
