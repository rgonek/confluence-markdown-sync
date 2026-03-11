package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

type diffFolderLookupRemote interface {
	GetFolder(ctx context.Context, folderID string) (confluence.Folder, error)
}

type diffPageLookupRemote interface {
	GetPage(ctx context.Context, pageID string) (confluence.Page, error)
}

func listAllDiffPages(ctx context.Context, remote syncflow.PullRemote, opts confluence.PageListOptions) ([]confluence.Page, error) {
	result := []confluence.Page{}
	cursor := opts.Cursor
	for {
		opts.Cursor = cursor
		pageResult, err := remote.ListPages(ctx, opts)
		if err != nil {
			return nil, err
		}
		result = append(result, pageResult.Pages...)
		if strings.TrimSpace(pageResult.NextCursor) == "" || pageResult.NextCursor == cursor {
			break
		}
		cursor = pageResult.NextCursor
	}
	return result, nil
}

func recoverMissingPagesForDiff(ctx context.Context, remote diffPageLookupRemote, spaceID string, localPageIDs map[string]string, remotePages []confluence.Page) ([]confluence.Page, error) {
	remoteByID := make(map[string]struct{}, len(remotePages))
	for _, p := range remotePages {
		remoteByID[p.ID] = struct{}{}
	}

	result := remotePages
	processedIDs := make(map[string]struct{})
	for _, id := range localPageIDs {
		if id == "" {
			continue
		}
		if _, exists := remoteByID[id]; exists {
			continue
		}
		if _, processed := processedIDs[id]; processed {
			continue
		}
		processedIDs[id] = struct{}{}

		page, err := remote.GetPage(ctx, id)
		if err != nil {
			if errors.Is(err, confluence.ErrNotFound) || errors.Is(err, confluence.ErrArchived) {
				continue
			}
			var apiErr *confluence.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
				continue
			}
			return nil, err
		}

		if page.SpaceID == spaceID && syncflow.IsSyncableRemotePageStatus(page.Status) {
			result = append(result, page)
			remoteByID[id] = struct{}{}
		}
	}
	return result, nil
}

func resolveDiffFolderHierarchyFromPages(ctx context.Context, remote diffFolderLookupRemote, pages []confluence.Page) (map[string]confluence.Folder, []syncflow.PullDiagnostic, error) {
	folderByID := map[string]confluence.Folder{}
	diagnostics := []syncflow.PullDiagnostic{}
	fallbackTracker := syncflow.NewFolderLookupFallbackTracker()

	queue := []string{}
	enqueued := map[string]struct{}{}
	for _, page := range pages {
		if !strings.EqualFold(strings.TrimSpace(page.ParentType), "folder") {
			continue
		}
		folderID := strings.TrimSpace(page.ParentPageID)
		if folderID == "" {
			continue
		}
		if _, exists := enqueued[folderID]; exists {
			continue
		}
		queue = append(queue, folderID)
		enqueued[folderID] = struct{}{}
	}

	visited := map[string]struct{}{}
	for len(queue) > 0 {
		folderID := queue[0]
		queue = queue[1:]

		if _, seen := visited[folderID]; seen {
			continue
		}
		visited[folderID] = struct{}{}

		folder, err := remote.GetFolder(ctx, folderID)
		if err != nil {
			if !shouldIgnoreFolderHierarchyError(err) {
				return nil, nil, fmt.Errorf("get folder %s: %w", folderID, err)
			}
			if diag, ok := fallbackTracker.Report("diff-folder-hierarchy", folderID, err); ok {
				diagnostics = append(diagnostics, diag)
			}
			continue
		}

		folderByID[folder.ID] = folder

		if !strings.EqualFold(strings.TrimSpace(folder.ParentType), "folder") {
			continue
		}
		parentID := strings.TrimSpace(folder.ParentID)
		if parentID == "" {
			continue
		}
		if _, seen := visited[parentID]; seen {
			continue
		}
		if _, exists := enqueued[parentID]; exists {
			continue
		}
		queue = append(queue, parentID)
		enqueued[parentID] = struct{}{}
	}

	return folderByID, diagnostics, nil
}

func shouldIgnoreFolderHierarchyError(err error) bool {
	if errors.Is(err, confluence.ErrNotFound) {
		return true
	}
	var apiErr *confluence.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 400, 409:
			return false
		default:
			return true
		}
	}
	return false
}

func diffDisplayRelPath(spaceDir, path string) string {
	relPath, err := filepath.Rel(spaceDir, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relPath)
}
