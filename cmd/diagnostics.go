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
	diag = syncflow.NormalizePullDiagnostic(diag)
	level, qualifier := classifySyncDiagnostic(diag)
	message := strings.TrimSpace(diag.Message)
	if qualifier != "" {
		message = qualifier + "; action required: " + yesNo(diag.ActionRequired) + ": " + message
	}
	return fmt.Sprintf("%s: %s [%s] %s\n", level, diag.Path, diag.Code, message)
}

func classifySyncDiagnostic(diag syncflow.PullDiagnostic) (level string, qualifier string) {
	switch strings.TrimSpace(diag.Category) {
	case syncflow.DiagnosticCategoryPreservedExternalLink:
		return "note", "preserved external/cross-space link"
	case syncflow.DiagnosticCategoryPathChange:
		return "note", "planned markdown path changed"
	case syncflow.DiagnosticCategoryDegradedReference:
		return "warning", "unresolved but safely degraded reference"
	case syncflow.DiagnosticCategoryBlockingReference:
		return "error", "broken strict-path reference that blocks push"
	case syncflow.DiagnosticCategoryDegradedContent:
		return "warning", "degraded but pullable content"
	default:
		return "warning", ""
	}
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
