package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	gosync "sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

const (
	// DefaultPullOverlapWindow is the default overlap window for incremental pull fetches.
	DefaultPullOverlapWindow = 5 * time.Minute
	pullPageBatchSize        = 100
	pullChangeBatchSize      = 100
	maxPaginationIterations  = 500
)

// PullRemote defines the remote operations required by pull orchestration.
type PullRemote interface {
	GetUser(ctx context.Context, accountID string) (confluence.User, error)
	GetSpace(ctx context.Context, spaceKey string) (confluence.Space, error)
	ListPages(ctx context.Context, opts confluence.PageListOptions) (confluence.PageListResult, error)
	GetFolder(ctx context.Context, folderID string) (confluence.Folder, error)
	ListChanges(ctx context.Context, opts confluence.ChangeListOptions) (confluence.ChangeListResult, error)
	GetPage(ctx context.Context, pageID string) (confluence.Page, error)
	GetContentStatus(ctx context.Context, pageID string) (string, error)
	GetLabels(ctx context.Context, pageID string) ([]string, error)
	ListAttachments(ctx context.Context, pageID string) ([]confluence.Attachment, error)
	DownloadAttachment(ctx context.Context, attachmentID string, pageID string, out io.Writer) error
}

// Progress defines a progress reporter.
type Progress interface {
	SetDescription(desc string)
	SetTotal(total int)
	SetCurrentItem(name string)
	Add(n int)
	Done()
}

// PullOptions controls pull orchestration behavior.
type PullOptions struct {
	SpaceKey          string
	SpaceDir          string
	State             fs.SpaceState
	PullStartedAt     time.Time
	OverlapWindow     time.Duration
	TargetPageID      string
	ForceFull         bool
	SkipMissingAssets bool
	OnDownloadError   func(attachmentID string, pageID string, err error) bool // return true to skip and continue
	Progress          Progress
	PrefetchedPages   []confluence.Page // pages fetched during estimate phase to avoid duplicate listing
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
	diagnostics := []PullDiagnostic{}

	userCache := map[string]string{}
	getUserDisplayName := func(ctx context.Context, accountID string) string {
		accountID = strings.TrimSpace(accountID)
		if accountID == "" {
			return ""
		}
		if name, ok := userCache[accountID]; ok {
			return name
		}
		user, err := remote.GetUser(ctx, accountID)
		if err != nil {
			userCache[accountID] = accountID // fallback to ID on error
			return accountID
		}
		name := strings.TrimSpace(user.DisplayName)
		if name == "" {
			name = accountID
		}
		userCache[accountID] = name
		return name
	}

	space, err := remote.GetSpace(ctx, opts.SpaceKey)
	if err != nil {
		return PullResult{}, fmt.Errorf("resolve space %q: %w", opts.SpaceKey, err)
	}
	state.SpaceKey = strings.TrimSpace(space.Key)
	if state.SpaceKey == "" {
		state.SpaceKey = strings.TrimSpace(opts.SpaceKey)
	}

	if opts.Progress != nil {
		opts.Progress.SetDescription("Scanning space for pages")
	}

	var pages []confluence.Page
	if len(opts.PrefetchedPages) > 0 {
		pages = opts.PrefetchedPages
	} else {
		pages, err = listAllPages(ctx, remote, confluence.PageListOptions{
			SpaceID:  space.ID,
			SpaceKey: opts.SpaceKey,
			Status:   "current",
			Limit:    pullPageBatchSize,
		}, opts.Progress)
		if err != nil {
			return PullResult{}, fmt.Errorf("list pages: %w", err)
		}
	}

	pages, err = recoverMissingPages(ctx, remote, space.ID, state.PagePathIndex, pages)
	if err != nil {
		return PullResult{}, fmt.Errorf("recover missing pages: %w", err)
	}

	if opts.Progress != nil {
		opts.Progress.SetTotal(len(pages))
	}

	folderByID, folderDiags, err := resolveFolderHierarchyFromPages(ctx, remote, pages)
	if err != nil {
		return PullResult{}, err
	}
	diagnostics = append(diagnostics, folderDiags...)

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

	pagePathByIDAbs, pagePathByIDRel := PlanPagePaths(spaceDir, state.PagePathIndex, pages, folderByID)

	if opts.Progress != nil {
		opts.Progress.SetDescription("Identifying changed pages")
	}
	changedPageIDs, err := selectChangedPageIDs(ctx, remote, opts, overlapWindow, pageByID)
	if err != nil {
		return PullResult{}, err
	}
	if strings.TrimSpace(opts.TargetPageID) == "" {
		changedSet := map[string]struct{}{}
		for _, pageID := range changedPageIDs {
			changedSet[pageID] = struct{}{}
		}
		for _, pageID := range movedPageIDs(state.PagePathIndex, pagePathByIDRel) {
			changedSet[pageID] = struct{}{}
		}
		changedPageIDs = sortedStringKeys(changedSet)
	}

	if opts.Progress != nil {
		opts.Progress.SetDescription("Fetching pages")
		opts.Progress.SetTotal(len(changedPageIDs))
	}

	changedPages := make(map[string]confluence.Page, len(changedPageIDs))
	var changedPagesMu gosync.Mutex
	var diagMu gosync.Mutex

	readExistingFrontmatter := func(pageID string) (fs.Frontmatter, bool) {
		absPath, ok := pagePathByIDAbs[pageID]
		if !ok {
			return fs.Frontmatter{}, false
		}
		raw, err := os.ReadFile(absPath) //nolint:gosec // path comes from planned in-scope page path map
		if err != nil {
			return fs.Frontmatter{}, false
		}
		doc, err := fs.ParseMarkdownDocument(raw)
		if err != nil {
			return fs.Frontmatter{}, false
		}
		return doc.Frontmatter, true
	}

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(5)

	for _, pageID := range changedPageIDs {
		pageID := pageID // copy for goroutine
		g.Go(func() error {
			if opts.Progress != nil {
				opts.Progress.SetCurrentItem(pageID)
			}

			page, err := remote.GetPage(gCtx, pageID)
			if err != nil {
				if opts.Progress != nil {
					opts.Progress.Add(1)
				}
				if errors.Is(err, confluence.ErrNotFound) {
					return nil
				}
				return fmt.Errorf("fetch page %s: %w", pageID, err)
			}

			status, err := remote.GetContentStatus(gCtx, pageID)
			if err != nil {
				existingFM, ok := readExistingFrontmatter(pageID)
				if ok && existingFM.Status != "" {
					page.ContentStatus = existingFM.Status
				}
				diagMu.Lock()
				diagnostics = append(diagnostics, PullDiagnostic{
					Path:    pageID,
					Code:    "CONTENT_STATUS_FETCH_FAILED",
					Message: fmt.Sprintf("fetch content status for page %s: %v", pageID, err),
				})
				diagMu.Unlock()
			} else {
				page.ContentStatus = status
			}

			labels, err := remote.GetLabels(gCtx, pageID)
			if err != nil {
				existingFM, ok := readExistingFrontmatter(pageID)
				if ok && len(existingFM.Labels) > 0 {
					page.Labels = existingFM.Labels
				}
				diagMu.Lock()
				diagnostics = append(diagnostics, PullDiagnostic{
					Path:    pageID,
					Code:    "LABELS_FETCH_FAILED",
					Message: fmt.Sprintf("fetch labels for page %s: %v", pageID, err),
				})
				diagMu.Unlock()
			} else {
				page.Labels = labels
			}

			changedPagesMu.Lock()
			changedPages[pageID] = page
			if page.Version > maxVersion {
				maxVersion = page.Version
			}
			if page.LastModified.After(maxRemoteModified) {
				maxRemoteModified = page.LastModified
			}
			changedPagesMu.Unlock()

			if opts.Progress != nil {
				opts.Progress.Add(1)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return PullResult{}, err
	}

	attachmentIndex := cloneStringMap(state.AttachmentIndex)
	// Build reverse index for O(1) lookups during planning
	pathByAttachmentID := make(map[string]string, len(attachmentIndex))
	for relPath, id := range attachmentIndex {
		pathByAttachmentID[id] = relPath
	}

	attachmentPathByID := map[string]string{}
	attachmentPageByID := map[string]string{}
	staleAttachmentPaths := map[string]struct{}{}

	deletedPageIDs := deletedPageIDs(state.PagePathIndex, pageByID)
	for _, pageID := range deletedPageIDs {
		for _, removedPath := range removeAttachmentsForPage(attachmentIndex, pageID) {
			staleAttachmentPaths[removedPath] = struct{}{}
			// Sync reverse index
			for id, p := range pathByAttachmentID {
				if p == removedPath {
					delete(pathByAttachmentID, id)
					break
				}
			}
		}
	}

	for _, pageID := range changedPageIDs {
		page, ok := changedPages[pageID]
		if !ok {
			continue
		}

		refs, diag := collectAttachmentRefs(page.BodyADF, page.ID)
		if diag != nil {
			diagnostics = append(diagnostics, *diag)
		}

		refs, resolvedUnknownCount, unresolvedUnknownCount, resolveErr := resolveUnknownAttachmentRefsByFilename(ctx, remote, page.ID, refs, attachmentIndex)
		if resolveErr != nil {
			diagnostics = append(diagnostics, PullDiagnostic{
				Path:    page.ID,
				Code:    "UNKNOWN_MEDIA_ID_LOOKUP_FAILED",
				Message: fmt.Sprintf("failed to resolve UNKNOWN_MEDIA_ID references for page %s: %v", page.ID, resolveErr),
			})
		}

		if resolvedUnknownCount > 0 {
			diagnostics = append(diagnostics, PullDiagnostic{
				Path:    page.ID,
				Code:    "UNKNOWN_MEDIA_ID_RESOLVED",
				Message: fmt.Sprintf("resolved %d UNKNOWN_MEDIA_ID reference(s) by filename for page %s", resolvedUnknownCount, page.ID),
			})
		}

		if unresolvedUnknownCount == 0 {
			for _, removedPath := range removeStaleAttachmentsForPage(attachmentIndex, page.ID, refs) {
				staleAttachmentPaths[removedPath] = struct{}{}
				// Sync reverse index
				for id, p := range pathByAttachmentID {
					if p == removedPath {
						delete(pathByAttachmentID, id)
						break
					}
				}
			}
		} else {
			diagnostics = append(diagnostics, PullDiagnostic{
				Path:    page.ID,
				Code:    "UNKNOWN_MEDIA_ID_UNRESOLVED",
				Message: fmt.Sprintf("%d UNKNOWN_MEDIA_ID reference(s) remain unresolved for page %s; stale attachment pruning skipped for safety", unresolvedUnknownCount, page.ID),
			})
		}

		refIDs := make([]string, 0, len(refs))
		for id := range refs {
			refIDs = append(refIDs, id)
		}
		sort.Strings(refIDs)

		for _, attachmentID := range refIDs {
			ref := refs[attachmentID]
			if isUnknownMediaID(ref.AttachmentID) {
				continue
			}
			relAssetPath := buildAttachmentPath(ref)

			// Optimized: check if this attachment ID was already at a different path
			if existingPath, found := pathByAttachmentID[ref.AttachmentID]; found && existingPath != relAssetPath {
				delete(attachmentIndex, existingPath)
				delete(pathByAttachmentID, ref.AttachmentID)
				staleAttachmentPaths[existingPath] = struct{}{}
			}

			attachmentIndex[relAssetPath] = ref.AttachmentID
			pathByAttachmentID[ref.AttachmentID] = relAssetPath
			attachmentPathByID[ref.AttachmentID] = filepath.Join(spaceDir, filepath.FromSlash(relAssetPath))
			attachmentPageByID[ref.AttachmentID] = ref.PageID
		}
	}

	assetIDs := sortedStringKeys(attachmentPathByID)
	downloadedAssets := make([]string, 0, len(assetIDs))

	if opts.Progress != nil {
		opts.Progress.SetDescription("Downloading assets")
		opts.Progress.SetTotal(len(assetIDs))
		if len(assetIDs) == 0 {
			opts.Progress.SetCurrentItem("")
		}
	}

	for _, attachmentID := range assetIDs {
		assetPath := attachmentPathByID[attachmentID]
		pageID := attachmentPageByID[attachmentID]

		if opts.Progress != nil {
			opts.Progress.SetCurrentItem(filepath.Base(assetPath))
		}

		if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
			return PullResult{}, fmt.Errorf("prepare attachment directory %s: %w", assetPath, err)
		}

		err := func() error {
			var lastErr error
			for retry := 0; retry < 3; retry++ {
				if retry > 0 {
					if err := contextSleep(ctx, time.Duration(retry)*time.Second); err != nil {
						return err
					}
				}

				tempFile, err := os.CreateTemp(filepath.Dir(assetPath), "asset-*")
				if err != nil {
					return fmt.Errorf("create temp attachment file %s: %w", assetPath, err)
				}
				tempName := tempFile.Name()

				downloadErr := remote.DownloadAttachment(ctx, attachmentID, pageID, tempFile)
				closeErr := tempFile.Close()

				if downloadErr == nil && closeErr == nil {
					if err := os.Rename(tempName, assetPath); err != nil {
						_ = os.Remove(tempName)
						return fmt.Errorf("rename attachment file %s: %w", assetPath, err)
					}
					return nil
				}
				_ = os.Remove(tempName)

				if downloadErr == nil && closeErr != nil {
					return fmt.Errorf("close temp attachment file %s: %w", assetPath, closeErr)
				}

				lastErr = downloadErr
				if errors.Is(downloadErr, confluence.ErrNotFound) {
					break // No point in retrying 404
				}
			}
			return lastErr
		}()

		if err != nil {
			// Clean up partially downloaded file
			_ = os.Remove(assetPath)

			skip := false
			if errors.Is(err, confluence.ErrNotFound) && opts.SkipMissingAssets {
				skip = true
			} else if opts.OnDownloadError != nil && opts.OnDownloadError(attachmentID, pageID, err) {
				skip = true
			}

			if skip {
				diagnostics = append(diagnostics, PullDiagnostic{
					Path:    attachmentID,
					Code:    "ATTACHMENT_DOWNLOAD_SKIPPED",
					Message: fmt.Sprintf("download attachment %s (page %s) failed, skipping: %v", attachmentID, pageID, err),
				})
				if opts.Progress != nil {
					opts.Progress.Add(1)
				}
				continue
			}
			return PullResult{}, fmt.Errorf("download attachment %s (page %s): %w", attachmentID, pageID, err)
		}

		relAssetPath, relErr := filepath.Rel(spaceDir, assetPath)
		if relErr != nil {
			relAssetPath = assetPath
		}
		downloadedAssets = append(downloadedAssets, filepath.ToSlash(relAssetPath))

		if opts.Progress != nil {
			opts.Progress.Add(1)
		}
	}

	if opts.Progress != nil {
		opts.Progress.SetCurrentItem("")
	}

	updatedMarkdown := make([]string, 0, len(changedPages))
	changedPageIDsSorted := sortedStringKeys(changedPages)

	if opts.Progress != nil {
		opts.Progress.SetDescription("Writing markdown")
		opts.Progress.SetTotal(len(changedPageIDsSorted))
		if len(changedPageIDsSorted) == 0 {
			opts.Progress.SetCurrentItem("")
		}
	}

	for _, pageID := range changedPageIDsSorted {
		page := changedPages[pageID]
		outputPath, ok := pagePathByIDAbs[page.ID]
		if !ok {
			return PullResult{}, fmt.Errorf("planned path missing for page %s", page.ID)
		}

		if opts.Progress != nil {
			opts.Progress.SetCurrentItem(filepath.Base(outputPath))
		}

		forward, err := converter.Forward(ctx, page.BodyADF, converter.ForwardConfig{
			LinkHook:  NewForwardLinkHook(outputPath, pagePathByIDAbs, opts.SpaceKey),
			MediaHook: NewForwardMediaHook(outputPath, attachmentPathByID),
		}, outputPath)
		if err != nil {
			return PullResult{}, fmt.Errorf("convert page %s: %w", page.ID, err)
		}

		var createdDate, lastModifiedDate string
		if !page.CreatedAt.IsZero() {
			createdDate = page.CreatedAt.Format(time.RFC3339)
		}
		if !page.LastModified.IsZero() {
			lastModifiedDate = page.LastModified.Format(time.RFC3339)
		}

		doc := fs.MarkdownDocument{
			Frontmatter: fs.Frontmatter{
				Title:     page.Title,
				ID:        page.ID,
				Space:     opts.SpaceKey,
				Version:   page.Version,
				State:     page.Status,
				Status:    page.ContentStatus,
				Labels:    page.Labels,
				CreatedBy: getUserDisplayName(ctx, page.AuthorID),
				CreatedAt: createdDate,
				UpdatedBy: getUserDisplayName(ctx, page.LastModifiedAuthorID),
				UpdatedAt: lastModifiedDate,
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

		if opts.Progress != nil {
			opts.Progress.Add(1)
		}
	}

	if opts.Progress != nil {
		opts.Progress.SetCurrentItem("")
		opts.Progress.Done()
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
		_ = removeEmptyParentDirs(filepath.Dir(absPath), spaceDir)
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

	folderPathIndex := buildFolderPathIndex(folderByID, pageByID)
	state.FolderPathIndex = folderPathIndex

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

// PlanPagePaths builds deterministic markdown paths for remote pages.
//
// It preserves previously mapped paths from page_path_index when possible,
// then allocates unique sanitized filenames for newly discovered pages.
func PlanPagePaths(
	spaceDir string,
	previousPageIndex map[string]string,
	pages []confluence.Page,
	folderByID map[string]confluence.Folder,
) (map[string]string, map[string]string) {
	pageByID := map[string]confluence.Page{}
	for _, page := range pages {
		pageByID[page.ID] = page
	}
	if folderByID == nil {
		folderByID = map[string]confluence.Folder{}
	}
	previousPathByID := map[string]string{}
	for _, previousPath := range sortedStringKeys(previousPageIndex) {
		pageID := previousPageIndex[previousPath]
		if _, exists := pageByID[pageID]; !exists {
			continue
		}
		normalized := normalizeRelPath(previousPath)
		if normalized == "" {
			continue
		}
		if _, exists := previousPathByID[pageID]; !exists {
			previousPathByID[pageID] = normalized
		}
	}

	absByID := map[string]string{}
	relByID := map[string]string{}
	usedRelPaths := map[string]struct{}{}

	type pagePathPlan struct {
		ID          string
		BaseRelPath string
	}
	plans := make([]pagePathPlan, 0, len(pages))
	for _, page := range pages {
		baseRelPath := plannedPageRelPath(page, pageByID, folderByID)
		if previousPath := previousPathByID[page.ID]; previousPath != "" && sameParentDirectory(previousPath, baseRelPath) {
			baseRelPath = previousPath
		}

		plans = append(plans, pagePathPlan{
			ID:          page.ID,
			BaseRelPath: baseRelPath,
		})
	}

	sort.Slice(plans, func(i, j int) bool {
		if plans[i].BaseRelPath == plans[j].BaseRelPath {
			return plans[i].ID < plans[j].ID
		}
		return plans[i].BaseRelPath < plans[j].BaseRelPath
	})

	for _, plan := range plans {
		relPath := ensureUniqueMarkdownPath(plan.BaseRelPath, usedRelPaths)
		usedRelPaths[relPath] = struct{}{}
		relByID[plan.ID] = relPath
		absByID[plan.ID] = filepath.Join(spaceDir, filepath.FromSlash(relPath))
	}

	return absByID, relByID
}

func plannedPageRelPath(page confluence.Page, pageByID map[string]confluence.Page, folderByID map[string]confluence.Folder) string {
	title := strings.TrimSpace(page.Title)
	if title == "" {
		title = "page-" + page.ID
	}
	filename := fs.SanitizeMarkdownFilename(title)

	ancestorSegments, ok := ancestorPathSegments(strings.TrimSpace(page.ParentPageID), strings.TrimSpace(page.ParentType), pageByID, folderByID)
	if !ok {
		// Fallback to flat if hierarchy is broken
		return normalizeRelPath(filename)
	}

	parts := append(ancestorSegments, filename)
	return normalizeRelPath(filepath.Join(parts...))
}

func ancestorPathSegments(parentID string, parentType string, pageByID map[string]confluence.Page, folderByID map[string]confluence.Folder) ([]string, bool) {
	currentID := strings.TrimSpace(parentID)
	currentType := strings.ToLower(strings.TrimSpace(parentType))
	if currentID == "" {
		return nil, true
	}
	if currentType == "" {
		currentType = "page"
	}

	visited := map[string]struct{}{}
	segmentsReversed := []string{}
	for currentID != "" {
		if _, seen := visited[currentID]; seen {
			return nil, false
		}
		visited[currentID] = struct{}{}

		var title string
		var nextID string
		var nextType string

		if currentType == "folder" {
			folder, ok := folderByID[currentID]
			if !ok {
				return nil, false
			}
			title = strings.TrimSpace(folder.Title)
			if title == "" {
				title = "folder-" + folder.ID
			}
			nextID = strings.TrimSpace(folder.ParentID)
			nextType = strings.ToLower(strings.TrimSpace(folder.ParentType))
			if nextType == "" {
				nextType = "folder"
			}
		} else {
			parentPage, ok := pageByID[currentID]
			if !ok {
				return nil, false
			}
			title = strings.TrimSpace(parentPage.Title)
			if title == "" {
				title = "page-" + parentPage.ID
			}
			nextID = strings.TrimSpace(parentPage.ParentPageID)
			nextType = strings.ToLower(strings.TrimSpace(parentPage.ParentType))
			if nextType == "" {
				nextType = "page"
			}
		}

		// Folders always contribute a directory segment (even top-level folders).
		// Pages only contribute a segment when they themselves have a parent; the
		// space-root page (no parent) does not create its own subdirectory.
		if currentType == "folder" || nextID != "" {
			segmentsReversed = append(segmentsReversed, fs.SanitizePathSegment(title))
		}

		currentID = nextID
		currentType = nextType
	}

	segments := make([]string, 0, len(segmentsReversed))
	for i := len(segmentsReversed) - 1; i >= 0; i-- {
		segments = append(segments, segmentsReversed[i])
	}
	return segments, true
}

func sameParentDirectory(pathA, pathB string) bool {
	dirA := normalizeRelPath(filepath.Dir(pathA))
	dirB := normalizeRelPath(filepath.Dir(pathB))
	return dirA == dirB
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

func movedPageIDs(previousPageIndex map[string]string, nextPathByID map[string]string) []string {
	set := map[string]struct{}{}
	for previousPath, pageID := range previousPageIndex {
		nextPath, exists := nextPathByID[pageID]
		if !exists {
			continue
		}
		if normalizeRelPath(previousPath) != normalizeRelPath(nextPath) {
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

func collectAttachmentRefs(adfJSON []byte, defaultPageID string) (map[string]attachmentRef, *PullDiagnostic) {
	if len(adfJSON) == 0 {
		return map[string]attachmentRef{}, nil
	}

	var raw any
	if err := json.Unmarshal(adfJSON, &raw); err != nil {
		return map[string]attachmentRef{}, &PullDiagnostic{
			Path:    defaultPageID,
			Code:    "MALFORMED_ADF",
			Message: fmt.Sprintf("failed to parse ADF for page %s: %v", defaultPageID, err),
		}
	}

	out := map[string]attachmentRef{}
	unknownRefSeq := 0
	walkADFNode(raw, func(node map[string]any) {
		nodeType, _ := node["type"].(string)
		if nodeType != "media" && nodeType != "mediaInline" && nodeType != "image" && nodeType != "file" {
			return
		}
		attrs, _ := node["attrs"].(map[string]any)
		if len(attrs) == 0 {
			return
		}

		attachmentID := firstString(attrs,
			"id",
			"attachmentId",
			"attachmentID",
			"mediaId",
			"fileId",
			"fileID",
		)
		if attachmentID == "" {
			return
		}

		pageID := firstString(attrs, "pageId", "pageID", "contentId")
		if pageID == "" {
			collection := firstString(attrs, "collection")
			if strings.HasPrefix(collection, "contentId-") {
				pageID = strings.TrimPrefix(collection, "contentId-")
			}
		}
		if pageID == "" {
			pageID = defaultPageID
		}

		filename := firstString(attrs, "filename", "fileName", "name", "alt", "title")
		if filename == "" {
			filename = "attachment"
		}

		refKey := attachmentID
		if isUnknownMediaID(attachmentID) {
			refKey = fmt.Sprintf("unknown-media-%s-%d", normalizeAttachmentFilename(filename), unknownRefSeq)
			unknownRefSeq++
		}

		out[refKey] = attachmentRef{
			PageID:       pageID,
			AttachmentID: attachmentID,
			Filename:     filename,
		}
	})

	return out, nil
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

func isUnknownMediaID(attachmentID string) bool {
	return strings.EqualFold(strings.TrimSpace(attachmentID), "UNKNOWN_MEDIA_ID")
}

func resolveUnknownAttachmentRefsByFilename(
	ctx context.Context,
	remote PullRemote,
	pageID string,
	refs map[string]attachmentRef,
	attachmentIndex map[string]string,
) (map[string]attachmentRef, int, int, error) {
	if len(refs) == 0 {
		return refs, 0, 0, nil
	}

	resolved := 0
	refs = cloneAttachmentRefs(refs)

	localFilenameIndex := buildLocalAttachmentFilenameIndex(attachmentIndex, pageID)
	unresolvedKeys := make([]string, 0)
	for _, key := range sortedStringKeys(refs) {
		ref := refs[key]
		if !isUnknownMediaID(ref.AttachmentID) {
			continue
		}

		if resolvedID, ok := resolveAttachmentIDByFilename(localFilenameIndex, ref.Filename); ok {
			delete(refs, key)
			ref.AttachmentID = resolvedID
			refs[resolvedID] = ref
			resolved++
			continue
		}

		unresolvedKeys = append(unresolvedKeys, key)
	}

	if len(unresolvedKeys) == 0 {
		return refs, resolved, 0, nil
	}

	remoteAttachments, err := remote.ListAttachments(ctx, pageID)
	if err != nil {
		return refs, resolved, len(unresolvedKeys), err
	}
	remoteFilenameIndex := buildRemoteAttachmentFilenameIndex(remoteAttachments)

	unresolved := 0
	for _, key := range unresolvedKeys {
		ref, ok := refs[key]
		if !ok || !isUnknownMediaID(ref.AttachmentID) {
			continue
		}

		resolvedID, ok := resolveAttachmentIDByFilename(remoteFilenameIndex, ref.Filename)
		if !ok {
			unresolved++
			continue
		}

		delete(refs, key)
		ref.AttachmentID = resolvedID
		refs[resolvedID] = ref
		resolved++
	}

	return refs, resolved, unresolved, nil
}

func cloneAttachmentRefs(refs map[string]attachmentRef) map[string]attachmentRef {
	out := make(map[string]attachmentRef, len(refs))
	for key, ref := range refs {
		out[key] = ref
	}
	return out
}

func buildLocalAttachmentFilenameIndex(attachmentIndex map[string]string, pageID string) map[string][]string {
	pageID = strings.TrimSpace(pageID)
	byFilename := map[string][]string{}

	for relPath, attachmentID := range attachmentIndex {
		if strings.TrimSpace(attachmentID) == "" {
			continue
		}
		if pageID != "" && !attachmentBelongsToPage(relPath, pageID) {
			continue
		}

		filename := attachmentFilenameFromAssetPath(relPath, attachmentID)
		filenameKey := normalizeAttachmentFilename(filename)
		if filenameKey == "" {
			continue
		}
		byFilename[filenameKey] = appendUniqueString(byFilename[filenameKey], strings.TrimSpace(attachmentID))
	}

	return byFilename
}

func buildRemoteAttachmentFilenameIndex(attachments []confluence.Attachment) map[string][]string {
	byFilename := map[string][]string{}
	for _, attachment := range attachments {
		attachmentID := strings.TrimSpace(attachment.ID)
		if attachmentID == "" {
			continue
		}

		filenameKey := normalizeAttachmentFilename(attachment.Filename)
		if filenameKey == "" {
			continue
		}
		byFilename[filenameKey] = appendUniqueString(byFilename[filenameKey], attachmentID)
	}
	return byFilename
}

func resolveAttachmentIDByFilename(byFilename map[string][]string, filename string) (string, bool) {
	filenameKey := normalizeAttachmentFilename(filename)
	if filenameKey == "" {
		return "", false
	}

	matches := byFilename[filenameKey]
	if len(matches) != 1 {
		return "", false
	}

	attachmentID := strings.TrimSpace(matches[0])
	if attachmentID == "" {
		return "", false
	}
	return attachmentID, true
}

func attachmentFilenameFromAssetPath(relPath, attachmentID string) string {
	base := filepath.Base(relPath)
	prefix := fs.SanitizePathSegment(strings.TrimSpace(attachmentID))
	if prefix == "" {
		return base
	}
	prefix += "-"
	if strings.HasPrefix(base, prefix) {
		filename := strings.TrimPrefix(base, prefix)
		if strings.TrimSpace(filename) != "" {
			return filename
		}
	}
	return base
}

func normalizeAttachmentFilename(filename string) string {
	filename = strings.TrimSpace(filepath.Base(filename))
	if filename == "" {
		return ""
	}
	filename = fs.SanitizePathSegment(filename)
	if filename == "" {
		return ""
	}
	return strings.ToLower(filename)
}

func appendUniqueString(values []string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return values
	}
	for _, existing := range values {
		if existing == candidate {
			return values
		}
	}
	return append(values, candidate)
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

type recoveryRemote interface {
	GetPage(ctx context.Context, pageID string) (confluence.Page, error)
}

func recoverMissingPages(ctx context.Context, remote recoveryRemote, spaceID string, localPageIDs map[string]string, remotePages []confluence.Page) ([]confluence.Page, error) {
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

		// Fetch missing page individually
		page, err := remote.GetPage(ctx, id)
		if err != nil {
			if errors.Is(err, confluence.ErrNotFound) || errors.Is(err, confluence.ErrArchived) {
				continue // Truly deleted
			}
			var apiErr *confluence.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
				continue
			}
			return nil, err
		}

		// If it belongs to the same space and is syncable, include it.
		if page.SpaceID == spaceID && IsSyncableRemotePageStatus(page.Status) {
			result = append(result, page)
			remoteByID[id] = struct{}{}
		}
	}
	return result, nil
}

func sortedStringKeys[V any](in map[string]V) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func contextSleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func buildFolderPathIndex(folderByID map[string]confluence.Folder, pageByID map[string]confluence.Page) map[string]string {
	if len(folderByID) == 0 {
		return nil
	}

	folderPathIndex := make(map[string]string)

	for folderID := range folderByID {
		localPath := buildFolderLocalPath(folderID, folderByID, pageByID)
		if localPath != "" {
			folderPathIndex[localPath] = folderID
		}
	}

	if len(folderPathIndex) == 0 {
		return nil
	}
	return folderPathIndex
}

func buildFolderLocalPath(folderID string, folderByID map[string]confluence.Folder, pageByID map[string]confluence.Page) string {
	segments := []string{}

	currentID := folderID
	currentType := "folder"

	for currentID != "" {
		var title string
		var nextID string
		var nextType string

		if currentType == "folder" {
			folder, ok := folderByID[currentID]
			if !ok {
				break
			}
			title = strings.TrimSpace(folder.Title)
			if title == "" {
				title = "folder-" + folder.ID
			}
			nextID = strings.TrimSpace(folder.ParentID)
			nextType = strings.ToLower(strings.TrimSpace(folder.ParentType))
			if nextType == "" {
				nextType = "folder"
			}
		} else {
			page, ok := pageByID[currentID]
			if !ok {
				break
			}
			title = strings.TrimSpace(page.Title)
			if title == "" {
				title = "page-" + page.ID
			}
			nextID = strings.TrimSpace(page.ParentPageID)
			nextType = strings.ToLower(strings.TrimSpace(page.ParentType))
			if nextType == "" {
				nextType = "page"
			}
		}

		segments = append(segments, fs.SanitizePathSegment(title))

		currentID = nextID
		currentType = nextType
	}

	if len(segments) == 0 {
		return ""
	}

	for i, j := 0, len(segments)-1; i < j; i, j = i+1, j-1 {
		segments[i], segments[j] = segments[j], segments[i]
	}

	return filepath.Join(segments...)
}
