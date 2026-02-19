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
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

const (
	// DefaultPullOverlapWindow is the default overlap window for incremental pull fetches.
	DefaultPullOverlapWindow = 2 * time.Minute
	pullPageBatchSize        = 100
	pullChangeBatchSize      = 100
)

// PullRemote defines the remote operations required by pull orchestration.
type PullRemote interface {
	GetSpace(ctx context.Context, spaceKey string) (confluence.Space, error)
	ListPages(ctx context.Context, opts confluence.PageListOptions) (confluence.PageListResult, error)
	ListChanges(ctx context.Context, opts confluence.ChangeListOptions) (confluence.ChangeListResult, error)
	GetPage(ctx context.Context, pageID string) (confluence.Page, error)
	DownloadAttachment(ctx context.Context, attachmentID string) ([]byte, error)
}

// PullOptions controls pull orchestration behavior.
type PullOptions struct {
	SpaceKey      string
	SpaceDir      string
	State         fs.SpaceState
	PullStartedAt time.Time
	OverlapWindow time.Duration
	TargetPageID  string
}

// PullDiagnostic captures non-fatal conversion diagnostics.
type PullDiagnostic struct {
	Path    string
	Code    string
	Message string
}

// PullResult captures pull execution outputs.
type PullResult struct {
	State            fs.SpaceState
	MaxVersion       int
	Diagnostics      []PullDiagnostic
	UpdatedMarkdown  []string
	DeletedMarkdown  []string
	DownloadedAssets []string
	DeletedAssets    []string
}

type attachmentRef struct {
	PageID       string
	AttachmentID string
	Filename     string
}

// Pull executes end-to-end pull orchestration in local filesystem scope.
func Pull(ctx context.Context, remote PullRemote, opts PullOptions) (PullResult, error) {
	if strings.TrimSpace(opts.SpaceKey) == "" {
		return PullResult{}, errors.New("space key is required")
	}
	if strings.TrimSpace(opts.SpaceDir) == "" {
		return PullResult{}, errors.New("space directory is required")
	}

	spaceDir, err := filepath.Abs(opts.SpaceDir)
	if err != nil {
		return PullResult{}, fmt.Errorf("resolve space directory: %w", err)
	}

	state := opts.State
	if state.PagePathIndex == nil {
		state.PagePathIndex = map[string]string{}
	}
	if state.AttachmentIndex == nil {
		state.AttachmentIndex = map[string]string{}
	}

	pullStartedAt := opts.PullStartedAt
	if pullStartedAt.IsZero() {
		pullStartedAt = time.Now().UTC()
	}
	overlapWindow := opts.OverlapWindow
	if overlapWindow <= 0 {
		overlapWindow = DefaultPullOverlapWindow
	}

	space, err := remote.GetSpace(ctx, opts.SpaceKey)
	if err != nil {
		return PullResult{}, fmt.Errorf("resolve space %q: %w", opts.SpaceKey, err)
	}

	pages, err := listAllPages(ctx, remote, confluence.PageListOptions{
		SpaceID:  space.ID,
		SpaceKey: opts.SpaceKey,
		Status:   "current",
		Limit:    pullPageBatchSize,
	})
	if err != nil {
		return PullResult{}, fmt.Errorf("list pages: %w", err)
	}

	pageByID := make(map[string]confluence.Page, len(pages))
	pageIDs := make([]string, 0, len(pages))
	maxRemoteModified := time.Time{}
	maxVersion := 0
	for _, page := range pages {
		pageByID[page.ID] = page
		pageIDs = append(pageIDs, page.ID)
		if page.Version > maxVersion {
			maxVersion = page.Version
		}
		if page.LastModified.After(maxRemoteModified) {
			maxRemoteModified = page.LastModified
		}
	}
	sort.Strings(pageIDs)

	pagePathByIDAbs, pagePathByIDRel := PlanPagePaths(spaceDir, state.PagePathIndex, pages)

	changedPageIDs, err := selectChangedPageIDs(ctx, remote, opts, overlapWindow, pageByID)
	if err != nil {
		return PullResult{}, err
	}

	changedPages := make(map[string]confluence.Page, len(changedPageIDs))
	for _, pageID := range changedPageIDs {
		page, err := remote.GetPage(ctx, pageID)
		if err != nil {
			if errors.Is(err, confluence.ErrNotFound) {
				continue
			}
			return PullResult{}, fmt.Errorf("fetch page %s: %w", pageID, err)
		}
		changedPages[pageID] = page
		if page.Version > maxVersion {
			maxVersion = page.Version
		}
		if page.LastModified.After(maxRemoteModified) {
			maxRemoteModified = page.LastModified
		}
	}

	attachmentIndex := cloneStringMap(state.AttachmentIndex)
	attachmentPathByID := map[string]string{}
	staleAttachmentPaths := map[string]struct{}{}

	deletedPageIDs := deletedPageIDs(state.PagePathIndex, pageByID)
	for _, pageID := range deletedPageIDs {
		for _, removedPath := range removeAttachmentsForPage(attachmentIndex, pageID) {
			staleAttachmentPaths[removedPath] = struct{}{}
		}
	}

	for _, pageID := range changedPageIDs {
		page, ok := changedPages[pageID]
		if !ok {
			continue
		}

		refs := collectAttachmentRefs(page.BodyADF, page.ID)
		for _, removedPath := range removeStaleAttachmentsForPage(attachmentIndex, page.ID, refs) {
			staleAttachmentPaths[removedPath] = struct{}{}
		}

		refIDs := make([]string, 0, len(refs))
		for id := range refs {
			refIDs = append(refIDs, id)
		}
		sort.Strings(refIDs)

		for _, attachmentID := range refIDs {
			ref := refs[attachmentID]
			relAssetPath := buildAttachmentPath(ref)

			for existingPath, existingID := range attachmentIndex {
				if existingID == ref.AttachmentID && existingPath != relAssetPath {
					delete(attachmentIndex, existingPath)
					staleAttachmentPaths[existingPath] = struct{}{}
				}
			}

			attachmentIndex[relAssetPath] = ref.AttachmentID
			attachmentPathByID[ref.AttachmentID] = filepath.Join(spaceDir, filepath.FromSlash(relAssetPath))
		}
	}

	assetIDs := sortedStringKeys(attachmentPathByID)
	downloadedAssets := make([]string, 0, len(assetIDs))
	for _, attachmentID := range assetIDs {
		assetPath := attachmentPathByID[attachmentID]
		raw, err := remote.DownloadAttachment(ctx, attachmentID)
		if err != nil {
			return PullResult{}, fmt.Errorf("download attachment %s: %w", attachmentID, err)
		}
		if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
			return PullResult{}, fmt.Errorf("prepare attachment directory %s: %w", assetPath, err)
		}
		if err := os.WriteFile(assetPath, raw, 0o644); err != nil {
			return PullResult{}, fmt.Errorf("write attachment %s: %w", assetPath, err)
		}
		relAssetPath, relErr := filepath.Rel(spaceDir, assetPath)
		if relErr != nil {
			relAssetPath = assetPath
		}
		downloadedAssets = append(downloadedAssets, filepath.ToSlash(relAssetPath))
	}

	updatedMarkdown := make([]string, 0, len(changedPages))
	diagnostics := []PullDiagnostic{}
	changedPageIDsSorted := sortedStringKeys(changedPages)
	for _, pageID := range changedPageIDsSorted {
		page := changedPages[pageID]
		outputPath, ok := pagePathByIDAbs[page.ID]
		if !ok {
			return PullResult{}, fmt.Errorf("planned path missing for page %s", page.ID)
		}

		forward, err := converter.Forward(ctx, page.BodyADF, converter.ForwardConfig{
			LinkHook:  NewForwardLinkHook(outputPath, pagePathByIDAbs, opts.SpaceKey),
			MediaHook: NewForwardMediaHook(outputPath, attachmentPathByID),
		}, outputPath)
		if err != nil {
			return PullResult{}, fmt.Errorf("convert page %s: %w", page.ID, err)
		}

		doc := fs.MarkdownDocument{
			Frontmatter: fs.Frontmatter{
				Title:                  page.Title,
				ConfluencePageID:       page.ID,
				ConfluenceSpaceKey:     opts.SpaceKey,
				ConfluenceVersion:      page.Version,
				ConfluenceLastModified: page.LastModified.UTC().Format(time.RFC3339),
				ConfluenceParentPageID: page.ParentPageID,
			},
			Body: forward.Markdown,
		}

		if err := fs.WriteMarkdownDocument(outputPath, doc); err != nil {
			return PullResult{}, fmt.Errorf("write page %s: %w", page.ID, err)
		}

		relPath, relErr := filepath.Rel(spaceDir, outputPath)
		if relErr != nil {
			relPath = outputPath
		}
		relPath = filepath.ToSlash(relPath)
		updatedMarkdown = append(updatedMarkdown, relPath)

		for _, warning := range forward.Warnings {
			diagnostics = append(diagnostics, PullDiagnostic{
				Path:    relPath,
				Code:    string(warning.Type),
				Message: warning.Message,
			})
		}
	}

	deletedMarkdownSet := map[string]struct{}{}
	for oldPath, pageID := range state.PagePathIndex {
		newPath, exists := pagePathByIDRel[pageID]
		if !exists || normalizeRelPath(oldPath) != normalizeRelPath(newPath) {
			deletedMarkdownSet[normalizeRelPath(oldPath)] = struct{}{}
		}
	}

	deletedMarkdown := sortedStringKeys(deletedMarkdownSet)
	for _, relPath := range deletedMarkdown {
		absPath := filepath.Join(spaceDir, filepath.FromSlash(relPath))
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			return PullResult{}, fmt.Errorf("delete markdown %s: %w", relPath, err)
		}
	}

	assetsRoot := filepath.Join(spaceDir, "assets")
	deletedAssets := sortedStringKeys(staleAttachmentPaths)
	for _, relPath := range deletedAssets {
		if _, stillPresent := attachmentIndex[relPath]; stillPresent {
			continue
		}
		absPath := filepath.Join(spaceDir, filepath.FromSlash(relPath))
		if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
			return PullResult{}, fmt.Errorf("delete attachment %s: %w", relPath, err)
		}
		_ = removeEmptyParentDirs(filepath.Dir(absPath), assetsRoot)
	}

	state.PagePathIndex = invertPathByID(pagePathByIDRel)
	state.AttachmentIndex = attachmentIndex

	highWatermark := pullStartedAt.UTC()
	if maxRemoteModified.After(highWatermark) {
		highWatermark = maxRemoteModified.UTC()
	}
	state.LastPullHighWatermark = highWatermark.Format(time.RFC3339)

	return PullResult{
		State:            state,
		MaxVersion:       maxVersion,
		Diagnostics:      diagnostics,
		UpdatedMarkdown:  updatedMarkdown,
		DeletedMarkdown:  deletedMarkdown,
		DownloadedAssets: downloadedAssets,
		DeletedAssets:    deletedAssets,
	}, nil
}

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
	})
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

func listAllPages(ctx context.Context, remote PullRemote, opts confluence.PageListOptions) ([]confluence.Page, error) {
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

func listAllChanges(ctx context.Context, remote PullRemote, opts confluence.ChangeListOptions) ([]confluence.Change, error) {
	result := []confluence.Change{}
	start := opts.Start
	for {
		opts.Start = start
		changeResult, err := remote.ListChanges(ctx, opts)
		if err != nil {
			return nil, err
		}
		result = append(result, changeResult.Changes...)
		if !changeResult.HasMore {
			break
		}
		next := changeResult.NextStart
		if next <= start {
			next = start + opts.Limit
		}
		if next <= start {
			break
		}
		start = next
	}
	return result, nil
}

// PlanPagePaths builds deterministic markdown paths for remote pages.
//
// It preserves previously mapped paths from page_path_index when possible,
// then allocates unique sanitized filenames for newly discovered pages.
func PlanPagePaths(
	spaceDir string,
	previousPageIndex map[string]string,
	pages []confluence.Page,
) (map[string]string, map[string]string) {
	pageByID := map[string]confluence.Page{}
	for _, page := range pages {
		pageByID[page.ID] = page
	}

	absByID := map[string]string{}
	relByID := map[string]string{}
	usedRelPaths := map[string]struct{}{}

	previousPaths := sortedStringKeys(previousPageIndex)
	for _, previousPath := range previousPaths {
		pageID := previousPageIndex[previousPath]
		if _, exists := pageByID[pageID]; !exists {
			continue
		}
		relPath := normalizeRelPath(previousPath)
		if relPath == "" {
			continue
		}
		if _, used := usedRelPaths[relPath]; used {
			continue
		}
		usedRelPaths[relPath] = struct{}{}
		relByID[pageID] = relPath
		absByID[pageID] = filepath.Join(spaceDir, filepath.FromSlash(relPath))
	}

	remaining := make([]confluence.Page, 0, len(pages))
	for _, page := range pages {
		if _, assigned := relByID[page.ID]; assigned {
			continue
		}
		remaining = append(remaining, page)
	}
	sort.Slice(remaining, func(i, j int) bool {
		if remaining[i].Title == remaining[j].Title {
			return remaining[i].ID < remaining[j].ID
		}
		return strings.ToLower(remaining[i].Title) < strings.ToLower(remaining[j].Title)
	})

	for _, page := range remaining {
		title := strings.TrimSpace(page.Title)
		if title == "" {
			title = "page-" + page.ID
		}
		baseName := fs.SanitizeMarkdownFilename(title)
		relPath := ensureUniqueMarkdownPath(baseName, usedRelPaths)
		usedRelPaths[relPath] = struct{}{}
		relByID[page.ID] = relPath
		absByID[page.ID] = filepath.Join(spaceDir, filepath.FromSlash(relPath))
	}

	return absByID, relByID
}

func ensureUniqueMarkdownPath(baseName string, used map[string]struct{}) string {
	baseName = normalizeRelPath(baseName)
	if baseName == "" {
		baseName = "untitled.md"
	}
	if _, exists := used[baseName]; !exists {
		return baseName
	}

	ext := filepath.Ext(baseName)
	stem := strings.TrimSuffix(baseName, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func deletedPageIDs(previousPageIndex map[string]string, remotePages map[string]confluence.Page) []string {
	set := map[string]struct{}{}
	for _, pageID := range previousPageIndex {
		if _, exists := remotePages[pageID]; !exists {
			set[pageID] = struct{}{}
		}
	}
	return sortedStringKeys(set)
}

func removeAttachmentsForPage(attachmentIndex map[string]string, pageID string) []string {
	removed := []string{}
	for relPath := range attachmentIndex {
		if !attachmentBelongsToPage(relPath, pageID) {
			continue
		}
		removed = append(removed, normalizeRelPath(relPath))
		delete(attachmentIndex, relPath)
	}
	sort.Strings(removed)
	return removed
}

func removeStaleAttachmentsForPage(
	attachmentIndex map[string]string,
	pageID string,
	currentRefs map[string]attachmentRef,
) []string {
	removed := []string{}
	for relPath, attachmentID := range attachmentIndex {
		if !attachmentBelongsToPage(relPath, pageID) {
			continue
		}
		if _, keep := currentRefs[attachmentID]; keep {
			continue
		}
		removed = append(removed, normalizeRelPath(relPath))
		delete(attachmentIndex, relPath)
	}
	sort.Strings(removed)
	return removed
}

func attachmentBelongsToPage(relPath, pageID string) bool {
	relPath = normalizeRelPath(relPath)
	parts := strings.Split(relPath, "/")
	if len(parts) < 3 {
		return false
	}
	if parts[0] != "assets" {
		return false
	}
	return parts[1] == pageID
}

func collectAttachmentRefs(adfJSON []byte, defaultPageID string) map[string]attachmentRef {
	if len(adfJSON) == 0 {
		return map[string]attachmentRef{}
	}

	var raw any
	if err := json.Unmarshal(adfJSON, &raw); err != nil {
		return map[string]attachmentRef{}
	}

	out := map[string]attachmentRef{}
	walkADFNode(raw, func(node map[string]any) {
		nodeType, _ := node["type"].(string)
		if nodeType != "media" {
			return
		}
		attrs, _ := node["attrs"].(map[string]any)
		if len(attrs) == 0 {
			return
		}

		attachmentID := firstString(attrs,
			"attachmentId",
			"attachmentID",
			"mediaId",
			"id",
		)
		if attachmentID == "" {
			return
		}

		pageID := firstString(attrs, "pageId", "pageID", "contentId")
		if pageID == "" {
			pageID = defaultPageID
		}

		filename := firstString(attrs, "filename", "fileName", "name")
		if filename == "" {
			filename = "attachment"
		}

		out[attachmentID] = attachmentRef{
			PageID:       pageID,
			AttachmentID: attachmentID,
			Filename:     filename,
		}
	})

	return out
}

func walkADFNode(node any, visit func(map[string]any)) {
	switch typed := node.(type) {
	case map[string]any:
		visit(typed)
		for _, value := range typed {
			walkADFNode(value, visit)
		}
	case []any:
		for _, item := range typed {
			walkADFNode(item, visit)
		}
	}
}

func firstString(attrs map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, exists := attrs[key]
		if !exists {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func buildAttachmentPath(ref attachmentRef) string {
	filename := filepath.Base(strings.TrimSpace(ref.Filename))
	filename = fs.SanitizePathSegment(filename)
	if filename == "" {
		filename = "attachment"
	}
	pageID := fs.SanitizePathSegment(ref.PageID)
	if pageID == "" {
		pageID = "unknown-page"
	}

	name := fs.SanitizePathSegment(ref.AttachmentID) + "-" + filename
	return filepath.ToSlash(filepath.Join("assets", pageID, name))
}

func invertPathByID(pathByID map[string]string) map[string]string {
	out := make(map[string]string, len(pathByID))
	for id, path := range pathByID {
		out[normalizeRelPath(path)] = id
	}
	return out
}

func normalizeRelPath(path string) string {
	path = filepath.ToSlash(filepath.Clean(path))
	path = strings.TrimPrefix(path, "./")
	if path == "." {
		return ""
	}
	return path
}

func removeEmptyParentDirs(startDir, stopDir string) error {
	startDir = filepath.Clean(startDir)
	stopDir = filepath.Clean(stopDir)

	for {
		if !isSubpathOrSame(stopDir, startDir) {
			return nil
		}
		if startDir == stopDir {
			entries, err := os.ReadDir(startDir)
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if len(entries) == 0 {
				_ = os.Remove(startDir)
			}
			return nil
		}

		entries, err := os.ReadDir(startDir)
		if err != nil {
			if os.IsNotExist(err) {
				startDir = filepath.Dir(startDir)
				continue
			}
			return err
		}
		if len(entries) > 0 {
			return nil
		}
		if err := os.Remove(startDir); err != nil && !os.IsNotExist(err) {
			return err
		}
		startDir = filepath.Dir(startDir)
	}
}

func isSubpathOrSame(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[normalizeRelPath(key)] = value
	}
	return out
}

func sortedStringKeys[V any](in map[string]V) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
