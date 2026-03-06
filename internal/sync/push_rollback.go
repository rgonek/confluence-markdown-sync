package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

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

func (r *pushRollbackTracker) trackCreatedPage(pageID string, pageStatus string) {
	pageID = strings.TrimSpace(pageID)
	if pageID == "" {
		return
	}
	r.createdPageID = pageID
	r.createdPageStatus = normalizePageLifecycleState(pageStatus)
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
		restoreResult, err := restorePageMetadataSnapshot(ctx, remote, r.metadataPageID, *r.metadataSnapshot)
		if err != nil {
			slog.Warn("push_rollback_step_failed", "path", r.relPath, "step", "metadata", "page_id", r.metadataPageID, "error", err.Error())
			if strings.Contains(err.Error(), "content status") {
				appendPushDiagnostic(
					r.diagnostics,
					r.relPath,
					"ROLLBACK_CONTENT_STATUS_FAILED",
					fmt.Sprintf("failed to restore content status for page %s: %v", r.metadataPageID, err),
				)
			}
			appendPushDiagnostic(
				r.diagnostics,
				r.relPath,
				"ROLLBACK_METADATA_FAILED",
				fmt.Sprintf("failed to restore metadata for page %s: %v", r.metadataPageID, err),
			)
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore metadata for page %s: %w", r.metadataPageID, err))
		} else {
			slog.Info("push_rollback_step_succeeded", "path", r.relPath, "step", "metadata", "page_id", r.metadataPageID)
			if restoreResult.ContentStatusRestored {
				appendPushDiagnostic(
					r.diagnostics,
					r.relPath,
					"ROLLBACK_CONTENT_STATUS_RESTORED",
					fmt.Sprintf("restored content status for page %s", r.metadataPageID),
				)
			}
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
		deleteOpts := deleteOptionsForPageLifecycle(r.createdPageStatus, false)
		if err := remote.DeletePage(ctx, r.createdPageID, deleteOpts); err != nil && !errors.Is(err, confluence.ErrNotFound) {
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
