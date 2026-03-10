package sync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

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
	opts.folderListTracker = newFolderListFallbackTracker()
	capabilities := newTenantCapabilityCache()
	diagnostics := make([]PushDiagnostic, 0)
	opts.folderMode = tenantFolderModeNative

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
	if opts.folderMode == tenantFolderModePageFallback {
		folderIDByPath = map[string]string{}
	}
	changes := normalizePushChanges(opts.Changes)
	commits := make([]PushCommitPlan, 0, len(changes))
	opts.contentStatusMode, err = capabilities.detectPushContentStatusMode(ctx, remote, opts.SpaceDir, pages, changes)
	if err != nil {
		return PushResult{State: state, Diagnostics: diagnostics}, err
	}
	diagnostics = append(diagnostics, capabilities.pushContentStatusDiagnostics()...)
	if err := seedPendingPageIDsForPushChanges(opts.SpaceDir, changes, pageIDByPath); err != nil {
		return PushResult{}, fmt.Errorf("seed pending page ids: %w", err)
	}
	if opts.contentStatusMode != tenantContentStatusModeDisabled {
		opts.contentStateCatalog, err = buildPushContentStateCatalog(ctx, remote, opts.SpaceKey, opts.SpaceDir, changes, pageIDByPath)
		if err != nil {
			return PushResult{State: state, Diagnostics: diagnostics}, err
		}
		if err := validatePushContentStatuses(opts.SpaceKey, opts.SpaceDir, changes, pageIDByPath, opts.contentStateCatalog); err != nil {
			return PushResult{State: state, Diagnostics: diagnostics}, err
		}
	}
	if err := runPushUpsertPreflight(ctx, opts, changes, pageIDByPath, attachmentIDByPath); err != nil {
		return PushResult{}, err
	}
	precreatedPages, err := precreatePendingPushPages(
		ctx,
		remote,
		space,
		&opts,
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
			commit, err := pushDeletePage(ctx, remote, opts, state, attachmentIDByPath, remotePageByID, relPath, &diagnostics)
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
				&opts,
				capabilities,
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

func republishUntilMediaResolvable(
	ctx context.Context,
	remote PushRemote,
	space confluence.Space,
	pageID string,
	updateInput confluence.PageUpsertInput,
	updatedPage confluence.Page,
	referencedAssetPaths []string,
	attachmentIDByPath map[string]string,
	uploadedAttachmentsByPath map[string]confluence.Attachment,
	dryRun bool,
) (confluence.Page, error) {
	if dryRun {
		return updatedPage, nil
	}

	for attempt := 0; attempt < 5; attempt++ {
		currentPage, err := remote.GetPage(ctx, pageID)
		if err != nil {
			return updatedPage, err
		}
		if !pageBodyHasUnknownMediaRefs(currentPage.BodyADF) {
			return currentPage, nil
		}

		if err := contextSleep(ctx, time.Duration(attempt+1)*time.Second); err != nil {
			return updatedPage, err
		}

		publishedAttachmentRefs, publishedMediaIDByPath, err := resolvePublishedAttachmentRefs(
			ctx,
			remote,
			pageID,
			referencedAssetPaths,
			attachmentIDByPath,
			uploadedAttachmentsByPath,
		)
		if err != nil {
			return updatedPage, err
		}

		retryADF, err := ensureADFMediaCollection(updateInput.BodyADF, pageID, publishedAttachmentRefs)
		if err != nil {
			return updatedPage, err
		}
		if len(publishedMediaIDByPath) == 0 {
			continue
		}

		retryInput := updateInput
		retryInput.SpaceID = space.ID
		retryInput.BodyADF = retryADF
		retryInput.Version = currentPage.Version + 1

		updatedPage, err = remote.UpdatePage(ctx, pageID, retryInput)
		if err != nil {
			return updatedPage, err
		}
		updateInput = retryInput
	}

	return updatedPage, nil
}

func pageBodyHasUnknownMediaRefs(adf []byte) bool {
	body := string(adf)
	return strings.Contains(body, "UNKNOWN_MEDIA_ID") || strings.Contains(body, "Invalid file id -")
}
