package cmd

import (
	"fmt"
	"io"
	"strings"

	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

func writeSyncDiagnostic(out io.Writer, diag syncflow.PullDiagnostic) error {
	_, err := io.WriteString(out, formatSyncDiagnostic(diag))
	return err
}

func formatSyncDiagnostic(diag syncflow.PullDiagnostic) string {
	level, qualifier := classifySyncDiagnostic(diag.Code)
	message := strings.TrimSpace(diag.Message)
	if qualifier != "" {
		message = qualifier + ": " + message
	}
	return fmt.Sprintf("%s: %s [%s] %s\n", level, diag.Path, diag.Code, message)
}

func classifySyncDiagnostic(code string) (level string, qualifier string) {
	switch strings.TrimSpace(code) {
	case "CROSS_SPACE_LINK_PRESERVED":
		return "note", "no action required"
	case "unresolved_reference":
		return "warning", "broken reference preserved as fallback output"
	case "FOLDER_LOOKUP_UNAVAILABLE",
		"CONTENT_STATUS_FETCH_FAILED",
		"LABELS_FETCH_FAILED",
		"UNKNOWN_MEDIA_ID_LOOKUP_FAILED",
		"UNKNOWN_MEDIA_ID_RESOLVED",
		"UNKNOWN_MEDIA_ID_UNRESOLVED",
		"ATTACHMENT_DOWNLOAD_SKIPPED":
		return "warning", "degraded but pullable content"
	default:
		return "warning", ""
	}
}
