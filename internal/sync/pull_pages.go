package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func selectChangedPages(
	ctx context.Context,
	remote PullRemote,
	opts PullOptions,
	overlapWindow time.Duration,
	pageByID map[string]confluence.Page,
) ([]string, map[string]confluence.Change, error) {
	changeByPageID := map[string]confluence.Change{}

	if strings.TrimSpace(opts.TargetPageID) != "" {
		targetID := strings.TrimSpace(opts.TargetPageID)
		if _, ok := pageByID[targetID]; !ok {
			return nil, changeByPageID, nil
		}
		changeByPageID[targetID] = changeFromPage(pageByID[targetID], opts.SpaceKey)
		return []string{targetID}, changeByPageID, nil
	}

	ids := map[string]struct{}{}

	if opts.ForceFull {
		for id, page := range pageByID {
			ids[id] = struct{}{}
			changeByPageID[id] = changeFromPage(page, opts.SpaceKey)
		}
		return sortedStringKeys(ids), changeByPageID, nil
	}

	if strings.TrimSpace(opts.State.LastPullHighWatermark) == "" {
		for id, page := range pageByID {
			ids[id] = struct{}{}
			changeByPageID[id] = changeFromPage(page, opts.SpaceKey)
		}
		return sortedStringKeys(ids), changeByPageID, nil
	}

	watermark, err := time.Parse(time.RFC3339, strings.TrimSpace(opts.State.LastPullHighWatermark))
	if err != nil {
		return nil, nil, fmt.Errorf("parse last_pull_high_watermark: %w", err)
	}

	since := watermark.Add(-overlapWindow)
	changes, err := listAllChanges(ctx, remote, confluence.ChangeListOptions{
		SpaceKey: opts.SpaceKey,
		Since:    since,
		Limit:    pullChangeBatchSize,
	}, opts.Progress)
	if err != nil {
		return nil, nil, fmt.Errorf("list incremental changes: %w", err)
	}

	for _, change := range changes {
		pageID := strings.TrimSpace(change.PageID)
		if pageID == "" {
			continue
		}
		change.PageID = pageID
		changeByPageID[pageID] = mergeChangedPage(changeByPageID[pageID], change)
		if _, ok := pageByID[pageID]; ok {
			ids[pageID] = struct{}{}
		}
	}

	trackedVersions := loadTrackedPageVersions(opts.SpaceDir, opts.State.PagePathIndex)
	for pageID, page := range pageByID {
		localVersion, tracked := trackedVersions[pageID]
		if !tracked || page.Version > localVersion {
			ids[pageID] = struct{}{}
			changeByPageID[pageID] = mergeChangedPage(changeByPageID[pageID], changeFromPage(page, opts.SpaceKey))
		}
	}

	return sortedStringKeys(ids), changeByPageID, nil
}

func changeFromPage(page confluence.Page, spaceKey string) confluence.Change {
	return confluence.Change{
		PageID:       strings.TrimSpace(page.ID),
		SpaceKey:     strings.TrimSpace(spaceKey),
		Title:        strings.TrimSpace(page.Title),
		Version:      page.Version,
		LastModified: page.LastModified,
	}
}

func mergeChangedPage(existing, incoming confluence.Change) confluence.Change {
	if strings.TrimSpace(existing.PageID) == "" {
		existing.PageID = strings.TrimSpace(incoming.PageID)
	}
	if strings.TrimSpace(existing.SpaceKey) == "" {
		existing.SpaceKey = strings.TrimSpace(incoming.SpaceKey)
	}
	if strings.TrimSpace(existing.Title) == "" {
		existing.Title = strings.TrimSpace(incoming.Title)
	}
	if incoming.Version > existing.Version {
		existing.Version = incoming.Version
	}
	if incoming.LastModified.After(existing.LastModified) {
		existing.LastModified = incoming.LastModified
	}
	return existing
}

func loadTrackedPageVersions(spaceDir string, pagePathIndex map[string]string) map[string]int {
	versions := map[string]int{}
	for relPath, pageID := range pagePathIndex {
		pageID = strings.TrimSpace(pageID)
		if pageID == "" {
			continue
		}
		absPath := filepath.Join(spaceDir, filepath.FromSlash(normalizeRelPath(relPath)))
		raw, err := os.ReadFile(absPath) //nolint:gosec // path is derived from tracked workspace state
		if err != nil {
			continue
		}
		doc, err := fs.ParseMarkdownDocument(raw)
		if err != nil {
			continue
		}
		versions[pageID] = doc.Frontmatter.Version
	}
	return versions
}

func fetchChangedPageWithRetry(
	ctx context.Context,
	remote PullRemote,
	pageID string,
	listedPage confluence.Page,
	changedPage confluence.Change,
) (confluence.Page, error) {
	expectedVersion := listedPage.Version
	if changedPage.Version > expectedVersion {
		expectedVersion = changedPage.Version
	}
	expectedModified := listedPage.LastModified
	if changedPage.LastModified.After(expectedModified) {
		expectedModified = changedPage.LastModified
	}

	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		page, err := remote.GetPage(ctx, pageID)
		if err == nil {
			if pageMatchesExpectedState(page, expectedVersion, expectedModified) {
				return page, nil
			}
			lastErr = fmt.Errorf(
				"page %s did not reach expected remote state yet (got version %d, want at least %d)",
				pageID,
				page.Version,
				expectedVersion,
			)
		} else if errors.Is(err, confluence.ErrNotFound) || errors.Is(err, confluence.ErrArchived) {
			lastErr = fmt.Errorf("page %s was listed as changed but is not readable yet: %w", pageID, err)
		} else {
			return confluence.Page{}, fmt.Errorf("fetch page %s: %w", pageID, err)
		}

		if attempt == 4 {
			break
		}
		if err := contextSleep(ctx, time.Duration(attempt+1)*200*time.Millisecond); err != nil {
			return confluence.Page{}, err
		}
	}

	return confluence.Page{}, fmt.Errorf("fetch page %s: %w", pageID, lastErr)
}

func pageMatchesExpectedState(page confluence.Page, expectedVersion int, expectedModified time.Time) bool {
	if expectedVersion > 0 && page.Version < expectedVersion {
		return false
	}
	if !expectedModified.IsZero() && !page.LastModified.IsZero() && page.LastModified.Before(expectedModified) {
		return false
	}
	return true
}

func shouldIgnoreFolderHierarchyError(err error) bool {
	if errors.Is(err, confluence.ErrNotFound) {
		return true
	}
	var apiErr *confluence.APIError
	return errors.As(err, &apiErr)
}

func listAllPages(ctx context.Context, remote PullRemote, opts confluence.PageListOptions, progress Progress) ([]confluence.Page, error) {
	result := []confluence.Page{}
	cursor := opts.Cursor
	iterations := 0
	for {
		if iterations >= maxPaginationIterations {
			return nil, fmt.Errorf("pagination loop exceeded %d iterations for space %s", maxPaginationIterations, opts.SpaceID)
		}
		iterations++
		opts.Cursor = cursor
		pageResult, err := remote.ListPages(ctx, opts)
		if err != nil {
			return nil, err
		}
		result = append(result, pageResult.Pages...)
		if progress != nil {
			progress.Add(len(pageResult.Pages))
		}
		if strings.TrimSpace(pageResult.NextCursor) == "" || pageResult.NextCursor == cursor {
			break
		}
		cursor = pageResult.NextCursor
	}
	return result, nil
}

func resolveFolderHierarchyFromPages(ctx context.Context, remote PullRemote, pages []confluence.Page) (map[string]confluence.Folder, []PullDiagnostic, error) {
	return resolveFolderHierarchyFromPagesWithMode(ctx, remote, pages, tenantFolderModeNative)
}

func resolveFolderHierarchyFromPagesWithMode(ctx context.Context, remote PullRemote, pages []confluence.Page, mode tenantFolderMode) (map[string]confluence.Folder, []PullDiagnostic, error) {
	folderByID := map[string]confluence.Folder{}
	diagnostics := []PullDiagnostic{}
	if mode == tenantFolderModePageFallback {
		return folderByID, diagnostics, nil
	}
	fallbackTracker := NewFolderLookupFallbackTracker()

	queue := []string{}
	enqueued := map[string]struct{}{}
	for _, page := range pages {
		if !strings.EqualFold(strings.TrimSpace(page.ParentType), "folder") {
			continue
		}
		parentID := strings.TrimSpace(page.ParentPageID)
		if parentID == "" {
			continue
		}
		if _, exists := enqueued[parentID]; exists {
			continue
		}
		queue = append(queue, parentID)
		enqueued[parentID] = struct{}{}
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
			if diag, ok := fallbackTracker.Report("pull-folder-hierarchy", folderID, err); ok {
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

// ResolveFolderPathIndex rebuilds folder_path_index from remote hierarchy.
func ResolveFolderPathIndex(ctx context.Context, remote PullRemote, pages []confluence.Page) (map[string]string, []PullDiagnostic, error) {
	folderByID, diagnostics, err := resolveFolderHierarchyFromPages(ctx, remote, pages)
	if err != nil {
		return nil, nil, err
	}

	pageByID := make(map[string]confluence.Page, len(pages))
	for _, page := range pages {
		pageByID[strings.TrimSpace(page.ID)] = page
	}

	folderPathIndex := buildFolderPathIndex(folderByID, pageByID)
	return folderPathIndex, diagnostics, nil
}

func listAllChanges(ctx context.Context, remote PullRemote, opts confluence.ChangeListOptions, progress Progress) ([]confluence.Change, error) {
	result := []confluence.Change{}
	start := opts.Start
	iterations := 0
	for {
		if iterations >= maxPaginationIterations {
			return nil, fmt.Errorf("pagination loop exceeded %d iterations for changes since %v", maxPaginationIterations, opts.Since)
		}
		iterations++
		opts.Start = start
		changeResult, err := remote.ListChanges(ctx, opts)
		if err != nil {
			return nil, err
		}
		result = append(result, changeResult.Changes...)
		if progress != nil {
			progress.Add(len(changeResult.Changes))
		}
		if !changeResult.HasMore {
			break
		}

		next := changeResult.NextStart
		if next <= start {
			next = start + len(changeResult.Changes)
		}
		if next <= start && opts.Limit > 0 {
			next = start + opts.Limit
		}
		if next <= start {
			break
		}
		start = next
	}
	return result, nil
}
