package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func snapshotPageContent(page confluence.Page) pushContentSnapshot {
	clonedBody := append(json.RawMessage(nil), page.BodyADF...)
	return pushContentSnapshot{
		SpaceID:      strings.TrimSpace(page.SpaceID),
		Title:        strings.TrimSpace(page.Title),
		ParentPageID: strings.TrimSpace(page.ParentPageID),
		Status:       normalizePageLifecycleState(page.Status),
		BodyADF:      clonedBody,
	}
}

func restorePageContentSnapshot(ctx context.Context, remote PushRemote, pageID string, snapshot pushContentSnapshot) error {
	pageID = strings.TrimSpace(pageID)
	if pageID == "" {
		return errors.New("page ID is required")
	}

	headPage, err := remote.GetPage(ctx, pageID)
	if err != nil {
		return fmt.Errorf("fetch latest page %s: %w", pageID, err)
	}

	spaceID := strings.TrimSpace(snapshot.SpaceID)
	if spaceID == "" {
		spaceID = strings.TrimSpace(headPage.SpaceID)
	}
	if spaceID == "" {
		return fmt.Errorf("resolve space id for page %s", pageID)
	}

	parentID := strings.TrimSpace(snapshot.ParentPageID)
	title := strings.TrimSpace(snapshot.Title)
	if title == "" {
		title = strings.TrimSpace(headPage.Title)
	}
	if title == "" {
		return fmt.Errorf("resolve title for page %s", pageID)
	}

	body := append(json.RawMessage(nil), snapshot.BodyADF...)
	if len(body) == 0 {
		body = []byte(`{"version":1,"type":"doc","content":[]}`)
	}

	nextVersion := headPage.Version + 1
	if nextVersion <= 0 {
		nextVersion = 1
	}

	_, err = remote.UpdatePage(ctx, pageID, confluence.PageUpsertInput{
		SpaceID:      spaceID,
		ParentPageID: parentID,
		Title:        title,
		Status:       normalizePageLifecycleState(snapshot.Status),
		Version:      nextVersion,
		BodyADF:      body,
	})
	if err != nil {
		return fmt.Errorf("update page %s to restore snapshot: %w", pageID, err)
	}

	return nil
}

func capturePageMetadataSnapshot(ctx context.Context, remote PushRemote, pageID string) (pushMetadataSnapshot, error) {
	status, err := remote.GetContentStatus(ctx, pageID)
	if err != nil {
		return pushMetadataSnapshot{}, fmt.Errorf("get content status: %w", err)
	}

	labels, err := remote.GetLabels(ctx, pageID)
	if err != nil {
		return pushMetadataSnapshot{}, fmt.Errorf("get labels: %w", err)
	}

	return pushMetadataSnapshot{
		ContentStatus: strings.TrimSpace(status),
		Labels:        fs.NormalizeLabels(labels),
	}, nil
}

func restorePageMetadataSnapshot(ctx context.Context, remote PushRemote, pageID string, snapshot pushMetadataSnapshot) error {
	targetStatus := strings.TrimSpace(snapshot.ContentStatus)
	currentStatus, err := remote.GetContentStatus(ctx, pageID)
	if err != nil {
		return fmt.Errorf("get content status: %w", err)
	}
	currentStatus = strings.TrimSpace(currentStatus)

	if currentStatus != targetStatus {
		if targetStatus == "" {
			if err := remote.DeleteContentStatus(ctx, pageID); err != nil {
				return fmt.Errorf("delete content status: %w", err)
			}
		} else {
			if err := remote.SetContentStatus(ctx, pageID, targetStatus); err != nil {
				return fmt.Errorf("set content status: %w", err)
			}
		}
	}

	remoteLabels, err := remote.GetLabels(ctx, pageID)
	if err != nil {
		return fmt.Errorf("get labels: %w", err)
	}

	targetLabelSet := map[string]struct{}{}
	for _, label := range fs.NormalizeLabels(snapshot.Labels) {
		targetLabelSet[label] = struct{}{}
	}

	currentLabelSet := map[string]struct{}{}
	for _, label := range fs.NormalizeLabels(remoteLabels) {
		currentLabelSet[label] = struct{}{}
	}

	for label := range currentLabelSet {
		if _, keep := targetLabelSet[label]; keep {
			continue
		}
		if err := remote.RemoveLabel(ctx, pageID, label); err != nil {
			return fmt.Errorf("remove label %q: %w", label, err)
		}
	}

	toAdd := make([]string, 0)
	for label := range targetLabelSet {
		if _, exists := currentLabelSet[label]; exists {
			continue
		}
		toAdd = append(toAdd, label)
	}
	sort.Strings(toAdd)

	if len(toAdd) > 0 {
		if err := remote.AddLabels(ctx, pageID, toAdd); err != nil {
			return fmt.Errorf("add labels: %w", err)
		}
	}

	return nil
}

func resolveLocalTitle(doc fs.MarkdownDocument, relPath string) string {
	title := strings.TrimSpace(doc.Frontmatter.Title)
	if title != "" {
		return title
	}

	for _, line := range strings.Split(doc.Body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			if title != "" {
				return title
			}
		}
	}

	base := filepath.Base(relPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func buildLocalPageTitleIndex(spaceDir string) (map[string]string, error) {
	out := map[string]string{}
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

		relPath, err := filepath.Rel(spaceDir, path)
		if err != nil {
			return nil
		}
		relPath = normalizeRelPath(relPath)
		if relPath == "" {
			return nil
		}

		doc, err := fs.ReadMarkdownDocument(path)
		if err != nil {
			return nil
		}

		title := strings.TrimSpace(resolveLocalTitle(doc, relPath))
		if title == "" {
			return nil
		}
		out[relPath] = title
		return nil
	})
	return out, err
}

func findTrackedTitleConflict(relPath, title string, pagePathIndex map[string]string, pageTitleByPath map[string]string) (string, string) {
	titleKey := strings.ToLower(strings.TrimSpace(title))
	if titleKey == "" {
		return "", ""
	}

	normalizedPath := normalizeRelPath(relPath)
	currentDir := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(normalizedPath))))

	for trackedPath, trackedPageID := range pagePathIndex {
		trackedPath = normalizeRelPath(trackedPath)
		trackedPageID = strings.TrimSpace(trackedPageID)
		if trackedPath == "" || trackedPageID == "" {
			continue
		}
		if trackedPath == normalizedPath {
			continue
		}

		trackedTitle := strings.ToLower(strings.TrimSpace(pageTitleByPath[trackedPath]))
		if trackedTitle == "" || trackedTitle != titleKey {
			continue
		}

		trackedDir := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(trackedPath))))
		if trackedDir != currentDir {
			continue
		}

		return trackedPath, trackedPageID
	}

	return "", ""
}

func normalizePageLifecycleState(state string) string {
	normalized := strings.TrimSpace(strings.ToLower(state))
	if normalized == "" {
		return "current"
	}
	return normalized
}

func listAllPushPages(ctx context.Context, remote PushRemote, opts confluence.PageListOptions) ([]confluence.Page, error) {
	// Try with title filter first if provided
	if opts.Title != "" {
		res, err := remote.ListPages(ctx, opts)
		if err == nil {
			return res.Pages, nil
		}
		// Fallback to full list if title filter failed
		opts.Title = ""
	}

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

func listAllPushFolders(ctx context.Context, remote PushRemote, opts confluence.FolderListOptions) ([]confluence.Folder, error) {
	// Try with title filter first if provided
	if opts.Title != "" {
		res, err := remote.ListFolders(ctx, opts)
		if err == nil {
			return res.Folders, nil
		}
		// Fallback to full list if title filter failed
		opts.Title = ""
	}

	result := []confluence.Folder{}
	cursor := opts.Cursor
	for {
		opts.Cursor = cursor
		folderResult, err := remote.ListFolders(ctx, opts)
		if err != nil {
			return nil, err
		}
		result = append(result, folderResult.Folders...)
		if strings.TrimSpace(folderResult.NextCursor) == "" || folderResult.NextCursor == cursor {
			break
		}
		cursor = folderResult.NextCursor
	}
	return result, nil
}
