package cmd

import (
	"strings"
	"testing"

	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

func TestFormatSyncDiagnostic_Classification(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		diag      syncflow.PullDiagnostic
		wantStart string
		wantText  string
	}{
		{
			name:      "preserved cross-space link is a note",
			diag:      syncflow.PullDiagnostic{Path: "page.md", Code: "CROSS_SPACE_LINK_PRESERVED", Message: "preserved absolute cross-space link"},
			wantStart: "note: page.md [CROSS_SPACE_LINK_PRESERVED]",
			wantText:  "preserved external/cross-space link; action required: no",
		},
		{
			name:      "unresolved reference is actionable degraded output",
			diag:      syncflow.PullDiagnostic{Path: "page.md", Code: "unresolved_reference", Message: "page id 404 could not be resolved"},
			wantStart: "warning: page.md [unresolved_reference]",
			wantText:  "unresolved but safely degraded reference; action required: yes",
		},
		{
			name:      "folder fallback is marked degraded but pullable",
			diag:      syncflow.PullDiagnostic{Path: "folder-1", Code: "FOLDER_LOOKUP_UNAVAILABLE", Message: "falling back to page-only hierarchy"},
			wantStart: "warning: folder-1 [FOLDER_LOOKUP_UNAVAILABLE]",
			wantText:  "degraded but pullable content; action required: no",
		},
		{
			name:      "blocking strict-path reference is an error",
			diag:      syncflow.PullDiagnostic{Path: "page.md", Code: "STRICT_PATH_REFERENCE_BROKEN", Message: "relative link ../missing.md does not resolve"},
			wantStart: "error: page.md [STRICT_PATH_REFERENCE_BROKEN]",
			wantText:  "broken strict-path reference that blocks push; action required: yes",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := formatSyncDiagnostic(tc.diag)
			if !strings.HasPrefix(got, tc.wantStart) {
				t.Fatalf("unexpected prefix:\n%s", got)
			}
			if !strings.Contains(got, tc.wantText) {
				t.Fatalf("expected %q in:\n%s", tc.wantText, got)
			}
		})
	}
}
