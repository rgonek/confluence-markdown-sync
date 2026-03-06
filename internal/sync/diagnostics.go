package sync

import "strings"

const (
	DiagnosticCategoryPreservedExternalLink = "preserved_external_link"
	DiagnosticCategoryDegradedReference     = "degraded_reference"
	DiagnosticCategoryBlockingReference     = "blocking_reference"
	DiagnosticCategoryDegradedContent       = "degraded_content"
)

func NormalizePullDiagnostic(diag PullDiagnostic) PullDiagnostic {
	category, actionRequired := classifyPullDiagnostic(diag.Code)
	if strings.TrimSpace(diag.Category) == "" {
		diag.Category = category
	}
	if actionRequired {
		diag.ActionRequired = true
	}
	return diag
}

func NormalizePullDiagnostics(diags []PullDiagnostic) []PullDiagnostic {
	if len(diags) == 0 {
		return diags
	}
	out := make([]PullDiagnostic, 0, len(diags))
	for _, diag := range diags {
		out = append(out, NormalizePullDiagnostic(diag))
	}
	return out
}

func classifyPullDiagnostic(code string) (category string, actionRequired bool) {
	switch strings.TrimSpace(code) {
	case "CROSS_SPACE_LINK_PRESERVED":
		return DiagnosticCategoryPreservedExternalLink, false
	case "unresolved_reference":
		return DiagnosticCategoryDegradedReference, true
	case "STRICT_PATH_REFERENCE_BROKEN":
		return DiagnosticCategoryBlockingReference, true
	case "FOLDER_LOOKUP_UNAVAILABLE",
		"CONTENT_STATUS_FETCH_FAILED",
		"LABELS_FETCH_FAILED",
		"UNKNOWN_MEDIA_ID_LOOKUP_FAILED",
		"UNKNOWN_MEDIA_ID_RESOLVED",
		"UNKNOWN_MEDIA_ID_UNRESOLVED",
		"ATTACHMENT_DOWNLOAD_SKIPPED",
		"MALFORMED_ADF":
		return DiagnosticCategoryDegradedContent, false
	default:
		return DiagnosticCategoryDegradedContent, false
	}
}
