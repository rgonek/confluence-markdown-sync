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
			wantText:  "no action required",
		},
		{
			name:      "unresolved reference is called broken fallback",
			diag:      syncflow.PullDiagnostic{Path: "page.md", Code: "unresolved_reference", Message: "page id 404 could not be resolved"},
			wantStart: "warning: page.md [unresolved_reference]",
			wantText:  "broken reference preserved as fallback output",
		},
		{
			name:      "folder fallback is marked degraded but pullable",
			diag:      syncflow.PullDiagnostic{Path: "folder-1", Code: "FOLDER_LOOKUP_UNAVAILABLE", Message: "falling back to page-only hierarchy"},
			wantStart: "warning: folder-1 [FOLDER_LOOKUP_UNAVAILABLE]",
			wantText:  "degraded but pullable content",
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
