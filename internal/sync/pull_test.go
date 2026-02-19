package sync

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestPull_IncrementalRewriteDeleteAndWatermark(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	writeDoc := func(relPath string, pageID string, body string) {
		doc := fs.MarkdownDocument{
			Frontmatter: fs.Frontmatter{
				Title:                  strings.TrimSuffix(filepath.Base(relPath), ".md"),
				ConfluencePageID:       pageID,
				ConfluenceSpaceKey:     "ENG",
				ConfluenceVersion:      1,
				ConfluenceLastModified: "2026-02-01T08:00:00Z",
			},
			Body: body,
		}
		if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, filepath.FromSlash(relPath)), doc); err != nil {
			t.Fatalf("write %s: %v", relPath, err)
		}
	}

	writeDoc("root.md", "1", "old root\n")
	writeDoc("child.md", "2", "old child\n")
	writeDoc("deleted.md", "999", "to be deleted\n")

	legacyAssetPath := filepath.Join(spaceDir, "assets", "999", "att-old-legacy.png")
	if err := os.MkdirAll(filepath.Dir(legacyAssetPath), 0o755); err != nil {
		t.Fatalf("mkdir legacy assets: %v", err)
	}
	if err := os.WriteFile(legacyAssetPath, []byte("legacy"), 0o644); err != nil {
		t.Fatalf("write legacy asset: %v", err)
	}

	state := fs.SpaceState{
		LastPullHighWatermark: "2026-02-01T09:00:00Z",
		PagePathIndex: map[string]string{
			"root.md":    "1",
			"child.md":   "2",
			"deleted.md": "999",
		},
		AttachmentIndex: map[string]string{
			"assets/999/att-old-legacy.png": "att-old",
		},
	}

	pullStartedAt := time.Date(2026, time.February, 1, 10, 0, 0, 0, time.UTC)
	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      5,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
			},
			{
				ID:           "2",
				SpaceID:      "space-1",
				Title:        "Child",
				ParentPageID: "1",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 9, 15, 0, 0, time.UTC),
			},
		},
		changes: []confluence.Change{
			{PageID: "1", SpaceKey: "ENG", LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC)},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      5,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, sampleRootADF()),
			},
			"2": {
				ID:           "2",
				SpaceID:      "space-1",
				Title:        "Child",
				ParentPageID: "1",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 9, 15, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, sampleChildADF()),
			},
		},
		attachments: map[string][]byte{
			"att-1": []byte("diagram-bytes"),
		},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey:      "ENG",
		SpaceDir:      spaceDir,
		State:         state,
		PullStartedAt: pullStartedAt,
		OverlapWindow: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	expectedSince := time.Date(2026, time.February, 1, 8, 55, 0, 0, time.UTC)
	if !fake.lastChangeSince.Equal(expectedSince) {
		t.Fatalf("ListChanges since = %s, want %s", fake.lastChangeSince.Format(time.RFC3339), expectedSince.Format(time.RFC3339))
	}

	rootDoc, err := fs.ReadMarkdownDocument(filepath.Join(spaceDir, "root.md"))
	if err != nil {
		t.Fatalf("read root.md: %v", err)
	}
	if !strings.Contains(rootDoc.Body, "[Known](Root/Child.md#section-a)") {
		t.Fatalf("expected rewritten known link in root body, got:\n%s", rootDoc.Body)
	}
	if !strings.Contains(rootDoc.Body, "[Missing](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=404)") {
		t.Fatalf("expected unresolved fallback link in root body, got:\n%s", rootDoc.Body)
	}
	if !strings.Contains(rootDoc.Body, "![Diagram](assets/1/att-1-diagram.png)") {
		t.Fatalf("expected rewritten media link in root body, got:\n%s", rootDoc.Body)
	}
	if rootDoc.Frontmatter.ConfluenceVersion != 5 {
		t.Fatalf("root version = %d, want 5", rootDoc.Frontmatter.ConfluenceVersion)
	}

	assetPath := filepath.Join(spaceDir, "assets", "1", "att-1-diagram.png")
	assetRaw, err := os.ReadFile(assetPath)
	if err != nil {
		t.Fatalf("read downloaded asset: %v", err)
	}
	if string(assetRaw) != "diagram-bytes" {
		t.Fatalf("downloaded asset bytes = %q, want %q", string(assetRaw), "diagram-bytes")
	}

	if _, err := os.Stat(filepath.Join(spaceDir, "deleted.md")); !os.IsNotExist(err) {
		t.Fatalf("deleted.md should be deleted, stat error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, "child.md")); !os.IsNotExist(err) {
		t.Fatalf("legacy child.md should be deleted after hierarchy rewrite, stat error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, "Root", "Child.md")); err != nil {
		t.Fatalf("hierarchical child markdown should exist, stat error=%v", err)
	}
	if _, err := os.Stat(legacyAssetPath); !os.IsNotExist(err) {
		t.Fatalf("legacy asset should be deleted, stat error=%v", err)
	}

	if len(result.Diagnostics) == 0 {
		t.Fatalf("expected unresolved diagnostics, got none")
	}
	foundUnresolved := false
	for _, d := range result.Diagnostics {
		if d.Code == "unresolved_reference" {
			foundUnresolved = true
			break
		}
	}
	if !foundUnresolved {
		t.Fatalf("expected unresolved_reference diagnostic, got %+v", result.Diagnostics)
	}

	if result.State.LastPullHighWatermark != "2026-02-01T11:00:00Z" {
		t.Fatalf("watermark = %q, want 2026-02-01T11:00:00Z", result.State.LastPullHighWatermark)
	}
	if got := result.State.PagePathIndex["root.md"]; got != "1" {
		t.Fatalf("state page_path_index[root.md] = %q, want 1", got)
	}
	if got := result.State.PagePathIndex["Root/Child.md"]; got != "2" {
		t.Fatalf("state page_path_index[Root/Child.md] = %q, want 2", got)
	}
	if _, exists := result.State.PagePathIndex["child.md"]; exists {
		t.Fatalf("state page_path_index should not include legacy flat child.md path")
	}
	if _, exists := result.State.PagePathIndex["deleted.md"]; exists {
		t.Fatalf("state page_path_index should not include deleted.md")
	}
	if got := result.State.AttachmentIndex["assets/1/att-1-diagram.png"]; got != "att-1" {
		t.Fatalf("state attachment_index mismatch: %q", got)
	}
	if _, exists := result.State.AttachmentIndex["assets/999/att-old-legacy.png"]; exists {
		t.Fatalf("state attachment_index should not include legacy asset")
	}
}

func TestPlanPagePaths_MaintainsConfluenceHierarchy(t *testing.T) {
	spaceDir := t.TempDir()

	pages := []confluence.Page{
		{ID: "1", Title: "Root"},
		{ID: "2", Title: "Child", ParentPageID: "1"},
		{ID: "3", Title: "Grand Child", ParentPageID: "2"},
	}

	_, relByID := PlanPagePaths(spaceDir, nil, pages)

	if got := relByID["1"]; got != "Root.md" {
		t.Fatalf("root path = %q, want Root.md", got)
	}
	if got := relByID["2"]; got != "Root/Child.md" {
		t.Fatalf("child path = %q, want Root/Child.md", got)
	}
	if got := relByID["3"]; got != "Root/Child/Grand-Child.md" {
		t.Fatalf("grandchild path = %q, want Root/Child/Grand-Child.md", got)
	}
}

func TestPlanPagePaths_FallsBackToTopLevelWhenParentMissing(t *testing.T) {
	spaceDir := t.TempDir()

	pages := []confluence.Page{
		{ID: "2", Title: "Child", ParentPageID: "missing-parent"},
	}

	_, relByID := PlanPagePaths(spaceDir, nil, pages)

	if got := relByID["2"]; got != "Child.md" {
		t.Fatalf("fallback path = %q, want Child.md", got)
	}
}

type fakePullRemote struct {
	space           confluence.Space
	pages           []confluence.Page
	changes         []confluence.Change
	pagesByID       map[string]confluence.Page
	attachments     map[string][]byte
	lastChangeSince time.Time
}

func (f *fakePullRemote) GetSpace(_ context.Context, _ string) (confluence.Space, error) {
	return f.space, nil
}

func (f *fakePullRemote) ListPages(_ context.Context, _ confluence.PageListOptions) (confluence.PageListResult, error) {
	return confluence.PageListResult{Pages: f.pages}, nil
}

func (f *fakePullRemote) ListChanges(_ context.Context, opts confluence.ChangeListOptions) (confluence.ChangeListResult, error) {
	f.lastChangeSince = opts.Since
	return confluence.ChangeListResult{Changes: f.changes}, nil
}

func (f *fakePullRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
	page, ok := f.pagesByID[pageID]
	if !ok {
		return confluence.Page{}, confluence.ErrNotFound
	}
	return page, nil
}

func (f *fakePullRemote) DownloadAttachment(_ context.Context, attachmentID string) ([]byte, error) {
	raw, ok := f.attachments[attachmentID]
	if !ok {
		return nil, confluence.ErrNotFound
	}
	return raw, nil
}

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return raw
}

func sampleRootADF() map[string]any {
	return map[string]any{
		"version": 1,
		"type":    "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "Known",
						"marks": []any{
							map[string]any{
								"type": "link",
								"attrs": map[string]any{
									"href":     "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=2",
									"pageId":   "2",
									"spaceKey": "ENG",
									"anchor":   "section-a",
								},
							},
						},
					},
					map[string]any{
						"type": "text",
						"text": " ",
					},
					map[string]any{
						"type": "text",
						"text": "Missing",
						"marks": []any{
							map[string]any{
								"type": "link",
								"attrs": map[string]any{
									"href":     "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=404",
									"pageId":   "404",
									"spaceKey": "ENG",
								},
							},
						},
					},
				},
			},
			map[string]any{
				"type": "mediaSingle",
				"content": []any{
					map[string]any{
						"type": "media",
						"attrs": map[string]any{
							"type":         "image",
							"id":           "att-1",
							"attachmentId": "att-1",
							"pageId":       "1",
							"fileName":     "diagram.png",
							"alt":          "Diagram",
						},
					},
				},
			},
		},
	}
}

func sampleChildADF() map[string]any {
	return map[string]any{
		"version": 1,
		"type":    "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "Child body",
					},
				},
			},
		},
	}
}

func TestPull_SkipsMissingAssets(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	_ = os.MkdirAll(spaceDir, 0o755)

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Page 1"},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:      "1",
				Title:   "Page 1",
				BodyADF: rawJSON(t, sampleRootADF()),
			},
		},
		attachments: map[string][]byte{}, // Empty!
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey:          "ENG",
		SpaceDir:          spaceDir,
		SkipMissingAssets: true,
	})
	if err != nil {
		t.Fatalf("Pull() with skip=true failed: %v", err)
	}

	foundMissing := false
	for _, d := range result.Diagnostics {
		if d.Code == "ATTACHMENT_DOWNLOAD_SKIPPED" && strings.Contains(d.Message, "att-1") {
			foundMissing = true
			break
		}
	}
	if !foundMissing {
		t.Fatalf("expected ATTACHMENT_DOWNLOAD_SKIPPED diagnostic, got %+v", result.Diagnostics)
	}

	// Now try with skip=false (default)
	_, err = Pull(context.Background(), fake, PullOptions{
		SpaceKey:          "ENG",
		SpaceDir:          spaceDir,
		SkipMissingAssets: false,
	})
	if err == nil {
		t.Fatalf("Pull() with skip=false should have failed for missing attachment")
	}
	if !strings.Contains(err.Error(), "att-1") || !strings.Contains(err.Error(), "page 1") {
		t.Fatalf("error message should mention attachment and page, got: %v", err)
	}
}
