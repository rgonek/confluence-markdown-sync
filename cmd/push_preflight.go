package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

type pushPreflightContext struct {
	state          fs.SpaceState
	remotePageByID map[string]confluence.Page
	concerns       []string
}

type pushDeletePreview struct {
	path      string
	pageID    string
	pageTitle string
}

func runPushPreflight(
	ctx context.Context,
	out io.Writer,
	target config.Target,
	spaceKey, spaceDir string,
	gitClient *git.Client,
	spaceScopePath, changeScopePath string,
	onConflict string,
) error {
	baselineRef, err := gitPushBaselineRef(gitClient, spaceKey)
	if err != nil {
		return err
	}
	syncChanges, err := collectPushChangesForTarget(gitClient, baselineRef, target, spaceScopePath, changeScopePath)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "preflight for space %s\n", spaceKey)
	if len(syncChanges) == 0 {
		_, _ = fmt.Fprintf(out, "preflight for space %s: no local markdown changes detected since last sync (no-op)\n", spaceKey)
		return nil
	}

	if err := runPushValidation(ctx, out, target, spaceDir, "preflight validate failed"); err != nil {
		return err
	}

	preflightCtx, err := buildPushPreflightContext(ctx, spaceKey, spaceDir, syncChanges)
	if err != nil {
		return err
	}

	printPushPreflightConcerns(out, preflightCtx.concerns)
	printPushPreflightPageMutations(out, spaceDir, syncChanges, onConflict, preflightCtx)
	printPushPreflightAttachmentMutations(out, spaceDir, syncChanges, preflightCtx.state)

	addCount, modifyCount, deleteCount := summarizePushChanges(syncChanges)
	_, _ = fmt.Fprintf(out, "changes: %d (A:%d M:%d D:%d)\n", len(syncChanges), addCount, modifyCount, deleteCount)
	printPushPreflightDeleteSummary(out, spaceDir, syncChanges, preflightCtx)
	if len(syncChanges) > 10 || deleteCount > 0 {
		_, _ = fmt.Fprintln(out, "safety confirmation would be required")
	}
	return nil
}

func buildPushPreflightContext(ctx context.Context, spaceKey, spaceDir string, syncChanges []syncflow.PushFileChange) (pushPreflightContext, error) {
	envPath := findEnvPath(spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return pushPreflightContext{}, fmt.Errorf("failed to load config: %w", err)
	}

	remote, err := newPushRemote(cfg)
	if err != nil {
		return pushPreflightContext{}, fmt.Errorf("create confluence client: %w", err)
	}
	defer closeRemoteIfPossible(remote)

	concerns := make([]string, 0, 2)
	remotePageByID := map[string]confluence.Page{}
	remotePages := make([]confluence.Page, 0)
	space, spaceErr := remote.GetSpace(ctx, spaceKey)
	if spaceErr == nil {
		listResult, listErr := remote.ListPages(ctx, confluence.PageListOptions{
			SpaceID:  space.ID,
			SpaceKey: spaceKey,
			Status:   "current",
			Limit:    100,
		})
		if listErr == nil {
			remotePages = append(remotePages, listResult.Pages...)
			for _, page := range listResult.Pages {
				remotePageByID[strings.TrimSpace(page.ID)] = page
			}
		}
	}
	if pageID, pageStatus, ok := preflightContentStatusProbeTarget(spaceDir, syncChanges, remotePages); ok {
		if _, probeErr := remote.GetContentStatus(ctx, pageID, pageStatus); isPreflightCapabilityProbeError(probeErr) {
			concerns = append(concerns, "content-status metadata sync disabled for this push")
		}
	}

	state, _ := fs.LoadState(spaceDir)

	return pushPreflightContext{
		state:          state,
		remotePageByID: remotePageByID,
		concerns:       concerns,
	}, nil
}

func printPushPreflightConcerns(out io.Writer, concerns []string) {
	if len(concerns) == 0 {
		return
	}

	_, _ = fmt.Fprintln(out, "Remote capability concerns:")
	for _, concern := range concerns {
		_, _ = fmt.Fprintf(out, "  %s\n", concern)
	}
}

func preflightContentStatusProbeTarget(spaceDir string, syncChanges []syncflow.PushFileChange, remotePages []confluence.Page) (string, string, bool) {
	needsContentStatusSync := false
	for _, change := range syncChanges {
		if change.Type != syncflow.PushChangeAdd && change.Type != syncflow.PushChangeModify {
			continue
		}
		relPath := strings.TrimSpace(change.Path)
		if relPath == "" {
			continue
		}

		frontmatter, err := fs.ReadFrontmatter(filepath.Join(spaceDir, filepath.FromSlash(relPath)))
		if err != nil {
			continue
		}

		pageID := strings.TrimSpace(frontmatter.ID)
		if pageID == "" && strings.TrimSpace(frontmatter.Status) == "" {
			continue
		}
		needsContentStatusSync = true
		if pageID != "" {
			return pageID, normalizePreflightPageLifecycleState(frontmatter.State), true
		}
	}
	if !needsContentStatusSync {
		return "", "", false
	}
	for _, page := range remotePages {
		pageID := strings.TrimSpace(page.ID)
		if pageID == "" {
			continue
		}
		return pageID, normalizePreflightPageLifecycleState(page.Status), true
	}
	return "", "", false
}

func normalizePreflightPageLifecycleState(state string) string {
	normalized := strings.TrimSpace(strings.ToLower(state))
	if normalized == "" {
		return "current"
	}
	return normalized
}

func printPushPreflightPageMutations(
	out io.Writer,
	spaceDir string,
	syncChanges []syncflow.PushFileChange,
	onConflict string,
	preflightCtx pushPreflightContext,
) {
	_, _ = fmt.Fprintln(out, "Planned page mutations:")
	for _, change := range syncChanges {
		_, _ = fmt.Fprintf(
			out,
			"  %s\n",
			preflightPageMutationLine(spaceDir, change, onConflict, preflightCtx.state, preflightCtx.remotePageByID),
		)
	}
}

func preflightPageMutationLine(
	spaceDir string,
	change syncflow.PushFileChange,
	onConflict string,
	state fs.SpaceState,
	remotePageByID map[string]confluence.Page,
) string {
	switch change.Type {
	case syncflow.PushChangeAdd:
		return fmt.Sprintf("add %s", change.Path)
	case syncflow.PushChangeDelete:
		return resolvePushDeletePreview(spaceDir, state, remotePageByID, change.Path).preflightMutationLine()
	case syncflow.PushChangeModify:
		return preflightModifyMutationLine(spaceDir, change.Path, onConflict, remotePageByID)
	default:
		return change.Path
	}
}

func preflightModifyMutationLine(
	spaceDir string,
	relPath string,
	onConflict string,
	remotePageByID map[string]confluence.Page,
) string {
	frontmatter, err := fs.ReadFrontmatter(filepath.Join(spaceDir, filepath.FromSlash(relPath)))
	if err != nil {
		return fmt.Sprintf("update %s", relPath)
	}

	pageID := strings.TrimSpace(frontmatter.ID)
	if pageID == "" {
		return fmt.Sprintf("update %s", relPath)
	}

	remotePage, ok := remotePageByID[pageID]
	if !ok {
		return fmt.Sprintf("update %s (page %s)", relPath, pageID)
	}

	plannedVersion := frontmatter.Version + 1
	if onConflict == OnConflictForce {
		plannedVersion = remotePage.Version + 1
	}

	return fmt.Sprintf("update %s (page %s, %q, version %d)", relPath, pageID, remotePage.Title, plannedVersion)
}

func printPushPreflightAttachmentMutations(out io.Writer, spaceDir string, syncChanges []syncflow.PushFileChange, state fs.SpaceState) {
	uploads, deletes := preflightAttachmentMutations(spaceDir, syncChanges, state)
	if len(uploads) == 0 && len(deletes) == 0 {
		return
	}

	_, _ = fmt.Fprintln(out, "Planned attachment mutations:")
	for _, upload := range uploads {
		_, _ = fmt.Fprintf(out, "  upload %s\n", upload)
	}
	for _, deletePath := range deletes {
		_, _ = fmt.Fprintf(out, "  delete %s\n", deletePath)
	}
}

func printPushPreflightDeleteSummary(out io.Writer, spaceDir string, syncChanges []syncflow.PushFileChange, preflightCtx pushPreflightContext) {
	if !pushHasDeleteChange(syncChanges) {
		return
	}

	_, _ = fmt.Fprintln(out, "Destructive operations in this push:")
	for _, change := range syncChanges {
		if change.Type != syncflow.PushChangeDelete {
			continue
		}
		_, _ = fmt.Fprintf(
			out,
			"  %s\n",
			resolvePushDeletePreview(spaceDir, preflightCtx.state, preflightCtx.remotePageByID, change.Path).destructiveSummaryLine(),
		)
	}
}

func isPreflightCapabilityProbeError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *confluence.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	default:
		return false
	}
}

func preflightChangesNeedFolderHierarchy(changes []syncflow.PushFileChange) bool {
	for _, change := range changes {
		if change.Type == syncflow.PushChangeDelete {
			continue
		}
		dirPath := strings.TrimSpace(filepath.ToSlash(filepath.Dir(filepath.FromSlash(change.Path))))
		if dirPath != "" && dirPath != "." {
			return true
		}
	}
	return false
}

func preflightAttachmentMutations(spaceDir string, syncChanges []syncflow.PushFileChange, state fs.SpaceState) (uploads, deletes []string) {
	plannedUploadKeys := map[string]struct{}{}

	for _, change := range syncChanges {
		if change.Type == syncflow.PushChangeDelete {
			continue
		}

		absPath := filepath.Join(spaceDir, filepath.FromSlash(change.Path))
		doc, err := fs.ReadMarkdownDocument(absPath)
		if err != nil {
			continue
		}

		pageID := strings.TrimSpace(doc.Frontmatter.ID)
		if pageID == "" {
			continue
		}

		referencedPaths, err := syncflow.CollectReferencedAssetPaths(spaceDir, absPath, doc.Body)
		if err != nil {
			continue
		}

		for _, assetPath := range referencedPaths {
			plannedKey := filepath.ToSlash(filepath.Join("assets", pageID, filepath.Base(assetPath)))
			plannedUploadKeys[plannedKey] = struct{}{}
			if strings.TrimSpace(state.AttachmentIndex[plannedKey]) == "" {
				uploads = append(uploads, plannedKey)
			}
		}

		prefix := "assets/" + pageID + "/"
		for stateKey := range state.AttachmentIndex {
			if !strings.HasPrefix(stateKey, prefix) {
				continue
			}
			if _, covered := plannedUploadKeys[stateKey]; !covered {
				deletes = append(deletes, stateKey)
			}
		}
	}

	sort.Strings(uploads)
	sort.Strings(deletes)
	return uploads, deletes
}

func readPushChangePageID(spaceDir string, state fs.SpaceState, relPath string) string {
	pageID := ""
	frontmatter, err := fs.ReadFrontmatter(filepath.Join(spaceDir, filepath.FromSlash(relPath)))
	if err == nil {
		pageID = strings.TrimSpace(frontmatter.ID)
	}
	if pageID == "" {
		pageID = strings.TrimSpace(state.PagePathIndex[relPath])
	}
	return pageID
}

func resolvePushDeletePreview(
	spaceDir string,
	state fs.SpaceState,
	remotePageByID map[string]confluence.Page,
	relPath string,
) pushDeletePreview {
	pageID := readPushChangePageID(spaceDir, state, relPath)
	preview := pushDeletePreview{
		path:   relPath,
		pageID: pageID,
	}
	if pageID == "" {
		return preview
	}
	if remotePage, ok := remotePageByID[pageID]; ok {
		preview.pageTitle = strings.TrimSpace(remotePage.Title)
	}
	return preview
}

func (p pushDeletePreview) preflightMutationLine() string {
	if p.pageID == "" {
		return fmt.Sprintf("⚠ Destructive: delete %s", p.path)
	}
	if p.pageTitle != "" {
		return fmt.Sprintf("⚠ Destructive: archive remote page for %s (page %s, %q)", p.path, p.pageID, p.pageTitle)
	}
	return fmt.Sprintf("⚠ Destructive: archive remote page for %s (page %s)", p.path, p.pageID)
}

func (p pushDeletePreview) destructiveSummaryLine() string {
	if p.pageID == "" {
		return fmt.Sprintf("delete %s", p.path)
	}
	if p.pageTitle != "" {
		return fmt.Sprintf("archive remote page %s %q (%s)", p.pageID, p.pageTitle, p.path)
	}
	return fmt.Sprintf("archive remote page %s (%s)", p.pageID, p.path)
}
