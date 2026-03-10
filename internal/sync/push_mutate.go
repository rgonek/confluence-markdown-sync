package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func pushDeletePage(
	ctx context.Context,
	remote PushRemote,
	opts PushOptions,
	state fs.SpaceState,
	attachmentIDByPath map[string]string,
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
		deleteOpts := deleteOptionsForPageLifecycle(page.Status, true)
		if err := remote.DeletePage(ctx, pageID, deleteOpts); err != nil && !errors.Is(err, confluence.ErrNotFound) {
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
				verifiedArchived, verificationDetail := verifyArchivedAfterArchiveWaitFailure(ctx, remote, pageID)
				if verifiedArchived {
					appendPushDiagnostic(
						diagnostics,
						relPath,
						"ARCHIVE_CONFIRMED_AFTER_WAIT_FAILURE",
						fmt.Sprintf("archive task %s reported %v, but follow-up verification confirmed page %s is no longer current (%s)", taskID, waitErr, pageID, verificationDetail),
					)
				} else {
					code := "ARCHIVE_TASK_FAILED"
					if errors.Is(waitErr, confluence.ErrArchiveTaskTimeout) {
						code = "ARCHIVE_TASK_TIMEOUT"
						if verificationDetail != "" {
							code = "ARCHIVE_TASK_STILL_RUNNING"
						}
					}

					message := fmt.Sprintf("archive task %s did not complete for page %s: %v", taskID, pageID, waitErr)
					if strings.TrimSpace(status.RawStatus) != "" {
						message = fmt.Sprintf("archive task %s did not complete for page %s (status=%s): %v", taskID, pageID, status.RawStatus, waitErr)
					}
					if verificationDetail != "" {
						message = fmt.Sprintf("%s; verification=%s; consider rerunning with --archive-task-timeout=%s if Confluence is slow", message, verificationDetail, normalizedArchiveTimeoutForDiagnostic(opts.ArchiveTimeout))
					}
					appendPushDiagnostic(diagnostics, relPath, code, message)
					return PushCommitPlan{}, fmt.Errorf("wait for archive task %s for page %s: %w", taskID, pageID, waitErr)
				}
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
		if !opts.DryRun {
			if err := deleteLocalAssetFile(opts.SpaceDir, assetPath); err != nil {
				return PushCommitPlan{}, fmt.Errorf("delete local attachment %s: %w", assetPath, err)
			}
		}
		delete(state.AttachmentIndex, assetPath)
		delete(attachmentIDByPath, assetPath)
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

func verifyArchivedAfterArchiveWaitFailure(ctx context.Context, remote PushRemote, pageID string) (bool, string) {
	page, err := remote.GetPage(ctx, pageID)
	switch {
	case err == nil:
		status := normalizePageLifecycleState(page.Status)
		if status == "archived" {
			return true, "page status is archived"
		}
		if status == "current" || status == "draft" {
			return false, fmt.Sprintf("page still resolves as %s", status)
		}
		return false, fmt.Sprintf("page still resolves with status %q", page.Status)
	case errors.Is(err, confluence.ErrArchived):
		return true, "GetPage reports archived"
	case errors.Is(err, confluence.ErrNotFound):
		return true, "GetPage no longer finds the page"
	default:
		return false, fmt.Sprintf("verification read failed: %v", err)
	}
}

func normalizedArchiveTimeoutForDiagnostic(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return confluence.DefaultArchiveTaskTimeout
	}
	return timeout
}

func deleteLocalAssetFile(spaceDir, relPath string) error {
	relPath = normalizeRelPath(relPath)
	if relPath == "" {
		return nil
	}

	absPath := filepath.Join(spaceDir, filepath.FromSlash(relPath))
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	_ = removeEmptyParentDirs(filepath.Dir(absPath), filepath.Join(spaceDir, "assets"))
	return nil
}

func pushUpsertPage(
	ctx context.Context,
	remote PushRemote,
	space confluence.Space,
	opts *PushOptions,
	capabilities *tenantCapabilityCache,
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
	isExistingPage := pageID != ""
	normalizedRelPath := normalizeRelPath(relPath)
	precreatedPage, hasPrecreated := precreatedPages[normalizedRelPath]
	targetState := normalizePageLifecycleState(doc.Frontmatter.State)
	trackContentStatus := shouldSyncContentStatus(isExistingPage, doc)
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

	contentStatusMode := capabilities.currentPushContentStatusMode()
	rollback := newPushRollbackTracker(relPath, contentStatusMode, diagnostics)
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
				fmt.Sprintf("moved %s to %s and updated the markdown reference; this first-push asset relocation is expected and stable after pull", move.From, move.To),
			)
		}
	}

	linkHook := NewReverseLinkHookWithGlobalIndex(opts.SpaceDir, pageIDByPath, opts.GlobalPageIndex, opts.Domain)
	strictAttachmentIndex, referencedAssetPaths, err := BuildStrictAttachmentIndex(opts.SpaceDir, absPath, doc.Body, attachmentIDByPath)
	if err != nil {
		preflightErr := fmt.Errorf("resolve assets for %s: %w", relPath, err)
		if hasPrecreated {
			return failWithRollback(preflightErr)
		}
		return PushCommitPlan{}, preflightErr
	}
	preparedBody, err := PrepareMarkdownForAttachmentConversion(opts.SpaceDir, absPath, doc.Body, strictAttachmentIndex)
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

	if !isExistingPage {
		if hasPrecreated {
			pageID = strings.TrimSpace(precreatedPage.ID)
			if pageID == "" {
				return failWithRollback(fmt.Errorf("pre-created placeholder page for %s returned empty page ID", relPath))
			}

			rollback.trackCreatedPage(pageID, targetState)
			localVersion = precreatedPage.Version
			remotePage = precreatedPage
			remotePageByID[pageID] = precreatedPage
			pageIDByPath[normalizedRelPath] = pageID

			doc.Frontmatter.ID = pageID
			doc.Frontmatter.Version = precreatedPage.Version
		} else {
			if dirPath != "" && dirPath != "." {
			folderIDByPath, err = ensureFolderHierarchy(ctx, remote, space.ID, dirPath, relPath, opts, pageIDByPath, folderIDByPath, diagnostics)
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

			rollback.trackCreatedPage(pageID, targetState)
			localVersion = created.Version
			remotePage = created
			remotePageByID[pageID] = created
			pageIDByPath[normalizedRelPath] = pageID

			doc.Frontmatter.ID = pageID
			doc.Frontmatter.Version = created.Version
		}
	}

	referencedIDs := map[string]struct{}{}
	uploadedAttachmentsByPath := map[string]confluence.Attachment{}
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
		uploadedAttachmentsByPath[assetRelPath] = uploaded
		state.AttachmentIndex[assetRelPath] = uploadedID
		rollback.trackUploadedAttachment(pageID, uploadedID, assetRelPath)
		appendPushDiagnostic(
			diagnostics,
			assetRelPath,
			"ATTACHMENT_CREATED",
			fmt.Sprintf("uploaded attachment %s from %s", uploadedID, assetRelPath),
		)
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
		if !opts.DryRun {
			if err := deleteLocalAssetFile(opts.SpaceDir, stalePath); err != nil {
				return failWithRollback(fmt.Errorf("delete local stale attachment %s: %w", stalePath, err))
			}
		}
		delete(state.AttachmentIndex, stalePath)
		delete(attachmentIDByPath, stalePath)
		touchedAssets = append(touchedAssets, stalePath)
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
		return failWithRollback(fmt.Errorf("resolve published attachment metadata for %s: %w", relPath, err))
	}

	preparedBody, err = PrepareMarkdownForAttachmentConversion(opts.SpaceDir, absPath, doc.Body, publishedMediaIDByPath)
	if err != nil {
		return failWithRollback(fmt.Errorf("prepare attachment conversion for %s with resolved attachment IDs: %w", relPath, err))
	}

	mediaHook = NewReverseMediaHook(opts.SpaceDir, publishedMediaIDByPath)
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

	finalADF, err := ensureADFMediaCollection(reverse.ADF, pageID, publishedAttachmentRefs)
	if err != nil {
		return failWithRollback(fmt.Errorf("post-process ADF for %s: %w", relPath, err))
	}

	updateInput := confluence.PageUpsertInput{
		SpaceID:      space.ID,
		ParentPageID: resolvedParentID,
		Title:        title,
		Status:       targetState,
		Version:      nextVersion,
		BodyADF:      finalADF,
	}
	updatedPage, err := remote.UpdatePage(ctx, pageID, updateInput)
	if err != nil && isExistingPage && errors.Is(err, confluence.ErrNotFound) {
		refreshedPage, refreshErr := remote.GetPage(ctx, pageID)
		if refreshErr != nil {
			if errors.Is(refreshErr, confluence.ErrNotFound) || errors.Is(refreshErr, confluence.ErrArchived) {
				return failWithRollback(fmt.Errorf(
					"page %q (id=%s) no longer exists remotely during push; run 'conf pull' to reconcile or remove the id to publish as a new page",
					relPath,
					pageID,
				))
			}
			return failWithRollback(fmt.Errorf("refresh page %s after update failure: %w", pageID, refreshErr))
		}

		if normalizePageLifecycleState(refreshedPage.Status) == "archived" {
			return failWithRollback(fmt.Errorf(
				"page %q (id=%s) is archived remotely and cannot be updated; run 'conf pull' to reconcile or remove the id to publish as a new page",
				relPath,
				pageID,
			))
		}

		if refreshedPage.Version > localVersion {
			switch policy {
			case PushConflictPolicyForce:
			case PushConflictPolicyPullMerge, PushConflictPolicyCancel:
				return failWithRollback(&PushConflictError{
					Path:          relPath,
					PageID:        pageID,
					LocalVersion:  localVersion,
					RemoteVersion: refreshedPage.Version,
					Policy:        policy,
				})
			default:
				return failWithRollback(&PushConflictError{
					Path:          relPath,
					PageID:        pageID,
					LocalVersion:  localVersion,
					RemoteVersion: refreshedPage.Version,
					Policy:        PushConflictPolicyCancel,
				})
			}
		}

		retryParentID := strings.TrimSpace(refreshedPage.ParentPageID)
		if retryParentID == "" {
			retryParentID = strings.TrimSpace(fallbackParentID)
		}

		retryVersion := localVersion + 1
		if policy == PushConflictPolicyForce && refreshedPage.Version >= retryVersion {
			retryVersion = refreshedPage.Version + 1
		}

		retryInput := updateInput
		retryInput.ParentPageID = retryParentID
		retryInput.Version = retryVersion
		updatedPage, err = remote.UpdatePage(ctx, pageID, retryInput)
		if err != nil {
			return failWithRollback(fmt.Errorf("update page %s after retry: %w", pageID, err))
		}

		remotePageByID[pageID] = refreshedPage
		appendPushDiagnostic(
			diagnostics,
			relPath,
			"UPDATE_RETRIED_AFTER_NOT_FOUND",
			fmt.Sprintf(
				"retried update for page %s after not-found response (parent %q -> %q)",
				pageID,
				strings.TrimSpace(updateInput.ParentPageID),
				strings.TrimSpace(retryInput.ParentPageID),
			),
		)
	}
	if err != nil {
		return failWithRollback(fmt.Errorf("update page %s: %w", pageID, err))
	}
	if len(referencedAssetPaths) > 0 {
		reconciledPage, reconcileErr := republishUntilMediaResolvable(
			ctx,
			remote,
			space,
			pageID,
			updateInput,
			updatedPage,
			referencedAssetPaths,
			attachmentIDByPath,
			uploadedAttachmentsByPath,
			opts.DryRun,
		)
		if reconcileErr != nil {
			return failWithRollback(fmt.Errorf("verify published attachment media for %s: %w", relPath, reconcileErr))
		}
		updatedPage = reconciledPage
	}
	rollback.markContentRestoreRequired()

	if isExistingPage {
		snapshot, snapshotErr := capturePageMetadataSnapshot(ctx, remote, pageID, remotePage.Status, contentStatusMode, trackContentStatus)
		if snapshotErr != nil {
			return failWithRollback(fmt.Errorf("capture metadata snapshot for %s: %w", relPath, snapshotErr))
		}
		rollback.trackMetadataSnapshot(pageID, snapshot)
	}

	if err := syncPageMetadata(ctx, remote, pageID, doc, isExistingPage, capabilities, opts.contentStateCatalog, diagnostics); err != nil {
		return failWithRollback(fmt.Errorf("sync metadata for %s: %w", relPath, err))
	}
	if !opts.DryRun {
		refreshedPage, err := remote.GetPage(ctx, pageID)
		if err != nil {
			return failWithRollback(fmt.Errorf("refresh page %s after metadata sync: %w", pageID, err))
		}
		updatedPage = refreshedPage
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
	collapseFolderParentIfIndexPage(ctx, remote, relPath, pageID, folderIDByPath, remotePageByID, diagnostics)
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
