package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

func printDestructivePushPreview(out io.Writer, changes []syncflow.PushFileChange, spaceDir string, state fs.SpaceState) {
	if !pushHasDeleteChange(changes) {
		return
	}

	_, _ = fmt.Fprintln(out, "Destructive operations in this push:")
	for _, change := range changes {
		if change.Type != syncflow.PushChangeDelete {
			continue
		}
		_, _ = fmt.Fprintf(out, "  %s\n", pushDeletePreview{
			path:   change.Path,
			pageID: readPushChangePageID(spaceDir, state, change.Path),
		}.destructiveSummaryLine())
	}
}

func printPushDiagnostics(out io.Writer, diagnostics []syncflow.PushDiagnostic) {
	if len(diagnostics) == 0 {
		return
	}

	_, _ = fmt.Fprintln(out, "\nDiagnostics:")
	for _, diag := range diagnostics {
		_, _ = fmt.Fprintf(out, "  [%s] %s: %s\n", diag.Code, diag.Path, diag.Message)
	}
}

func printPushWarningSummary(out io.Writer, warnings []string) {
	if len(warnings) == 0 {
		return
	}

	_, _ = fmt.Fprintln(out, "\nSummary of warnings:")
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(out, "  - %s\n", warning)
	}
}

func printPushSyncSummary(out io.Writer, commits []syncflow.PushCommitPlan, diagnostics []syncflow.PushDiagnostic) {
	if len(commits) == 0 && len(diagnostics) == 0 {
		return
	}

	deletedPages := 0
	for _, commit := range commits {
		if commit.Deleted {
			deletedPages++
		}
	}

	attachmentDeleted := 0
	attachmentUploaded := 0
	attachmentPreserved := 0
	attachmentSkipped := 0
	compatibilityNotes := make([]string, 0, 2)
	for _, diag := range diagnostics {
		switch diag.Code {
		case "ATTACHMENT_CREATED":
			attachmentUploaded++
		case "ATTACHMENT_DELETED":
			attachmentDeleted++
		case "ATTACHMENT_PRESERVED":
			attachmentPreserved++
			attachmentSkipped++
		default:
			if strings.HasPrefix(diag.Code, "ATTACHMENT_") && strings.Contains(diag.Code, "SKIPPED") {
				attachmentSkipped++
			}
			if diag.Code == "FOLDER_COMPATIBILITY_MODE" {
				compatibilityNotes = append(compatibilityNotes, strings.TrimSpace(diag.Message))
			}
		}
	}

	_, _ = fmt.Fprintln(out, "\nSync Summary:")
	_, _ = fmt.Fprintf(out, "  pages changed: %d (archived remotely: %d)\n", len(commits), deletedPages)
	if attachmentUploaded > 0 || attachmentDeleted > 0 || attachmentPreserved > 0 || attachmentSkipped > 0 {
		_, _ = fmt.Fprintf(out, "  attachments: uploaded %d, deleted %d, preserved %d, skipped %d\n", attachmentUploaded, attachmentDeleted, attachmentPreserved, attachmentSkipped)
	}
	if len(diagnostics) > 0 {
		_, _ = fmt.Fprintf(out, "  diagnostics: %d\n", len(diagnostics))
	}
	for _, note := range sortedUniqueStrings(compatibilityNotes) {
		_, _ = fmt.Fprintf(out, "  compatibility: %s\n", note)
	}
}

func formatPushConflictError(conflictErr *syncflow.PushConflictError) error {
	switch conflictErr.Policy {
	case syncflow.PushConflictPolicyPullMerge:
		return fmt.Errorf(
			"conflict for %s (remote v%d > local v%d): run 'conf pull' to merge remote changes into your local workspace before retrying push",
			conflictErr.Path,
			conflictErr.RemoteVersion,
			conflictErr.LocalVersion,
		)
	case syncflow.PushConflictPolicyForce:
		return conflictErr
	default:
		return fmt.Errorf(
			"conflict for %s (remote v%d > local v%d): rerun with --on-conflict=force to overwrite remote, or run 'conf pull' to merge",
			conflictErr.Path,
			conflictErr.RemoteVersion,
			conflictErr.LocalVersion,
		)
	}
}

func normalizedArchiveTaskTimeout() time.Duration {
	timeout := flagArchiveTaskTimeout
	if timeout <= 0 {
		return confluence.DefaultArchiveTaskTimeout
	}
	return timeout
}

func normalizedArchiveTaskPollInterval() time.Duration {
	interval := flagArchiveTaskPollInterval
	if interval <= 0 {
		interval = confluence.DefaultArchiveTaskPollInterval
	}
	timeout := normalizedArchiveTaskTimeout()
	if interval > timeout {
		return timeout
	}
	return interval
}
