package sync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

func selectChangedPageIDs(
	ctx context.Context,
	remote PullRemote,
	opts PullOptions,
	overlapWindow time.Duration,
	pageByID map[string]confluence.Page,
) ([]string, error) {
	if strings.TrimSpace(opts.TargetPageID) != "" {
		targetID := strings.TrimSpace(opts.TargetPageID)
		if _, ok := pageByID[targetID]; !ok {
			return nil, nil
		}
		return []string{targetID}, nil
	}

	if opts.ForceFull {
		allIDs := make([]string, 0, len(pageByID))
		for id := range pageByID {
			allIDs = append(allIDs, id)
		}
		sort.Strings(allIDs)
		return allIDs, nil
	}

	if strings.TrimSpace(opts.State.LastPullHighWatermark) == "" {
		allIDs := make([]string, 0, len(pageByID))
		for id := range pageByID {
			allIDs = append(allIDs, id)
		}
		sort.Strings(allIDs)
		return allIDs, nil
	}

	watermark, err := time.Parse(time.RFC3339, strings.TrimSpace(opts.State.LastPullHighWatermark))
	if err != nil {
		return nil, fmt.Errorf("parse last_pull_high_watermark: %w", err)
	}

	since := watermark.Add(-overlapWindow)
	changes, err := listAllChanges(ctx, remote, confluence.ChangeListOptions{
		SpaceKey: opts.SpaceKey,
		Since:    since,
		Limit:    pullChangeBatchSize,
	}, opts.Progress)
	if err != nil {
		return nil, fmt.Errorf("list incremental changes: %w", err)
	}

	ids := map[string]struct{}{}
	for _, change := range changes {
		if _, ok := pageByID[change.PageID]; ok {
			ids[change.PageID] = struct{}{}
		}
	}

	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
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
	folderByID := map[string]confluence.Folder{}
	diagnostics := []PullDiagnostic{}

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
			diagnostics = append(diagnostics, PullDiagnostic{
				Path:    folderID,
				Code:    "FOLDER_LOOKUP_UNAVAILABLE",
				Message: fmt.Sprintf("folder %s unavailable, falling back to page-only hierarchy: %v", folderID, err),
			})
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
