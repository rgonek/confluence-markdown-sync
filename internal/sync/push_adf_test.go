package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func testTenantCapabilityCache(mode tenantContentStatusMode) *tenantCapabilityCache {
	cache := newTenantCapabilityCache()
	cache.pushContentStatusMode.resolved = true
	cache.pushContentStatusMode.mode = mode
	return cache
}

func compatibilityNotImplementedError(pageID string) error {
	return &confluence.APIError{
		StatusCode: http.StatusNotImplemented,
		Method:     "GET",
		URL:        fmt.Sprintf("/wiki/rest/api/content/%s/state", pageID),
		Message:    "Not Implemented",
	}
}

func TestEnsureADFMediaCollection(t *testing.T) {
	testCases := []struct {
		name     string
		adf      string
		pageID   string
		expected string
	}{
		{
			name:     "adds collection and type to media node",
			adf:      `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"media","attrs":{"id":"att1"}}]}]}`,
			pageID:   "123",
			expected: `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"media","attrs":{"id":"att1","collection":"contentId-123","type":"file"}}]}]}`,
		},
		{
			name:     "adds collection and type to mediaInline node",
			adf:      `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"mediaInline","attrs":{"id":"att2"}}]}]}`,
			pageID:   "456",
			expected: `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"mediaInline","attrs":{"id":"att2","collection":"contentId-456","type":"file"}}]}]}`,
		},
		{
			name:     "does not overwrite existing collection or type",
			adf:      `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"media","attrs":{"id":"att3","collection":"other","type":"image"}}]}]}`,
			pageID:   "789",
			expected: `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"media","attrs":{"id":"att3","collection":"other","type":"image"}}]}]}`,
		},
		{
			name:     "handles nested nodes",
			adf:      `{"type":"doc","content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"media","attrs":{"id":"att4"}}]}]}]}]}`,
			pageID:   "101",
			expected: `{"type":"doc","content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"media","attrs":{"id":"att4","collection":"contentId-101","type":"file"}}]}]}]}]}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ensureADFMediaCollection([]byte(tc.adf), tc.pageID, nil)
			if err != nil {
				t.Fatalf("ensureADFMediaCollection() error: %v", err)
			}

			var gotObj, wantObj any
			if err := json.Unmarshal(got, &gotObj); err != nil {
				t.Fatalf("unmarshal got: %v", err)
			}
			if err := json.Unmarshal([]byte(tc.expected), &wantObj); err != nil {
				t.Fatalf("unmarshal expected: %v", err)
			}

			gotJSON, _ := json.Marshal(gotObj)
			wantJSON, _ := json.Marshal(wantObj)

			if string(gotJSON) != string(wantJSON) {
				t.Errorf("got  %s\nwant %s", string(gotJSON), string(wantJSON))
			}
		})
	}
}

func TestEnsureADFMediaCollection_EnrichesPublishedAttachmentMetadata(t *testing.T) {
	adf := `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"mediaInline","attrs":{"id":"att-1"}}]}]}`

	got, err := ensureADFMediaCollection([]byte(adf), "123", map[string]publishedAttachmentRef{
		"assets/123/manual.pdf": {
			AttachmentID: "att-1",
			MediaID:      "file-1",
			PageID:       "123",
			Filename:     "manual.pdf",
			MediaType:    "file",
		},
	})
	if err != nil {
		t.Fatalf("ensureADFMediaCollection() error: %v", err)
	}

	expected := `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"mediaInline","attrs":{"id":"file-1","attachmentId":"att-1","pageId":"123","fileName":"manual.pdf","collection":"contentId-123","type":"file"}}]}]}`

	var gotObj, wantObj any
	if err := json.Unmarshal(got, &gotObj); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	if err := json.Unmarshal([]byte(expected), &wantObj); err != nil {
		t.Fatalf("unmarshal expected: %v", err)
	}

	gotJSON, _ := json.Marshal(gotObj)
	wantJSON, _ := json.Marshal(wantObj)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("got  %s\nwant %s", string(gotJSON), string(wantJSON))
	}
}

func TestEnsureADFMediaCollection_DropsInvalidRenderIDWhenOnlyAttachmentIDIsKnown(t *testing.T) {
	adf := `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"mediaInline","attrs":{"id":"att-1"}}]}]}`

	got, err := ensureADFMediaCollection([]byte(adf), "123", map[string]publishedAttachmentRef{
		"assets/123/manual.pdf": {
			AttachmentID: "att-1",
			PageID:       "123",
			Filename:     "manual.pdf",
			MediaType:    "file",
		},
	})
	if err != nil {
		t.Fatalf("ensureADFMediaCollection() error: %v", err)
	}

	gotStr := string(got)
	if strings.Contains(gotStr, `"id":"att-1"`) {
		t.Fatalf("expected render id to be removed when only attachment id is known, got %s", gotStr)
	}
	if !strings.Contains(gotStr, `"attachmentId":"att-1"`) {
		t.Fatalf("expected attachmentId metadata to remain, got %s", gotStr)
	}
}

func TestEnsureADFMediaCollection_SplitsLiteralISODateTextNodes(t *testing.T) {
	adf := `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Release date: 2026-03-09"}]}]}`

	got, err := ensureADFMediaCollection([]byte(adf), "123", nil)
	if err != nil {
		t.Fatalf("ensureADFMediaCollection() error: %v", err)
	}

	gotStr := string(got)
	if strings.Contains(gotStr, `"type":"date"`) {
		t.Fatalf("did not expect date node in post-processed ADF, got %s", gotStr)
	}
	for _, expected := range []string{
		`"text":"Release date: "`,
		`"text":"2026"`,
		`"text":"‑"`,
		`"text":"03"`,
		`"text":"09"`,
	} {
		if !strings.Contains(gotStr, expected) {
			t.Fatalf("expected post-processed ADF to contain %s, got %s", expected, gotStr)
		}
	}
}

func TestSyncPageMetadata_EquivalentLabelSetsDoNotChurn(t *testing.T) {
	remote := newRollbackPushRemote()
	remote.labelsByPage["1"] = []string{"ops", "team"}

	doc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Labels: []string{" team ", "OPS", "team"},
		},
	}

	if err := syncPageMetadata(context.Background(), remote, "1", doc, true, testTenantCapabilityCache(tenantContentStatusModeEnabled), pushContentStateCatalog{}, nil); err != nil {
		t.Fatalf("syncPageMetadata() error: %v", err)
	}

	if len(remote.addLabelsCalls) != 0 {
		t.Fatalf("add labels calls = %d, want 0", len(remote.addLabelsCalls))
	}
	if len(remote.removeLabelCalls) != 0 {
		t.Fatalf("remove label calls = %d, want 0", len(remote.removeLabelCalls))
	}
}

func TestSyncPageMetadata_SetsContentStatusOnlyWhenPresent(t *testing.T) {
	remote := newRollbackPushRemote()

	doc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			State:  "draft",
			Status: "Ready to review",
		},
	}

	if err := syncPageMetadata(context.Background(), remote, "1", doc, true, testTenantCapabilityCache(tenantContentStatusModeEnabled), pushContentStateCatalog{global: map[string]confluence.ContentState{"ready to review": {ID: 80, Name: "Ready to review", Color: "FFAB00"}}}, nil); err != nil {
		t.Fatalf("syncPageMetadata() error: %v", err)
	}

	if len(remote.setContentStatusArgs) != 1 {
		t.Fatalf("set content status args = %d, want 1", len(remote.setContentStatusArgs))
	}
	if got := remote.setContentStatusArgs[0]; got.PageStatus != "draft" || got.StatusName != "Ready to review" {
		t.Fatalf("unexpected content status call: %+v", got)
	}
	if len(remote.deleteContentStatusArgs) != 0 {
		t.Fatalf("delete content status args = %d, want 0", len(remote.deleteContentStatusArgs))
	}
}

func TestSyncPageMetadata_ClearsContentStatusWhenExistingPageStatusRemoved(t *testing.T) {
	remote := newRollbackPushRemote()
	remote.contentStatuses["1"] = "Ready"

	doc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			State: "current",
		},
	}

	if err := syncPageMetadata(context.Background(), remote, "1", doc, true, testTenantCapabilityCache(tenantContentStatusModeEnabled), pushContentStateCatalog{}, nil); err != nil {
		t.Fatalf("syncPageMetadata() error: %v", err)
	}

	if len(remote.getContentStatusCalls) != 1 {
		t.Fatalf("get content status calls = %d, want 1", len(remote.getContentStatusCalls))
	}
	if len(remote.setContentStatusArgs) != 0 {
		t.Fatalf("set content status args = %d, want 0", len(remote.setContentStatusArgs))
	}
	if len(remote.deleteContentStatusArgs) != 1 {
		t.Fatalf("delete content status args = %d, want 1", len(remote.deleteContentStatusArgs))
	}
	if got := remote.deleteContentStatusArgs[0]; got.PageStatus != "current" {
		t.Fatalf("unexpected delete content status call: %+v", got)
	}
	if got := remote.contentStatuses["1"]; got != "" {
		t.Fatalf("content status after clear = %q, want empty", got)
	}
}

func TestSyncPageMetadata_SkipsContentStatusForNewPageWhenStatusMissing(t *testing.T) {
	remote := newRollbackPushRemote()

	doc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			State: "current",
		},
	}

	if err := syncPageMetadata(context.Background(), remote, "", doc, false, testTenantCapabilityCache(tenantContentStatusModeEnabled), pushContentStateCatalog{}, nil); err != nil {
		t.Fatalf("syncPageMetadata() error: %v", err)
	}

	if len(remote.getContentStatusCalls) != 0 {
		t.Fatalf("get content status calls = %d, want 0", len(remote.getContentStatusCalls))
	}
	if len(remote.setContentStatusArgs) != 0 {
		t.Fatalf("set content status args = %d, want 0", len(remote.setContentStatusArgs))
	}
	if len(remote.deleteContentStatusArgs) != 0 {
		t.Fatalf("delete content status args = %d, want 0", len(remote.deleteContentStatusArgs))
	}
}

func TestSyncPageMetadata_DisablesContentStatusModeOnCompatibilityError(t *testing.T) {
	remote := newRollbackPushRemote()
	remote.getContentStatusErr = compatibilityNotImplementedError("new-page-1")

	doc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			State:  "current",
			Status: "Ready to review",
		},
	}
	cache := testTenantCapabilityCache(tenantContentStatusModeEnabled)
	cache.pushContentStatusMode.resolved = false
	var diagnostics []PushDiagnostic

	if err := syncPageMetadata(context.Background(), remote, "new-page-1", doc, false, cache, pushContentStateCatalog{}, &diagnostics); err != nil {
		t.Fatalf("syncPageMetadata() error: %v", err)
	}

	if got := cache.currentPushContentStatusMode(); got != tenantContentStatusModeDisabled {
		t.Fatalf("content status mode = %q, want disabled", got)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != "CONTENT_STATUS_COMPATIBILITY_MODE" {
		t.Fatalf("diagnostics = %+v, want compatibility mode diagnostic", diagnostics)
	}
	if len(remote.setContentStatusArgs) != 0 {
		t.Fatalf("set content status args = %d, want 0", len(remote.setContentStatusArgs))
	}
	if len(remote.deleteContentStatusArgs) != 0 {
		t.Fatalf("delete content status args = %d, want 0", len(remote.deleteContentStatusArgs))
	}
}

func TestSyncPageMetadata_UsesNameOnlyFallbackWhenCatalogUnavailable(t *testing.T) {
	remote := newRollbackPushRemote()

	doc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			State:  "current",
			Status: "Unlisted Status",
		},
	}

	if err := syncPageMetadata(context.Background(), remote, "new-page-1", doc, false, testTenantCapabilityCache(tenantContentStatusModeEnabled), pushContentStateCatalog{}, nil); err != nil {
		t.Fatalf("syncPageMetadata() error: %v", err)
	}

	if len(remote.setContentStatusArgs) != 1 {
		t.Fatalf("set content status args = %d, want 1", len(remote.setContentStatusArgs))
	}
	if got := remote.setContentStatusArgs[0]; got.StatusName != "Unlisted Status" || got.PageStatus != "current" {
		t.Fatalf("unexpected content status call: %+v", got)
	}
}
