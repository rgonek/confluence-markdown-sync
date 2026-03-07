package sync

import (
	"context"
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
	GetContentStatus(ctx context.Context, pageID string, pageStatus string) (string, error)
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
	GlobalPageIndex   GlobalPageIndex
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
	Path           string
	Code           string
	Message        string
	Category       string
	ActionRequired bool
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

	state := normalizePullState(opts.State)

	pullStartedAt := opts.PullStartedAt
	if pullStartedAt.IsZero() {
		pullStartedAt = time.Now().UTC()
	}
	overlapWindow := opts.OverlapWindow
	if overlapWindow <= 0 {
		overlapWindow = DefaultPullOverlapWindow
	}
	diagnostics := []PullDiagnostic{}
	capabilities := newTenantCapabilityCache()

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

	folderMode, folderModeDiags, err := capabilities.detectPullFolderMode(ctx, remote, pages)
	if err != nil {
		return PullResult{}, fmt.Errorf("probe folder capability: %w", err)
	}
	diagnostics = append(diagnostics, folderModeDiags...)

	contentStatusMode, contentStatusDiags := capabilities.detectPullContentStatusMode(ctx, remote, pages)
	diagnostics = append(diagnostics, contentStatusDiags...)

	folderByID, folderDiags, err := resolveFolderHierarchyFromPagesWithMode(ctx, remote, pages, folderMode)
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
	pathMoves := PlannedPagePathMoves(state.PagePathIndex, pagePathByIDRel)
	for _, move := range pathMoves {
		diagnostics = append(diagnostics, pagePathMoveDiagnostic(move))
	}

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
		for _, move := range pathMoves {
			changedSet[move.PageID] = struct{}{}
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

			if contentStatusMode == tenantContentStatusModeDisabled {
				existingFM, ok := readExistingFrontmatter(pageID)
				if ok && existingFM.Status != "" {
					page.ContentStatus = existingFM.Status
				}
			} else {
				status, err := remote.GetContentStatus(gCtx, pageID, page.Status)
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
					if err := os.Rename(tempName, assetPath); err != nil { //nolint:gosec // Path is controlled by application
						_ = os.Remove(tempName) //nolint:gosec // Path is controlled by application
						return fmt.Errorf("rename attachment file %s: %w", assetPath, err)
					}
					return nil
				}
				_ = os.Remove(tempName) //nolint:gosec // Path is controlled by application

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

		linkNotices := make([]ForwardLinkNotice, 0)
		forward, err := converter.Forward(ctx, page.BodyADF, converter.ForwardConfig{
			LinkHook: NewForwardLinkHookWithGlobalIndex(
				outputPath,
				spaceDir,
				pagePathByIDAbs,
				opts.GlobalPageIndex,
				opts.SpaceKey,
				func(notice ForwardLinkNotice) {
					linkNotices = append(linkNotices, notice)
				},
			),
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

		for _, notice := range linkNotices {
			diagnostics = append(diagnostics, PullDiagnostic{
				Path:    relPath,
				Code:    notice.Code,
				Message: notice.Message,
			})
		}
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
		Diagnostics:      NormalizePullDiagnostics(diagnostics),
		UpdatedMarkdown:  updatedMarkdown,
		DeletedMarkdown:  deletedMarkdown,
		DownloadedAssets: downloadedAssets,
		DeletedAssets:    deletedAssets,
	}, nil
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

func contextSleep(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
