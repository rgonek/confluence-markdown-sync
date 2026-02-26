package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

const pushPageBatchSize = 100

// PushRemote defines remote operations required by push orchestration.
type PushRemote interface {
	GetSpace(ctx context.Context, spaceKey string) (confluence.Space, error)
	ListPages(ctx context.Context, opts confluence.PageListOptions) (confluence.PageListResult, error)
	GetPage(ctx context.Context, pageID string) (confluence.Page, error)
	GetContentStatus(ctx context.Context, pageID string) (string, error)
	SetContentStatus(ctx context.Context, pageID string, statusName string) error
	DeleteContentStatus(ctx context.Context, pageID string) error
	GetLabels(ctx context.Context, pageID string) ([]string, error)
	AddLabels(ctx context.Context, pageID string, labels []string) error
	RemoveLabel(ctx context.Context, pageID string, labelName string) error
	CreatePage(ctx context.Context, input confluence.PageUpsertInput) (confluence.Page, error)
	UpdatePage(ctx context.Context, pageID string, input confluence.PageUpsertInput) (confluence.Page, error)
	ArchivePages(ctx context.Context, pageIDs []string) (confluence.ArchiveResult, error)
	WaitForArchiveTask(ctx context.Context, taskID string, opts confluence.ArchiveTaskWaitOptions) (confluence.ArchiveTaskStatus, error)
	DeletePage(ctx context.Context, pageID string, hardDelete bool) error
	UploadAttachment(ctx context.Context, input confluence.AttachmentUploadInput) (confluence.Attachment, error)
	DeleteAttachment(ctx context.Context, attachmentID string, pageID string) error
	CreateFolder(ctx context.Context, input confluence.FolderCreateInput) (confluence.Folder, error)
	MovePage(ctx context.Context, pageID string, targetID string) error
}

// PushConflictPolicy controls remote-ahead conflict behavior.
type PushConflictPolicy string

const (
	PushConflictPolicyPullMerge PushConflictPolicy = "pull-merge"
	PushConflictPolicyForce     PushConflictPolicy = "force"
	PushConflictPolicyCancel    PushConflictPolicy = "cancel"
)

// PushChangeType is the git-derived file change type for push planning.
type PushChangeType string

const (
	PushChangeAdd      PushChangeType = "A"
	PushChangeModify   PushChangeType = "M"
	PushChangeDelete   PushChangeType = "D"
	PushChangeTypeNone PushChangeType = ""
)

// PushFileChange captures one changed markdown path inside a space scope.
type PushFileChange struct {
	Type PushChangeType
	Path string
}

// PushOptions controls push orchestration.
type PushOptions struct {
	SpaceKey            string
	SpaceDir            string
	Domain              string
	State               fs.SpaceState
	GlobalPageIndex     GlobalPageIndex
	Changes             []PushFileChange
	ConflictPolicy      PushConflictPolicy
	HardDelete          bool
	KeepOrphanAssets    bool
	DryRun              bool
	ArchiveTimeout      time.Duration
	ArchivePollInterval time.Duration
	Progress            Progress
}

// PushCommitPlan describes local paths and metadata for one push commit.
type PushCommitPlan struct {
	Path        string
	Deleted     bool
	PageID      string
	PageTitle   string
	Version     int
	SpaceKey    string
	URL         string
	StagedPaths []string
}

// PushDiagnostic captures non-fatal push diagnostics.
type PushDiagnostic struct {
	Path    string
	Code    string
	Message string
}

// PushResult captures outputs of push orchestration.
type PushResult struct {
	State       fs.SpaceState
	Commits     []PushCommitPlan
	Diagnostics []PushDiagnostic
}

type pushMetadataSnapshot struct {
	ContentStatus string
	Labels        []string
}

type pushContentSnapshot struct {
	SpaceID      string
	Title        string
	ParentPageID string
	Status       string
	BodyADF      json.RawMessage
}

type rollbackAttachment struct {
	PageID       string
	AttachmentID string
	Path         string
}

type pushRollbackTracker struct {
	relPath            string
	createdPageID      string
	uploadedAssets     []rollbackAttachment
	contentPageID      string
	contentSnapshot    *pushContentSnapshot
	contentRestoreReq  bool
	metadataPageID     string
	metadataSnapshot   *pushMetadataSnapshot
	metadataRestoreReq bool
	diagnostics        *[]PushDiagnostic
}

// PushConflictError indicates a remote-ahead page conflict.
type PushConflictError struct {
	Path          string
	PageID        string
	LocalVersion  int
	RemoteVersion int
	Policy        PushConflictPolicy
}

func (e *PushConflictError) Error() string {
	return fmt.Sprintf(
		"remote version conflict for %s (page %s): local=%d remote=%d policy=%s",
		e.Path,
		e.PageID,
		e.LocalVersion,
		e.RemoteVersion,
		e.Policy,
	)
}

// Push executes the v1 push sync loop for in-scope markdown changes.
func Push(ctx context.Context, remote PushRemote, opts PushOptions) (PushResult, error) {
	if strings.TrimSpace(opts.SpaceKey) == "" {
		return PushResult{}, errors.New("space key is required")
	}
	if strings.TrimSpace(opts.SpaceDir) == "" {
		return PushResult{}, errors.New("space directory is required")
	}
	if len(opts.Changes) == 0 {
		state := opts.State
		state = normalizePushState(state)
		return PushResult{State: state}, nil
	}

	spaceDir, err := filepath.Abs(opts.SpaceDir)
	if err != nil {
		return PushResult{}, fmt.Errorf("resolve space directory: %w", err)
	}
	opts.SpaceDir = spaceDir

	state := normalizePushState(opts.State)
	policy := normalizeConflictPolicy(opts.ConflictPolicy)

	space, err := remote.GetSpace(ctx, opts.SpaceKey)
	if err != nil {
		return PushResult{}, fmt.Errorf("resolve space %q: %w", opts.SpaceKey, err)
	}

	pages, err := listAllPushPages(ctx, remote, confluence.PageListOptions{
		SpaceID:  space.ID,
		SpaceKey: opts.SpaceKey,
		Status:   "current",
		Limit:    pushPageBatchSize,
	})
	if err != nil {
		return PushResult{}, fmt.Errorf("list pages: %w", err)
	}

	pages, err = recoverMissingPages(ctx, remote, space.ID, state.PagePathIndex, pages)
	if err != nil {
		return PushResult{}, fmt.Errorf("recover missing pages: %w", err)
	}

	remotePageByID := make(map[string]confluence.Page, len(pages))

	for _, page := range pages {
		remotePageByID[page.ID] = page
	}

	pageIDByPath, err := BuildPageIndex(spaceDir)
	if err != nil {
		return PushResult{}, fmt.Errorf("build page index: %w", err)
	}

	pageTitleByPath, err := buildLocalPageTitleIndex(spaceDir)
	if err != nil {
		return PushResult{}, fmt.Errorf("build title index: %w", err)
	}

	attachmentIDByPath := cloneStringMap(state.AttachmentIndex)
	folderIDByPath := cloneStringMap(state.FolderPathIndex)
	changes := normalizePushChanges(opts.Changes)
	commits := make([]PushCommitPlan, 0, len(changes))
	diagnostics := make([]PushDiagnostic, 0)
	if err := seedPendingPageIDsForPushChanges(opts.SpaceDir, changes, pageIDByPath); err != nil {
		return PushResult{}, fmt.Errorf("seed pending page ids: %w", err)
	}
	if err := runPushUpsertPreflight(ctx, opts, changes, pageIDByPath, attachmentIDByPath); err != nil {
		return PushResult{}, err
	}
	precreatedPages, err := precreatePendingPushPages(
		ctx,
		remote,
		space,
		opts,
		state,
		changes,
		pageIDByPath,
		pageTitleByPath,
		folderIDByPath,
		&diagnostics,
	)
	if err != nil {
		return PushResult{}, err
	}
	pendingPrecreatedPages := clonePageMap(precreatedPages)

	if opts.Progress != nil {
		opts.Progress.SetDescription("Pushing changes")
		opts.Progress.SetTotal(len(changes))
	}

	for _, change := range changes {
		relPath := normalizeRelPath(change.Path)
		if relPath == "" {
			if opts.Progress != nil {
				opts.Progress.Add(1)
			}
			continue
		}

		switch change.Type {
		case PushChangeDelete:
			commit, err := pushDeletePage(ctx, remote, opts, state, remotePageByID, relPath, &diagnostics)
			if err != nil {
				if !opts.DryRun {
					cleanupPendingPrecreatedPages(ctx, remote, pendingPrecreatedPages, &diagnostics)
				}
				return PushResult{State: state, Commits: commits, Diagnostics: diagnostics}, err
			}
			if commit.Path != "" {
				commits = append(commits, commit)
			}
		case PushChangeAdd, PushChangeModify:
			delete(pendingPrecreatedPages, relPath)
			commit, err := pushUpsertPage(
				ctx,
				remote,
				space,
				opts,
				state,
				policy,
				pageIDByPath,
				pageTitleByPath,
				attachmentIDByPath,
				folderIDByPath,
				remotePageByID,
				relPath,
				precreatedPages,
				&diagnostics,
			)
			if err != nil {
				if !opts.DryRun {
					cleanupPendingPrecreatedPages(ctx, remote, pendingPrecreatedPages, &diagnostics)
				}
				return PushResult{State: state, Commits: commits, Diagnostics: diagnostics}, err
			}
			if commit.Path != "" {
				commits = append(commits, commit)
			}
		default:
			if opts.Progress != nil {
				opts.Progress.Add(1)
			}
			continue
		}

		if opts.Progress != nil {
			opts.Progress.Add(1)
		}
	}

	if opts.Progress != nil {
		opts.Progress.Done()
	}

	state.AttachmentIndex = attachmentIDByPath
	state.FolderPathIndex = folderIDByPath

	return PushResult{
		State:       state,
		Commits:     commits,
		Diagnostics: diagnostics,
	}, nil
}

func pushDeletePage(
	ctx context.Context,
	remote PushRemote,
	opts PushOptions,
	state fs.SpaceState,
	remotePageByID map[string]confluence.Page,
	relPath string,
	diagnostics *[]PushDiagnostic,
) (PushCommitPlan, error) {
	pageID := strings.TrimSpace(state.PagePathIndex[relPath])
	if pageID == "" {
		return PushCommitPlan{}, nil
	}

	page := remotePageByID[pageID]
	if opts.HardDelete {
		if err := remote.DeletePage(ctx, pageID, true); err != nil && !errors.Is(err, confluence.ErrNotFound) {
			return PushCommitPlan{}, fmt.Errorf("hard-delete page %s: %w", pageID, err)
		}
	} else {
		archiveAlreadyApplied := false
		archiveResult, err := remote.ArchivePages(ctx, []string{pageID})
		if err != nil {
			switch {
			case errors.Is(err, confluence.ErrNotFound), errors.Is(err, confluence.ErrArchived):
				archiveAlreadyApplied = true
				appendPushDiagnostic(
					diagnostics,
					relPath,
					"ARCHIVE_ALREADY_APPLIED",
					fmt.Sprintf("page %s was already archived or missing remotely", pageID),
				)
			default:
				return PushCommitPlan{}, fmt.Errorf("archive page %s: %w", pageID, err)
			}
		}

		if !archiveAlreadyApplied {
			taskID := strings.TrimSpace(archiveResult.TaskID)
			if taskID == "" {
				message := fmt.Sprintf("archive request for page %s did not return a long-task ID", pageID)
				appendPushDiagnostic(diagnostics, relPath, "ARCHIVE_TASK_FAILED", message)
				return PushCommitPlan{}, fmt.Errorf("archive page %s: missing long-task ID", pageID)
			}

			status, waitErr := remote.WaitForArchiveTask(ctx, taskID, confluence.ArchiveTaskWaitOptions{
				Timeout:      opts.ArchiveTimeout,
				PollInterval: opts.ArchivePollInterval,
			})
			if waitErr != nil {
				code := "ARCHIVE_TASK_FAILED"
				if errors.Is(waitErr, confluence.ErrArchiveTaskTimeout) {
					code = "ARCHIVE_TASK_TIMEOUT"
				}

				message := fmt.Sprintf("archive task %s did not complete for page %s: %v", taskID, pageID, waitErr)
				if strings.TrimSpace(status.RawStatus) != "" {
					message = fmt.Sprintf("archive task %s did not complete for page %s (status=%s): %v", taskID, pageID, status.RawStatus, waitErr)
				}
				appendPushDiagnostic(diagnostics, relPath, code, message)
				return PushCommitPlan{}, fmt.Errorf("wait for archive task %s for page %s: %w", taskID, pageID, waitErr)
			}
		}
	}

	stalePaths := collectPageAttachmentPaths(state.AttachmentIndex, pageID)
	for _, assetPath := range stalePaths {
		attachmentID := state.AttachmentIndex[assetPath]
		if strings.TrimSpace(attachmentID) != "" {
			if err := remote.DeleteAttachment(ctx, attachmentID, pageID); err != nil && !errors.Is(err, confluence.ErrNotFound) && !errors.Is(err, confluence.ErrArchived) {
				return PushCommitPlan{}, fmt.Errorf("delete attachment %s: %w", attachmentID, err)
			}
			appendPushDiagnostic(
				diagnostics,
				assetPath,
				"ATTACHMENT_DELETED",
				fmt.Sprintf("deleted attachment %s during page removal", strings.TrimSpace(attachmentID)),
			)
		}
		delete(state.AttachmentIndex, assetPath)
	}

	delete(state.PagePathIndex, relPath)

	stagedPaths := append([]string{relPath}, stalePaths...)
	stagedPaths = dedupeSortedPaths(stagedPaths)

	pageTitle := page.Title
	if strings.TrimSpace(pageTitle) == "" {
		pageTitle = strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
	}

	return PushCommitPlan{
		Path:        relPath,
		Deleted:     true,
		PageID:      pageID,
		PageTitle:   pageTitle,
		Version:     page.Version,
		SpaceKey:    opts.SpaceKey,
		URL:         page.WebURL,
		StagedPaths: stagedPaths,
	}, nil
}

func pushUpsertPage(
	ctx context.Context,
	remote PushRemote,
	space confluence.Space,
	opts PushOptions,
	state fs.SpaceState,
	policy PushConflictPolicy,
	pageIDByPath PageIndex,
	pageTitleByPath map[string]string,
	attachmentIDByPath map[string]string,
	folderIDByPath map[string]string,
	remotePageByID map[string]confluence.Page,
	relPath string,
	precreatedPages map[string]confluence.Page,
	diagnostics *[]PushDiagnostic,
) (PushCommitPlan, error) {
	absPath := filepath.Join(opts.SpaceDir, filepath.FromSlash(relPath))
	doc, err := fs.ReadMarkdownDocument(absPath)
	if err != nil {
		return PushCommitPlan{}, fmt.Errorf("read markdown %s: %w", relPath, err)
	}

	pageID := strings.TrimSpace(doc.Frontmatter.ID)
	normalizedRelPath := normalizeRelPath(relPath)
	precreatedPage, hasPrecreated := precreatedPages[normalizedRelPath]
	targetState := normalizePageLifecycleState(doc.Frontmatter.State)
	dirPath := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(relPath))))
	title := resolveLocalTitle(doc, relPath)
	pageTitleByPath[normalizedRelPath] = title

	if pageID == "" && !hasPrecreated {
		if conflictingPath, conflictingID := findTrackedTitleConflict(relPath, title, state.PagePathIndex, pageTitleByPath); conflictingPath != "" {
			return PushCommitPlan{}, fmt.Errorf(
				"new page %q duplicates tracked page %q (id=%s) with title %q; update the existing file instead of creating a duplicate",
				relPath,
				conflictingPath,
				conflictingID,
				title,
			)
		}
	}

	trackedPageID := strings.TrimSpace(state.PagePathIndex[relPath])
	if trackedPageID != "" {
		if pageID == "" {
			return PushCommitPlan{}, fmt.Errorf(
				"page %q has no id in frontmatter but was previously synced (id=%s). Restore the id field or use a different filename",
				relPath, trackedPageID,
			)
		}
		if pageID != trackedPageID {
			return PushCommitPlan{}, fmt.Errorf(
				"page %q changed immutable id from %s to %s",
				relPath, trackedPageID, pageID,
			)
		}
	}

	localVersion := doc.Frontmatter.Version
	fallbackParentID := strings.TrimSpace(doc.Frontmatter.ConfluenceParentPageID)
	var remotePage confluence.Page
	isExistingPage := pageID != ""

	rollback := newPushRollbackTracker(relPath, diagnostics)
	failWithRollback := func(opErr error) (PushCommitPlan, error) {
		slog.Warn("push_mutation_failed",
			"path", relPath,
			"error", opErr.Error(),
			"rollback_created_page", strings.TrimSpace(rollback.createdPageID) != "",
			"rollback_uploaded_assets", len(rollback.uploadedAssets),
			"rollback_content_snapshot", rollback.contentRestoreReq,
			"rollback_metadata_snapshot", rollback.metadataRestoreReq,
		)
		if opts.DryRun {
			slog.Info("push_rollback_skipped", "path", relPath, "reason", "dry_run")
			return PushCommitPlan{}, opErr
		}
		if rollbackErr := rollback.rollback(ctx, remote); rollbackErr != nil {
			return PushCommitPlan{}, errors.Join(opErr, fmt.Errorf("rollback for %s: %w", relPath, rollbackErr))
		}
		return PushCommitPlan{}, opErr
	}

	if pageID != "" {
		// Always fetch the latest version specifically for the page we're about to update
		// to avoid eventual consistency issues with space-wide listing.
		fetched, fetchErr := remote.GetPage(ctx, pageID)
		if fetchErr != nil {
			if errors.Is(fetchErr, confluence.ErrArchived) {
				return PushCommitPlan{}, fmt.Errorf(
					"page %q (id=%s) is archived remotely and cannot be updated; run 'conf pull' to reconcile or remove the id to publish as a new page",
					relPath,
					pageID,
				)
			}
			if errors.Is(fetchErr, confluence.ErrNotFound) {
				return PushCommitPlan{}, fmt.Errorf("remote page %s for %s was not found", pageID, relPath)
			}
			return PushCommitPlan{}, fmt.Errorf("fetch page %s: %w", pageID, fetchErr)
		}
		remotePage = fetched
		if normalizePageLifecycleState(remotePage.Status) == "archived" {
			return PushCommitPlan{}, fmt.Errorf(
				"page %q (id=%s) is archived remotely and cannot be updated; run 'conf pull' to reconcile or remove the id to publish as a new page",
				relPath,
				pageID,
			)
		}
		remotePageByID[pageID] = fetched
		rollback.trackContentSnapshot(pageID, snapshotPageContent(fetched))

		fallbackParentID = strings.TrimSpace(remotePage.ParentPageID)
		if normalizePageLifecycleState(remotePage.Status) == "current" && targetState == "draft" {
			return PushCommitPlan{}, fmt.Errorf(
				"page %q cannot be transitioned from current to draft",
				relPath,
			)
		}

		if remotePage.Version > localVersion {
			switch policy {

			case PushConflictPolicyForce:
				// Continue and overwrite on top of remote head.
			case PushConflictPolicyPullMerge, PushConflictPolicyCancel:
				return PushCommitPlan{}, &PushConflictError{
					Path:          relPath,
					PageID:        pageID,
					LocalVersion:  localVersion,
					RemoteVersion: remotePage.Version,
					Policy:        policy,
				}
			default:
				return PushCommitPlan{}, &PushConflictError{
					Path:          relPath,
					PageID:        pageID,
					LocalVersion:  localVersion,
					RemoteVersion: remotePage.Version,
					Policy:        PushConflictPolicyCancel,
				}
			}
		}
	}

	touchedAssets := make([]string, 0)
	assetOwnerPageID := strings.TrimSpace(pageID)
	if assetOwnerPageID == "" && hasPrecreated {
		assetOwnerPageID = strings.TrimSpace(precreatedPage.ID)
	}
	if assetOwnerPageID != "" {
		migratedBody, migratedPaths, migratedMoves, migrateErr := migrateReferencedAssetsToPageHierarchy(
			opts.SpaceDir,
			absPath,
			assetOwnerPageID,
			doc.Body,
			attachmentIDByPath,
			state.AttachmentIndex,
		)
		if migrateErr != nil {
			preflightErr := fmt.Errorf("normalize assets for %s: %w", relPath, migrateErr)
			if hasPrecreated {
				return failWithRollback(preflightErr)
			}
			return PushCommitPlan{}, preflightErr
		}
		doc.Body = migratedBody
		touchedAssets = append(touchedAssets, migratedPaths...)
		for _, move := range migratedMoves {
			appendPushDiagnostic(
				diagnostics,
				move.To,
				"ATTACHMENT_PATH_NORMALIZED",
				fmt.Sprintf("moved %s to %s and updated markdown reference", move.From, move.To),
			)
		}
	}

	// Phase 1: preflight planning and strict conversion validation.
	linkHook := NewReverseLinkHookWithGlobalIndex(opts.SpaceDir, pageIDByPath, opts.GlobalPageIndex, opts.Domain)
	strictAttachmentIndex, referencedAssetPaths, err := BuildStrictAttachmentIndex(opts.SpaceDir, absPath, doc.Body, attachmentIDByPath)
	if err != nil {
		preflightErr := fmt.Errorf("resolve assets for %s: %w", relPath, err)
		if hasPrecreated {
			return failWithRollback(preflightErr)
		}
		return PushCommitPlan{}, preflightErr
	}
	preparedBody, err := PrepareMarkdownForAttachmentConversion(opts.SpaceDir, absPath, doc.Body)
	if err != nil {
		preflightErr := fmt.Errorf("prepare attachment conversion for %s: %w", relPath, err)
		if hasPrecreated {
			return failWithRollback(preflightErr)
		}
		return PushCommitPlan{}, preflightErr
	}
	mediaHook := NewReverseMediaHook(opts.SpaceDir, strictAttachmentIndex)

	if _, err := converter.Reverse(ctx, []byte(preparedBody), converter.ReverseConfig{
		LinkHook:  linkHook,
		MediaHook: mediaHook,
		Strict:    true,
	}, absPath); err != nil {
		preflightErr := fmt.Errorf("strict conversion failed for %s: %w", relPath, err)
		if hasPrecreated {
			return failWithRollback(preflightErr)
		}
		return PushCommitPlan{}, preflightErr
	}

	// Phase 2: perform remote mutations after preflight succeeds.
	if !isExistingPage {
		if hasPrecreated {
			pageID = strings.TrimSpace(precreatedPage.ID)
			if pageID == "" {
				return failWithRollback(fmt.Errorf("pre-created placeholder page for %s returned empty page ID", relPath))
			}

			rollback.trackCreatedPage(pageID)
			localVersion = precreatedPage.Version
			remotePage = precreatedPage
			remotePageByID[pageID] = precreatedPage
			pageIDByPath[normalizedRelPath] = pageID

			doc.Frontmatter.ID = pageID
			doc.Frontmatter.Version = precreatedPage.Version
		} else {
			if dirPath != "" && dirPath != "." {
				folderIDByPath, err = ensureFolderHierarchy(ctx, remote, space.ID, dirPath, folderIDByPath, diagnostics)
				if err != nil {
					return failWithRollback(fmt.Errorf("ensure folder hierarchy for %s: %w", relPath, err))
				}
			}

			resolvedParentID := resolveParentIDFromHierarchy(relPath, "", fallbackParentID, pageIDByPath, folderIDByPath)
			created, createErr := remote.CreatePage(ctx, confluence.PageUpsertInput{
				SpaceID:      space.ID,
				ParentPageID: resolvedParentID,
				Title:        title,
				Status:       targetState,
				BodyADF:      []byte(`{"version":1,"type":"doc","content":[]}`),
			})
			if createErr != nil {
				return failWithRollback(fmt.Errorf("create placeholder page for %s: %w", relPath, createErr))
			}

			pageID = strings.TrimSpace(created.ID)
			if pageID == "" {
				return failWithRollback(fmt.Errorf("create placeholder page for %s returned empty page ID", relPath))
			}

			rollback.trackCreatedPage(pageID)
			localVersion = created.Version
			remotePage = created
			remotePageByID[pageID] = created
			pageIDByPath[normalizedRelPath] = pageID

			doc.Frontmatter.ID = pageID
			doc.Frontmatter.Version = created.Version
		}
	}

	referencedIDs := map[string]struct{}{}
	for _, assetRelPath := range referencedAssetPaths {
		if existingID := strings.TrimSpace(attachmentIDByPath[assetRelPath]); existingID != "" {
			referencedIDs[existingID] = struct{}{}
			touchedAssets = append(touchedAssets, assetRelPath)
			continue
		}

		assetAbsPath := filepath.Join(opts.SpaceDir, filepath.FromSlash(assetRelPath))
		raw, err := os.ReadFile(assetAbsPath) //nolint:gosec // asset path is resolved from validated in-scope markdown references
		if err != nil {
			return PushCommitPlan{}, fmt.Errorf("read asset %s: %w", assetRelPath, err)
		}

		uploaded, err := remote.UploadAttachment(ctx, confluence.AttachmentUploadInput{
			PageID:      pageID,
			Filename:    filepath.Base(assetAbsPath),
			ContentType: detectAssetContentType(assetAbsPath, raw),
			Data:        raw,
		})
		if err != nil {
			return failWithRollback(fmt.Errorf("upload asset %s: %w", assetRelPath, err))
		}

		uploadedID := strings.TrimSpace(uploaded.ID)
		if uploadedID == "" {
			return failWithRollback(fmt.Errorf("upload asset %s returned empty attachment ID", assetRelPath))
		}

		attachmentIDByPath[assetRelPath] = uploadedID
		state.AttachmentIndex[assetRelPath] = uploadedID
		rollback.trackUploadedAttachment(pageID, uploadedID, assetRelPath)
		referencedIDs[uploadedID] = struct{}{}
		touchedAssets = append(touchedAssets, assetRelPath)
	}

	stalePaths := collectPageAttachmentPaths(state.AttachmentIndex, pageID)
	for _, stalePath := range stalePaths {
		attachmentID := strings.TrimSpace(state.AttachmentIndex[stalePath])
		if attachmentID == "" {
			delete(state.AttachmentIndex, stalePath)
			delete(attachmentIDByPath, stalePath)
			continue
		}
		if _, keep := referencedIDs[attachmentID]; keep {
			continue
		}
		if opts.KeepOrphanAssets {
			appendPushDiagnostic(
				diagnostics,
				stalePath,
				"ATTACHMENT_PRESERVED",
				fmt.Sprintf("kept unreferenced attachment %s because --keep-orphan-assets is enabled", attachmentID),
			)
			continue
		}
		if err := remote.DeleteAttachment(ctx, attachmentID, pageID); err != nil && !errors.Is(err, confluence.ErrNotFound) && !errors.Is(err, confluence.ErrArchived) {
			return failWithRollback(fmt.Errorf("delete stale attachment %s: %w", attachmentID, err))
		}
		appendPushDiagnostic(
			diagnostics,
			stalePath,
			"ATTACHMENT_DELETED",
			fmt.Sprintf("deleted stale attachment %s", attachmentID),
		)
		delete(state.AttachmentIndex, stalePath)
		delete(attachmentIDByPath, stalePath)
		touchedAssets = append(touchedAssets, stalePath)
	}

	mediaHook = NewReverseMediaHook(opts.SpaceDir, attachmentIDByPath)
	reverse, err := converter.Reverse(ctx, []byte(preparedBody), converter.ReverseConfig{
		LinkHook:  linkHook,
		MediaHook: mediaHook,
		Strict:    true,
	}, absPath)
	if err != nil {
		return failWithRollback(fmt.Errorf("strict conversion failed for %s after attachment mapping: %w", relPath, err))
	}

	resolvedParentID := resolveParentIDFromHierarchy(relPath, pageID, fallbackParentID, pageIDByPath, folderIDByPath)
	nextVersion := localVersion + 1
	if policy == PushConflictPolicyForce && remotePage.Version >= nextVersion {
		nextVersion = remotePage.Version + 1
	}

	// Post-process ADF to ensure required attributes for Confluence v2 API
	finalADF, err := ensureADFMediaCollection(reverse.ADF, pageID)
	if err != nil {
		return failWithRollback(fmt.Errorf("post-process ADF for %s: %w", relPath, err))
	}

	updatedPage, err := remote.UpdatePage(ctx, pageID, confluence.PageUpsertInput{
		SpaceID:      space.ID,
		ParentPageID: resolvedParentID,
		Title:        title,
		Status:       targetState,
		Version:      nextVersion,
		BodyADF:      finalADF,
	})
	if err != nil {
		return failWithRollback(fmt.Errorf("update page %s: %w", pageID, err))
	}
	rollback.markContentRestoreRequired()

	if isExistingPage {
		snapshot, snapshotErr := capturePageMetadataSnapshot(ctx, remote, pageID)
		if snapshotErr != nil {
			return failWithRollback(fmt.Errorf("capture metadata snapshot for %s: %w", relPath, snapshotErr))
		}
		rollback.trackMetadataSnapshot(pageID, snapshot)
	}

	if err := syncPageMetadata(ctx, remote, pageID, doc); err != nil {
		return failWithRollback(fmt.Errorf("sync metadata for %s: %w", relPath, err))
	}
	rollback.clearMetadataSnapshot()

	doc.Frontmatter.Title = title
	doc.Frontmatter.Version = updatedPage.Version
	if !opts.DryRun {
		if err := fs.WriteMarkdownDocument(absPath, doc); err != nil {
			return failWithRollback(fmt.Errorf("write markdown %s: %w", relPath, err))
		}
	}

	state.PagePathIndex[relPath] = pageID
	rollback.clearContentSnapshot()
	stagedPaths := append([]string{relPath}, touchedAssets...)
	stagedPaths = dedupeSortedPaths(stagedPaths)

	return PushCommitPlan{
		Path:        relPath,
		Deleted:     false,
		PageID:      pageID,
		PageTitle:   updatedPage.Title,
		Version:     updatedPage.Version,
		SpaceKey:    opts.SpaceKey,
		URL:         updatedPage.WebURL,
		StagedPaths: stagedPaths,
	}, nil
}

func newPushRollbackTracker(relPath string, diagnostics *[]PushDiagnostic) *pushRollbackTracker {
	return &pushRollbackTracker{
		relPath:     relPath,
		diagnostics: diagnostics,
	}
}

func appendPushDiagnostic(diagnostics *[]PushDiagnostic, path, code, message string) {
	if diagnostics == nil {
		return
	}
	*diagnostics = append(*diagnostics, PushDiagnostic{
		Path:    path,
		Code:    code,
		Message: message,
	})
}

func (r *pushRollbackTracker) trackCreatedPage(pageID string) {
	pageID = strings.TrimSpace(pageID)
	if pageID == "" {
		return
	}
	r.createdPageID = pageID
}

func (r *pushRollbackTracker) trackUploadedAttachment(pageID, attachmentID, path string) {
	attachmentID = strings.TrimSpace(attachmentID)
	if attachmentID == "" {
		return
	}
	r.uploadedAssets = append(r.uploadedAssets, rollbackAttachment{
		PageID:       strings.TrimSpace(pageID),
		AttachmentID: attachmentID,
		Path:         normalizeRelPath(path),
	})
}

func (r *pushRollbackTracker) trackContentSnapshot(pageID string, snapshot pushContentSnapshot) {
	pageID = strings.TrimSpace(pageID)
	if pageID == "" {
		return
	}
	r.contentPageID = pageID
	r.contentSnapshot = &snapshot
	r.contentRestoreReq = false
}

func (r *pushRollbackTracker) markContentRestoreRequired() {
	if r.contentSnapshot == nil || strings.TrimSpace(r.contentPageID) == "" {
		return
	}
	r.contentRestoreReq = true
}

func (r *pushRollbackTracker) clearContentSnapshot() {
	r.contentRestoreReq = false
}

func (r *pushRollbackTracker) trackMetadataSnapshot(pageID string, snapshot pushMetadataSnapshot) {
	r.metadataPageID = strings.TrimSpace(pageID)
	r.metadataSnapshot = &snapshot
	r.metadataRestoreReq = true
}

func (r *pushRollbackTracker) clearMetadataSnapshot() {
	r.metadataRestoreReq = false
}

func (r *pushRollbackTracker) rollback(ctx context.Context, remote PushRemote) error {
	var rollbackErr error

	if r.contentRestoreReq && r.contentSnapshot != nil && strings.TrimSpace(r.contentPageID) != "" {
		slog.Info("push_rollback_step", "path", r.relPath, "step", "page_content", "page_id", r.contentPageID)
		if err := restorePageContentSnapshot(ctx, remote, r.contentPageID, *r.contentSnapshot); err != nil {
			slog.Warn("push_rollback_step_failed", "path", r.relPath, "step", "page_content", "page_id", r.contentPageID, "error", err.Error())
			appendPushDiagnostic(
				r.diagnostics,
				r.relPath,
				"ROLLBACK_PAGE_CONTENT_FAILED",
				fmt.Sprintf("failed to restore page content for %s: %v", r.contentPageID, err),
			)
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore page content for %s: %w", r.contentPageID, err))
		} else {
			slog.Info("push_rollback_step_succeeded", "path", r.relPath, "step", "page_content", "page_id", r.contentPageID)
			appendPushDiagnostic(
				r.diagnostics,
				r.relPath,
				"ROLLBACK_PAGE_CONTENT_RESTORED",
				fmt.Sprintf("restored page content for %s", r.contentPageID),
			)
		}
	}

	if r.metadataRestoreReq && r.metadataSnapshot != nil && strings.TrimSpace(r.metadataPageID) != "" {
		slog.Info("push_rollback_step", "path", r.relPath, "step", "metadata", "page_id", r.metadataPageID)
		if err := restorePageMetadataSnapshot(ctx, remote, r.metadataPageID, *r.metadataSnapshot); err != nil {
			slog.Warn("push_rollback_step_failed", "path", r.relPath, "step", "metadata", "page_id", r.metadataPageID, "error", err.Error())
			appendPushDiagnostic(
				r.diagnostics,
				r.relPath,
				"ROLLBACK_METADATA_FAILED",
				fmt.Sprintf("failed to restore metadata for page %s: %v", r.metadataPageID, err),
			)
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore metadata for page %s: %w", r.metadataPageID, err))
		} else {
			slog.Info("push_rollback_step_succeeded", "path", r.relPath, "step", "metadata", "page_id", r.metadataPageID)
			appendPushDiagnostic(
				r.diagnostics,
				r.relPath,
				"ROLLBACK_METADATA_RESTORED",
				fmt.Sprintf("restored metadata for page %s", r.metadataPageID),
			)
		}
	}

	for _, uploaded := range r.uploadedAssets {
		if strings.TrimSpace(uploaded.AttachmentID) == "" {
			continue
		}
		slog.Info("push_rollback_step", "path", r.relPath, "step", "attachment", "attachment_id", uploaded.AttachmentID, "page_id", uploaded.PageID)

		if err := remote.DeleteAttachment(ctx, uploaded.AttachmentID, uploaded.PageID); err != nil && !errors.Is(err, confluence.ErrNotFound) {
			slog.Warn("push_rollback_step_failed", "path", r.relPath, "step", "attachment", "attachment_id", uploaded.AttachmentID, "page_id", uploaded.PageID, "error", err.Error())
			path := uploaded.Path
			if path == "" {
				path = r.relPath
			}
			appendPushDiagnostic(
				r.diagnostics,
				path,
				"ROLLBACK_ATTACHMENT_FAILED",
				fmt.Sprintf("failed to delete uploaded attachment %s: %v", uploaded.AttachmentID, err),
			)
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("delete uploaded attachment %s: %w", uploaded.AttachmentID, err))
			continue
		}

		path := uploaded.Path
		if path == "" {
			path = r.relPath
		}
		slog.Info("push_rollback_step_succeeded", "path", r.relPath, "step", "attachment", "attachment_id", uploaded.AttachmentID, "page_id", uploaded.PageID)
		appendPushDiagnostic(
			r.diagnostics,
			path,
			"ROLLBACK_ATTACHMENT_DELETED",
			fmt.Sprintf("deleted uploaded attachment %s", uploaded.AttachmentID),
		)
	}

	if strings.TrimSpace(r.createdPageID) != "" {
		slog.Info("push_rollback_step", "path", r.relPath, "step", "created_page", "page_id", r.createdPageID)
		if err := remote.DeletePage(ctx, r.createdPageID, true); err != nil && !errors.Is(err, confluence.ErrNotFound) {
			slog.Warn("push_rollback_step_failed", "path", r.relPath, "step", "created_page", "page_id", r.createdPageID, "error", err.Error())
			appendPushDiagnostic(
				r.diagnostics,
				r.relPath,
				"ROLLBACK_PAGE_DELETE_FAILED",
				fmt.Sprintf("failed to delete created page %s: %v", r.createdPageID, err),
			)
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("delete created page %s: %w", r.createdPageID, err))
		} else {
			slog.Info("push_rollback_step_succeeded", "path", r.relPath, "step", "created_page", "page_id", r.createdPageID)
			appendPushDiagnostic(
				r.diagnostics,
				r.relPath,
				"ROLLBACK_PAGE_DELETED",
				fmt.Sprintf("deleted created page %s", r.createdPageID),
			)
		}
	}

	if rollbackErr != nil {
		slog.Warn("push_rollback_finished", "path", r.relPath, "status", "failed", "error", rollbackErr.Error())
	} else {
		slog.Info("push_rollback_finished", "path", r.relPath, "status", "succeeded")
	}

	return rollbackErr
}

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

func resolveParentIDFromHierarchy(relPath, pageID, fallbackParentID string, pageIDByPath PageIndex, folderIDByPath map[string]string) string {
	resolvedFallback := strings.TrimSpace(fallbackParentID)
	resolvedPageID := strings.TrimSpace(pageID)

	dirPath := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(relPath))))
	if dirPath == "" || dirPath == "." {
		return resolvedFallback
	}

	currentDir := dirPath
	for currentDir != "" && currentDir != "." {
		dirBase := filepath.Base(filepath.FromSlash(currentDir))
		if strings.TrimSpace(dirBase) != "" && dirBase != "." {
			if folderID, ok := folderIDByPath[currentDir]; ok && folderID != "" {
				return folderID
			}

			candidatePath := normalizeRelPath(filepath.ToSlash(filepath.Join(currentDir, dirBase+".md")))
			candidateID := strings.TrimSpace(pageIDByPath[candidatePath])
			if candidateID != "" && candidateID != resolvedPageID {
				return candidateID
			}
		}

		nextDir := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(currentDir))))
		if nextDir == "" || nextDir == "." || nextDir == currentDir {
			break
		}
		currentDir = nextDir
	}

	return resolvedFallback
}

func ensureFolderHierarchy(
	ctx context.Context,
	remote PushRemote,
	spaceID, dirPath string,
	folderIDByPath map[string]string,
	diagnostics *[]PushDiagnostic,
) (map[string]string, error) {
	if dirPath == "" || dirPath == "." {
		return folderIDByPath, nil
	}

	segments := strings.Split(filepath.ToSlash(dirPath), "/")
	var currentPath string

	for _, seg := range segments {
		if currentPath == "" {
			currentPath = seg
		} else {
			currentPath = filepath.ToSlash(filepath.Join(currentPath, seg))
		}

		if existingID, ok := folderIDByPath[currentPath]; ok && existingID != "" {
			continue
		}

		var parentFolderID string
		parentPath := filepath.ToSlash(filepath.Dir(currentPath))
		if parentPath != "." && parentPath != "" {
			parentFolderID = folderIDByPath[parentPath]
		}

		created, err := remote.CreateFolder(ctx, confluence.FolderCreateInput{
			SpaceID:  spaceID,
			ParentID: parentFolderID,
			Title:    seg,
		})
		if err != nil {
			return nil, fmt.Errorf("create folder %q: %w", currentPath, err)
		}

		folderIDByPath[currentPath] = created.ID

		if diagnostics != nil {
			*diagnostics = append(*diagnostics, PushDiagnostic{
				Path:    currentPath,
				Code:    "FOLDER_CREATED",
				Message: fmt.Sprintf("Auto-created Confluence folder %q (id=%s)", currentPath, created.ID),
			})
		}
	}

	return folderIDByPath, nil
}

func normalizePushState(state fs.SpaceState) fs.SpaceState {
	if state.PagePathIndex == nil {
		state.PagePathIndex = map[string]string{}
	}
	if state.AttachmentIndex == nil {
		state.AttachmentIndex = map[string]string{}
	}

	normalizedPageIndex := make(map[string]string, len(state.PagePathIndex))
	for path, id := range state.PagePathIndex {
		normalizedPageIndex[normalizeRelPath(path)] = id
	}
	state.PagePathIndex = normalizedPageIndex
	state.AttachmentIndex = cloneStringMap(state.AttachmentIndex)
	return state
}

func normalizeConflictPolicy(policy PushConflictPolicy) PushConflictPolicy {
	switch policy {
	case PushConflictPolicyPullMerge, PushConflictPolicyForce, PushConflictPolicyCancel:
		return policy
	default:
		return PushConflictPolicyCancel
	}
}

func normalizePushChanges(changes []PushFileChange) []PushFileChange {
	out := make([]PushFileChange, 0, len(changes))
	for _, change := range changes {
		path := normalizeRelPath(change.Path)
		if path == "" {
			continue
		}
		switch change.Type {
		case PushChangeAdd, PushChangeModify, PushChangeDelete:
			out = append(out, PushFileChange{Type: change.Type, Path: path})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		pi := out[i].Path
		pj := out[j].Path

		if pi == pj {
			return out[i].Type < out[j].Type
		}

		// Count segments to sort by depth (shallowest first)
		di := strings.Count(pi, "/")
		dj := strings.Count(pj, "/")

		if di != dj {
			return di < dj
		}

		// Within same depth, check if it's an "index" file (BaseName/BaseName.md)
		// Index files should be pushed before their siblings to establish hierarchy.
		bi := isIndexFile(pi)
		bj := isIndexFile(pj)

		if bi != bj {
			return bi // true (index) comes before false
		}

		return pi < pj
	})
	return out
}

func seedPendingPageIDsForPushChanges(spaceDir string, changes []PushFileChange, pageIDByPath PageIndex) error {
	for _, change := range changes {
		switch change.Type {
		case PushChangeAdd, PushChangeModify:
			// continue
		default:
			continue
		}

		relPath := normalizeRelPath(change.Path)
		if relPath == "" {
			continue
		}
		if strings.TrimSpace(pageIDByPath[relPath]) != "" {
			continue
		}

		absPath := filepath.Join(spaceDir, filepath.FromSlash(relPath))
		fm, err := fs.ReadFrontmatter(absPath)
		if err != nil {
			return fmt.Errorf("read frontmatter %s: %w", relPath, err)
		}
		if strings.TrimSpace(fm.ID) != "" {
			pageIDByPath[relPath] = strings.TrimSpace(fm.ID)
			continue
		}

		pageIDByPath[relPath] = pendingPageID(relPath)
	}
	return nil
}

func runPushUpsertPreflight(
	ctx context.Context,
	opts PushOptions,
	changes []PushFileChange,
	pageIDByPath PageIndex,
	attachmentIDByPath map[string]string,
) error {
	for _, change := range changes {
		switch change.Type {
		case PushChangeAdd, PushChangeModify:
			// continue
		default:
			continue
		}

		relPath := normalizeRelPath(change.Path)
		if relPath == "" {
			continue
		}

		absPath := filepath.Join(opts.SpaceDir, filepath.FromSlash(relPath))
		doc, err := fs.ReadMarkdownDocument(absPath)
		if err != nil {
			return fmt.Errorf("read markdown %s: %w", relPath, err)
		}

		linkHook := NewReverseLinkHookWithGlobalIndex(opts.SpaceDir, pageIDByPath, opts.GlobalPageIndex, opts.Domain)
		strictAttachmentIndex, _, err := BuildStrictAttachmentIndex(opts.SpaceDir, absPath, doc.Body, attachmentIDByPath)
		if err != nil {
			return fmt.Errorf("resolve assets for %s: %w", relPath, err)
		}
		preparedBody, err := PrepareMarkdownForAttachmentConversion(opts.SpaceDir, absPath, doc.Body)
		if err != nil {
			return fmt.Errorf("prepare attachment conversion for %s: %w", relPath, err)
		}
		mediaHook := NewReverseMediaHook(opts.SpaceDir, strictAttachmentIndex)

		if _, err := converter.Reverse(ctx, []byte(preparedBody), converter.ReverseConfig{
			LinkHook:  linkHook,
			MediaHook: mediaHook,
			Strict:    true,
		}, absPath); err != nil {
			return fmt.Errorf("strict conversion failed for %s: %w", relPath, err)
		}
	}

	return nil
}

func precreatePendingPushPages(
	ctx context.Context,
	remote PushRemote,
	space confluence.Space,
	opts PushOptions,
	state fs.SpaceState,
	changes []PushFileChange,
	pageIDByPath PageIndex,
	pageTitleByPath map[string]string,
	folderIDByPath map[string]string,
	diagnostics *[]PushDiagnostic,
) (map[string]confluence.Page, error) {
	precreated := map[string]confluence.Page{}

	for _, change := range changes {
		switch change.Type {
		case PushChangeAdd, PushChangeModify:
			// continue
		default:
			continue
		}

		relPath := normalizeRelPath(change.Path)
		if relPath == "" {
			continue
		}

		if !isPendingPageID(pageIDByPath[relPath]) {
			continue
		}

		absPath := filepath.Join(opts.SpaceDir, filepath.FromSlash(relPath))
		doc, err := fs.ReadMarkdownDocument(absPath)
		if err != nil {
			return nil, fmt.Errorf("read markdown %s: %w", relPath, err)
		}

		title := resolveLocalTitle(doc, relPath)
		pageTitleByPath[normalizeRelPath(relPath)] = title
		if conflictingPath, conflictingID := findTrackedTitleConflict(relPath, title, state.PagePathIndex, pageTitleByPath); conflictingPath != "" {
			return nil, fmt.Errorf(
				"new page %q duplicates tracked page %q (id=%s) with title %q; update the existing file instead of creating a duplicate",
				relPath,
				conflictingPath,
				conflictingID,
				title,
			)
		}

		dirPath := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(relPath))))
		if dirPath != "" && dirPath != "." {
			folderIDByPath, err = ensureFolderHierarchy(ctx, remote, space.ID, dirPath, folderIDByPath, diagnostics)
			if err != nil {
				return nil, fmt.Errorf("ensure folder hierarchy for %s: %w", relPath, err)
			}
		}

		fallbackParentID := strings.TrimSpace(doc.Frontmatter.ConfluenceParentPageID)
		resolvedParentID := resolveParentIDFromHierarchy(relPath, "", fallbackParentID, pageIDByPath, folderIDByPath)
		created, err := remote.CreatePage(ctx, confluence.PageUpsertInput{
			SpaceID:      space.ID,
			ParentPageID: resolvedParentID,
			Title:        title,
			Status:       normalizePageLifecycleState(doc.Frontmatter.State),
			BodyADF:      []byte(`{"version":1,"type":"doc","content":[]}`),
		})
		if err != nil {
			return nil, fmt.Errorf("create placeholder page for %s: %w", relPath, err)
		}

		createdID := strings.TrimSpace(created.ID)
		if createdID == "" {
			return nil, fmt.Errorf("create placeholder page for %s returned empty page ID", relPath)
		}

		pageIDByPath[relPath] = createdID
		precreated[relPath] = created
	}

	return precreated, nil
}

func cleanupPendingPrecreatedPages(
	ctx context.Context,
	remote PushRemote,
	precreatedPages map[string]confluence.Page,
	diagnostics *[]PushDiagnostic,
) {
	for _, relPath := range sortedStringKeys(precreatedPages) {
		pageID := strings.TrimSpace(precreatedPages[relPath].ID)
		if pageID == "" {
			continue
		}

		if err := remote.DeletePage(ctx, pageID, true); err != nil && !errors.Is(err, confluence.ErrNotFound) {
			appendPushDiagnostic(
				diagnostics,
				relPath,
				"ROLLBACK_PRECREATED_PAGE_FAILED",
				fmt.Sprintf("failed to delete pre-created placeholder page %s: %v", pageID, err),
			)
			continue
		}

		appendPushDiagnostic(
			diagnostics,
			relPath,
			"ROLLBACK_PRECREATED_PAGE_DELETED",
			fmt.Sprintf("deleted pre-created placeholder page %s", pageID),
		)
	}
}

func clonePageMap(in map[string]confluence.Page) map[string]confluence.Page {
	if in == nil {
		return map[string]confluence.Page{}
	}
	out := make(map[string]confluence.Page, len(in))
	for key, page := range in {
		out[key] = page
	}
	return out
}

func isIndexFile(path string) bool {
	base := filepath.Base(filepath.FromSlash(path))
	if !strings.HasSuffix(base, ".md") {
		return false
	}
	name := strings.TrimSuffix(base, ".md")
	dir := filepath.Base(filepath.FromSlash(filepath.Dir(filepath.FromSlash(path))))
	return name == dir
}

func BuildStrictAttachmentIndex(spaceDir, sourcePath, body string, attachmentIndex map[string]string) (map[string]string, []string, error) {
	referencedAssetPaths, err := CollectReferencedAssetPaths(spaceDir, sourcePath, body)
	if err != nil {
		return nil, nil, err
	}

	strictAttachmentIndex := cloneStringMap(attachmentIndex)
	seedPendingAttachmentIDs(strictAttachmentIndex, referencedAssetPaths)
	return strictAttachmentIndex, referencedAssetPaths, nil
}

func seedPendingAttachmentIDs(attachmentIndex map[string]string, assetPaths []string) {
	for _, assetPath := range assetPaths {
		if strings.TrimSpace(attachmentIndex[assetPath]) != "" {
			continue
		}
		attachmentIndex[assetPath] = pendingAttachmentID(assetPath)
	}
}

func pendingAttachmentID(assetPath string) string {
	normalized := strings.TrimSpace(strings.ToLower(filepath.ToSlash(assetPath)))
	normalized = strings.ReplaceAll(normalized, "/", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")
	if normalized == "" {
		normalized = "asset"
	}
	return "pending-attachment-" + normalized
}

func pendingPageID(path string) string {
	normalized := strings.TrimSpace(strings.ToLower(filepath.ToSlash(path)))
	normalized = strings.ReplaceAll(normalized, "/", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")
	if normalized == "" {
		normalized = "page"
	}
	return "pending-page-" + normalized
}

func isPendingPageID(pageID string) bool {
	return strings.HasPrefix(strings.TrimSpace(strings.ToLower(pageID)), "pending-page-")
}

type markdownReferenceKind string

const (
	markdownReferenceKindLink  markdownReferenceKind = "link"
	markdownReferenceKindImage markdownReferenceKind = "image"
)

type markdownDestinationOccurrence struct {
	kind             markdownReferenceKind
	tokenStart       int
	tokenEnd         int
	destinationStart int
	destinationEnd   int
	raw              string
}

type localAssetReference struct {
	Occurrence markdownDestinationOccurrence
	AbsPath    string
	RelPath    string
}

type markdownDestinationRewrite struct {
	Occurrence             markdownDestinationOccurrence
	ReplacementDestination string
	AddImagePrefix         bool
}

type assetPathMove struct {
	From string
	To   string
}

func CollectReferencedAssetPaths(spaceDir, sourcePath, body string) ([]string, error) {
	references, err := collectLocalAssetReferences(spaceDir, sourcePath, body)
	if err != nil {
		return nil, err
	}

	paths := map[string]struct{}{}
	for _, reference := range references {
		paths[reference.RelPath] = struct{}{}
	}

	return sortedStringKeys(paths), nil
}

// PrepareMarkdownForAttachmentConversion rewrites local file links ([]()) to
// media syntax (![]()) for strict reverse conversion.
func PrepareMarkdownForAttachmentConversion(spaceDir, sourcePath, body string) (string, error) {
	references, err := collectLocalAssetReferences(spaceDir, sourcePath, body)
	if err != nil {
		return "", err
	}

	rewrites := make([]markdownDestinationRewrite, 0)
	for _, reference := range references {
		if reference.Occurrence.kind != markdownReferenceKindLink {
			continue
		}
		rewrites = append(rewrites, markdownDestinationRewrite{
			Occurrence:     reference.Occurrence,
			AddImagePrefix: true,
		})
	}

	if len(rewrites) == 0 {
		return body, nil
	}

	return applyMarkdownDestinationRewrites(body, rewrites), nil
}

func collectLocalAssetReferences(spaceDir, sourcePath, body string) ([]localAssetReference, error) {
	occurrences := collectMarkdownDestinationOccurrences([]byte(body))
	if len(occurrences) == 0 {
		return nil, nil
	}

	references := make([]localAssetReference, 0, len(occurrences))
	for _, occurrence := range occurrences {
		destination := normalizeMarkdownDestination(occurrence.raw)
		if destination == "" || isExternalDestination(destination) {
			continue
		}

		destination = sanitizeDestinationForLookup(destination)
		if destination == "" {
			continue
		}
		destination = decodeMarkdownPath(destination)

		if occurrence.kind == markdownReferenceKindLink && isMarkdownFilePath(destination) {
			continue
		}

		assetAbsPath := filepath.Clean(filepath.Join(filepath.Dir(sourcePath), filepath.FromSlash(destination)))
		if !isSubpathOrSame(spaceDir, assetAbsPath) {
			return nil, outsideSpaceAssetError(spaceDir, sourcePath, destination)
		}

		info, statErr := os.Stat(assetAbsPath)
		if statErr != nil {
			return nil, fmt.Errorf("asset %s not found", destination)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("asset %s is a directory, expected a file", destination)
		}

		relPath, err := filepath.Rel(spaceDir, assetAbsPath)
		if err != nil {
			return nil, err
		}
		relPath = normalizeRelPath(relPath)
		if relPath == "" || relPath == "." || strings.HasPrefix(relPath, "../") {
			return nil, outsideSpaceAssetError(spaceDir, sourcePath, destination)
		}

		references = append(references, localAssetReference{
			Occurrence: occurrence,
			AbsPath:    assetAbsPath,
			RelPath:    relPath,
		})
	}

	return references, nil
}

func collectMarkdownDestinationOccurrences(content []byte) []markdownDestinationOccurrence {
	occurrences := make([]markdownDestinationOccurrence, 0)

	inFence := false
	var fenceChar byte
	fenceLen := 0
	inlineCodeDelimiterLen := 0
	lineStart := true

	for i := 0; i < len(content); {
		if lineStart {
			if toggled, newFence, newFenceChar, newFenceLen, next := maybeToggleFenceState(content, i, inFence, fenceChar, fenceLen); toggled {
				inFence = newFence
				fenceChar = newFenceChar
				fenceLen = newFenceLen
				i = next
				lineStart = true
				continue
			}
		}

		if inFence {
			if content[i] == '\n' {
				lineStart = true
			} else {
				lineStart = false
			}
			i++
			continue
		}

		if content[i] == '`' {
			run := countRepeatedByte(content, i, '`')
			switch inlineCodeDelimiterLen {
			case 0:
				inlineCodeDelimiterLen = run
			case run:
				inlineCodeDelimiterLen = 0
			}
			i += run
			lineStart = false
			continue
		}

		if inlineCodeDelimiterLen > 0 {
			if content[i] == '\n' {
				lineStart = true
			} else {
				lineStart = false
			}
			i++
			continue
		}

		if content[i] == '!' && i+1 < len(content) && content[i+1] == '[' {
			if occurrence, next, ok := parseInlineLinkOccurrence(content, i+1); ok {
				occurrences = append(occurrences, markdownDestinationOccurrence{
					kind:             markdownReferenceKindImage,
					tokenStart:       i + 1,
					tokenEnd:         next,
					destinationStart: occurrence.start,
					destinationEnd:   occurrence.end,
					raw:              occurrence.raw,
				})
				i = next
				lineStart = false
				continue
			}
		}

		if content[i] == '[' && (i == 0 || content[i-1] != '!') {
			if occurrence, next, ok := parseInlineLinkOccurrence(content, i); ok {
				occurrences = append(occurrences, markdownDestinationOccurrence{
					kind:             markdownReferenceKindLink,
					tokenStart:       i,
					tokenEnd:         next,
					destinationStart: occurrence.start,
					destinationEnd:   occurrence.end,
					raw:              occurrence.raw,
				})
				i = next
				lineStart = false
				continue
			}
		}

		if content[i] == '\n' {
			lineStart = true
		} else {
			lineStart = false
		}
		i++
	}

	return occurrences
}

func applyMarkdownDestinationRewrites(body string, rewrites []markdownDestinationRewrite) string {
	if len(rewrites) == 0 {
		return body
	}

	sort.Slice(rewrites, func(i, j int) bool {
		if rewrites[i].Occurrence.tokenStart == rewrites[j].Occurrence.tokenStart {
			return rewrites[i].Occurrence.destinationStart < rewrites[j].Occurrence.destinationStart
		}
		return rewrites[i].Occurrence.tokenStart < rewrites[j].Occurrence.tokenStart
	})

	content := []byte(body)
	var builder strings.Builder
	builder.Grow(len(content) + len(rewrites))

	last := 0
	for _, rewrite := range rewrites {
		tokenStart := rewrite.Occurrence.tokenStart
		tokenEnd := rewrite.Occurrence.tokenEnd
		destinationStart := rewrite.Occurrence.destinationStart
		destinationEnd := rewrite.Occurrence.destinationEnd

		if tokenStart < last || tokenEnd > len(content) || destinationStart < tokenStart || destinationEnd > tokenEnd || destinationStart > destinationEnd {
			continue
		}

		builder.Write(content[last:tokenStart])
		if rewrite.AddImagePrefix {
			builder.WriteByte('!')
		}
		builder.Write(content[tokenStart:destinationStart])

		replacementToken := string(content[destinationStart:destinationEnd])
		if strings.TrimSpace(rewrite.ReplacementDestination) != "" {
			replacementToken = formatRelinkDestinationToken(rewrite.Occurrence.raw, rewrite.ReplacementDestination)
		}
		builder.WriteString(replacementToken)
		builder.Write(content[destinationEnd:tokenEnd])

		last = tokenEnd
	}

	builder.Write(content[last:])
	return builder.String()
}

func migrateReferencedAssetsToPageHierarchy(
	spaceDir, sourcePath, pageID, body string,
	attachmentIDByPath map[string]string,
	stateAttachmentIndex map[string]string,
) (string, []string, []assetPathMove, error) {
	pageID = fs.SanitizePathSegment(strings.TrimSpace(pageID))
	if pageID == "" {
		return body, nil, nil, nil
	}

	references, err := collectLocalAssetReferences(spaceDir, sourcePath, body)
	if err != nil {
		return "", nil, nil, err
	}
	if len(references) == 0 {
		return body, nil, nil, nil
	}

	reservedTargets := map[string]string{}
	movesBySource := map[string]string{}
	pathMoves := map[string]string{}
	touchedPaths := map[string]struct{}{}
	rewrites := make([]markdownDestinationRewrite, 0, len(references))

	for _, reference := range references {
		targetAbsPath, targetRelPath, resolveErr := resolvePageAssetTargetPath(spaceDir, pageID, reference.AbsPath, reservedTargets)
		if resolveErr != nil {
			return "", nil, nil, resolveErr
		}

		if targetRelPath == reference.RelPath {
			continue
		}

		touchedPaths[reference.RelPath] = struct{}{}
		touchedPaths[targetRelPath] = struct{}{}
		movesBySource[reference.AbsPath] = targetAbsPath
		pathMoves[reference.RelPath] = targetRelPath

		relativeDestination, relErr := relativeEncodedDestination(sourcePath, targetAbsPath)
		if relErr != nil {
			return "", nil, nil, fmt.Errorf("resolve relative path from %s to %s: %w", sourcePath, targetAbsPath, relErr)
		}

		rewrites = append(rewrites, markdownDestinationRewrite{
			Occurrence:             reference.Occurrence,
			ReplacementDestination: relativeDestination,
		})
	}

	for sourceAbsPath, targetAbsPath := range movesBySource {
		sourceAbsPath = filepath.Clean(sourceAbsPath)
		targetAbsPath = filepath.Clean(targetAbsPath)
		if sourceAbsPath == targetAbsPath {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetAbsPath), 0o750); err != nil {
			return "", nil, nil, fmt.Errorf("prepare asset directory %s: %w", filepath.Dir(targetAbsPath), err)
		}

		if err := os.Rename(sourceAbsPath, targetAbsPath); err != nil {
			return "", nil, nil, fmt.Errorf("move asset %s to %s: %w", sourceAbsPath, targetAbsPath, err)
		}
	}

	for oldPath, newPath := range pathMoves {
		if err := relocateAttachmentIndexPath(attachmentIDByPath, oldPath, newPath); err != nil {
			return "", nil, nil, err
		}
		if err := relocateAttachmentIndexPath(stateAttachmentIndex, oldPath, newPath); err != nil {
			return "", nil, nil, err
		}
	}

	updatedBody := body
	if len(rewrites) > 0 {
		updatedBody = applyMarkdownDestinationRewrites(body, rewrites)
	}

	moves := make([]assetPathMove, 0, len(pathMoves))
	for _, oldPath := range sortedStringKeys(pathMoves) {
		moves = append(moves, assetPathMove{From: oldPath, To: pathMoves[oldPath]})
	}

	return updatedBody, sortedStringKeys(touchedPaths), moves, nil
}

func resolvePageAssetTargetPath(spaceDir, pageID, sourceAbsPath string, reservedTargets map[string]string) (string, string, error) {
	filename := strings.TrimSpace(filepath.Base(sourceAbsPath))
	if filename == "" || filename == "." {
		filename = "attachment"
	}

	targetDir := filepath.Join(spaceDir, "assets", pageID)
	ext := filepath.Ext(filename)
	stem := strings.TrimSuffix(filename, ext)
	if stem == "" {
		stem = "attachment"
	}

	for index := 1; ; index++ {
		candidateName := filename
		if index > 1 {
			candidateName = stem + "-" + strconv.Itoa(index) + ext
		}

		candidateAbsPath := filepath.Join(targetDir, candidateName)
		candidateRelPath, err := filepath.Rel(spaceDir, candidateAbsPath)
		if err != nil {
			return "", "", err
		}
		candidateRelPath = normalizeRelPath(candidateRelPath)
		if candidateRelPath == "" || strings.HasPrefix(candidateRelPath, "../") {
			return "", "", fmt.Errorf("invalid target asset path %s", candidateAbsPath)
		}

		candidateKey := strings.ToLower(filepath.Clean(candidateAbsPath))
		sourceKey := strings.ToLower(filepath.Clean(sourceAbsPath))
		if reservedSource, exists := reservedTargets[candidateKey]; exists && strings.ToLower(filepath.Clean(reservedSource)) != sourceKey {
			continue
		}

		if strings.EqualFold(filepath.Clean(candidateAbsPath), filepath.Clean(sourceAbsPath)) {
			reservedTargets[candidateKey] = sourceAbsPath
			return candidateAbsPath, candidateRelPath, nil
		}

		if _, statErr := os.Stat(candidateAbsPath); statErr == nil {
			continue
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", "", statErr
		}

		reservedTargets[candidateKey] = sourceAbsPath
		return candidateAbsPath, candidateRelPath, nil
	}
}

func relativeEncodedDestination(sourcePath, targetAbsPath string) (string, error) {
	relPath, err := filepath.Rel(filepath.Dir(sourcePath), targetAbsPath)
	if err != nil {
		return "", err
	}
	return encodeMarkdownPath(filepath.ToSlash(relPath)), nil
}

func relocateAttachmentIndexPath(index map[string]string, oldRelPath, newRelPath string) error {
	if index == nil {
		return nil
	}

	oldRelPath = normalizeRelPath(oldRelPath)
	newRelPath = normalizeRelPath(newRelPath)
	if oldRelPath == "" || newRelPath == "" || oldRelPath == newRelPath {
		return nil
	}

	oldID := strings.TrimSpace(index[oldRelPath])
	if oldID == "" {
		return nil
	}

	if existingID := strings.TrimSpace(index[newRelPath]); existingID != "" && existingID != oldID {
		return fmt.Errorf("cannot remap attachment path %s to %s: destination is already mapped to %s", oldRelPath, newRelPath, existingID)
	}

	index[newRelPath] = oldID
	delete(index, oldRelPath)
	return nil
}

func sanitizeDestinationForLookup(destination string) string {
	if idx := strings.Index(destination, "#"); idx >= 0 {
		destination = destination[:idx]
	}
	if idx := strings.Index(destination, "?"); idx >= 0 {
		destination = destination[:idx]
	}
	return strings.TrimSpace(destination)
}

func isMarkdownFilePath(destination string) bool {
	return strings.EqualFold(filepath.Ext(strings.TrimSpace(destination)), ".md")
}

func outsideSpaceAssetError(spaceDir, sourcePath, destination string) error {
	filename := strings.TrimSpace(filepath.Base(destination))
	if filename == "" || filename == "." {
		filename = "file"
	}

	targetAbsPath := filepath.Join(spaceDir, "assets", filename)
	suggestedDestination, err := relativeEncodedDestination(sourcePath, targetAbsPath)
	if err != nil {
		suggestedDestination = filepath.ToSlash(filepath.Join("assets", filename))
	}

	spaceAssetsPath := filepath.ToSlash(filepath.Join(filepath.Base(spaceDir), "assets")) + "/"
	return fmt.Errorf(
		"asset %q is outside the space directory. move it into %q and update the link to %q",
		filename,
		spaceAssetsPath,
		suggestedDestination,
	)
}

func normalizeMarkdownDestination(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if strings.HasPrefix(raw, "<") {
		if end := strings.Index(raw, ">"); end > 0 {
			raw = raw[1:end]
		}
	}

	raw = strings.TrimSpace(raw)
	if idx := strings.IndexAny(raw, " \t"); idx >= 0 {
		raw = raw[:idx]
	}

	raw = strings.Trim(raw, "\"'")
	return strings.TrimSpace(raw)
}

func isExternalDestination(destination string) bool {
	lower := strings.ToLower(strings.TrimSpace(destination))
	if lower == "" {
		return true
	}
	if strings.HasPrefix(lower, "#") {
		return true
	}
	for _, prefix := range []string{"http://", "https://", "mailto:", "tel:", "data:", "//"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func collectPageAttachmentPaths(index map[string]string, pageID string) []string {
	paths := make([]string, 0)
	for relPath := range index {
		if attachmentBelongsToPage(relPath, pageID) {
			paths = append(paths, normalizeRelPath(relPath))
		}
	}
	sort.Strings(paths)
	return paths
}

func dedupeSortedPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		path = normalizeRelPath(path)
		if path == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}
	sort.Strings(normalized)
	return normalized
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

func detectAssetContentType(filename string, raw []byte) string {
	extType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if strings.TrimSpace(extType) != "" {
		return extType
	}

	if len(raw) == 0 {
		return "application/octet-stream"
	}
	sniffLen := len(raw)
	if sniffLen > 512 {
		sniffLen = 512
	}
	return http.DetectContentType(raw[:sniffLen])
}

func normalizePageLifecycleState(state string) string {
	normalized := strings.TrimSpace(strings.ToLower(state))
	if normalized == "" {
		return "current"
	}
	return normalized
}

func listAllPushPages(ctx context.Context, remote PushRemote, opts confluence.PageListOptions) ([]confluence.Page, error) {
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

// ensureADFMediaCollection post-processes the ADF JSON to add required 'collection'
// attributes to 'media' nodes, which is often needed for Confluence v2 API storage conversion.
func ensureADFMediaCollection(adfJSON []byte, pageID string) ([]byte, error) {
	if len(adfJSON) == 0 {
		return adfJSON, nil
	}
	if strings.TrimSpace(pageID) == "" {
		return adfJSON, nil
	}

	var root any
	if err := json.Unmarshal(adfJSON, &root); err != nil {
		return nil, fmt.Errorf("unmarshal ADF: %w", err)
	}

	modified := walkAndFixMediaNodes(root, pageID)
	if !modified {
		return adfJSON, nil
	}

	out, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("marshal ADF: %w", err)
	}
	return out, nil
}

func walkAndFixMediaNodes(node any, pageID string) bool {
	modified := false
	switch n := node.(type) {
	case map[string]any:
		if nodeType, ok := n["type"].(string); ok && (nodeType == "media" || nodeType == "mediaInline") {
			if attrs, ok := n["attrs"].(map[string]any); ok {
				// If we have an id but no collection, add it
				_, hasID := attrs["id"]
				if !hasID {
					_, hasID = attrs["attachmentId"]
				}
				collection, hasCollection := attrs["collection"].(string)
				if hasID && (!hasCollection || collection == "") {
					attrs["collection"] = "contentId-" + pageID
					modified = true
				}
				if _, hasType := attrs["type"]; !hasType {
					attrs["type"] = "file"
					modified = true
				}
			}
		}
		for _, v := range n {
			if walkAndFixMediaNodes(v, pageID) {
				modified = true
			}
		}
	case []any:
		for _, item := range n {
			if walkAndFixMediaNodes(item, pageID) {
				modified = true
			}
		}
	}
	return modified
}

func syncPageMetadata(ctx context.Context, remote PushRemote, pageID string, doc fs.MarkdownDocument) error {
	// 1. Sync Content Status
	targetStatus := strings.TrimSpace(doc.Frontmatter.Status)
	currentStatus, err := remote.GetContentStatus(ctx, pageID)
	if err != nil {
		return fmt.Errorf("get content status: %w", err)
	}
	if targetStatus != currentStatus {
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

	// 2. Sync Labels
	remoteLabels, err := remote.GetLabels(ctx, pageID)
	if err != nil {
		return fmt.Errorf("get labels: %w", err)
	}

	remoteLabelSet := map[string]struct{}{}
	for _, l := range fs.NormalizeLabels(remoteLabels) {
		remoteLabelSet[l] = struct{}{}
	}

	localLabelSet := map[string]struct{}{}
	for _, l := range fs.NormalizeLabels(doc.Frontmatter.Labels) {
		localLabelSet[l] = struct{}{}
	}

	var toAdd []string
	for l := range localLabelSet {
		if _, ok := remoteLabelSet[l]; !ok {
			toAdd = append(toAdd, l)
		}
	}

	for l := range remoteLabelSet {
		if _, ok := localLabelSet[l]; !ok {
			if err := remote.RemoveLabel(ctx, pageID, l); err != nil {
				return fmt.Errorf("remove label %q: %w", l, err)
			}
		}
	}

	sort.Strings(toAdd)

	if len(toAdd) > 0 {
		if err := remote.AddLabels(ctx, pageID, toAdd); err != nil {
			return fmt.Errorf("add labels: %w", err)
		}
	}

	return nil
}
