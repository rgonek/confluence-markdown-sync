package sync

import (
	"context"
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
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	writeDoc := func(relPath string, pageID string, body string) {
		doc := fs.MarkdownDocument{
			Frontmatter: fs.Frontmatter{
				Title: strings.TrimSuffix(filepath.Base(relPath), ".md"),
				ID:    pageID,

				Version:                1,
				ConfluenceLastModified: "2026-02-01T08:00:00Z",
			},
			Body: body,
		}
		if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, filepath.FromSlash(relPath)), doc); err != nil {
			t.Fatalf("write %s: %v", relPath, err)
		}
	}

	writeDoc("Root/Root.md", "1", "old root\n")
	writeDoc("Root/Child.md", "2", "old child\n")
	writeDoc("deleted.md", "999", "to be deleted\n")

	legacyAssetPath := filepath.Join(spaceDir, "assets", "999", "att-old-legacy.png")
	if err := os.MkdirAll(filepath.Dir(legacyAssetPath), 0o750); err != nil {
		t.Fatalf("mkdir legacy assets: %v", err)
	}
	if err := os.WriteFile(legacyAssetPath, []byte("legacy"), 0o600); err != nil {
		t.Fatalf("write legacy asset: %v", err)
	}

	state := fs.SpaceState{
		LastPullHighWatermark: "2026-02-01T09:00:00Z",
		PagePathIndex: map[string]string{
			"Root/Root.md":  "1",
			"Root/Child.md": "2",
			"deleted.md":    "999",
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
			"att-2": []byte("inline-bytes"),
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

	rootDoc, err := fs.ReadMarkdownDocument(filepath.Join(spaceDir, "Root/Root.md"))
	if err != nil {
		t.Fatalf("read Root/Root.md: %v", err)
	}
	if !strings.Contains(rootDoc.Body, "[Known](Child.md#section-a)") {
		t.Fatalf("expected rewritten known link in root body, got:\n%s", rootDoc.Body)
	}
	if !strings.Contains(rootDoc.Body, "[Missing](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=404)") {
		t.Fatalf("expected unresolved fallback link in root body, got:\n%s", rootDoc.Body)
	}
	if !strings.Contains(rootDoc.Body, "![Diagram](../assets/1/att-1-diagram.png)") {
		t.Fatalf("expected rewritten media link in root body, got:\n%s", rootDoc.Body)
	}
	if rootDoc.Frontmatter.Version != 5 {
		t.Fatalf("root version = %d, want 5", rootDoc.Frontmatter.Version)
	}

	assetPath := filepath.Join(spaceDir, "assets", "1", "att-1-diagram.png")
	assetRaw, err := os.ReadFile(assetPath) //nolint:gosec // test path is created in temp workspace
	if err != nil {
		t.Fatalf("read downloaded asset: %v", err)
	}
	if string(assetRaw) != "diagram-bytes" {
		t.Fatalf("downloaded asset bytes = %q, want %q", string(assetRaw), "diagram-bytes")
	}

	if _, err := os.Stat(filepath.Join(spaceDir, "deleted.md")); !os.IsNotExist(err) {
		t.Fatalf("deleted.md should be deleted, stat error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, "Root/Root.md")); err != nil {
		t.Fatalf("root markdown should exist at space root, stat error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, "Root/Child.md")); err != nil {
		t.Fatalf("child markdown should exist at space root, stat error=%v", err)
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
			if d.Category != "degraded_reference" {
				t.Fatalf("unresolved_reference category = %q, want degraded_reference", d.Category)
			}
			if !d.ActionRequired {
				t.Fatalf("unresolved_reference should require user action: %+v", d)
			}
			break
		}
	}
	if !foundUnresolved {
		t.Fatalf("expected unresolved_reference diagnostic, got %+v", result.Diagnostics)
	}

	if result.State.LastPullHighWatermark != "2026-02-01T11:00:00Z" {
		t.Fatalf("watermark = %q, want 2026-02-01T11:00:00Z", result.State.LastPullHighWatermark)
	}
	if result.State.SpaceKey != "ENG" {
		t.Fatalf("state space key = %q, want ENG", result.State.SpaceKey)
	}
	if got := result.State.PagePathIndex["Root/Root.md"]; got != "1" {
		t.Fatalf("state page_path_index[Root/Root.md] = %q, want 1", got)
	}
	if got := result.State.PagePathIndex["Root/Child.md"]; got != "2" {
		t.Fatalf("state page_path_index[Root/Child.md] = %q, want 2", got)
	}
	if _, exists := result.State.PagePathIndex["deleted.md"]; exists {
		t.Fatalf("state page_path_index should not include deleted.md")
	}
	if got := result.State.AttachmentIndex["assets/1/att-1-diagram.png"]; got != "att-1" {
		t.Fatalf("state attachment_index mismatch for att-1: %q", got)
	}
	if got := result.State.AttachmentIndex["assets/2/att-2-inline.png"]; got != "att-2" {
		t.Fatalf("state attachment_index mismatch for att-2: %q", got)
	}
	if _, exists := result.State.AttachmentIndex["assets/999/att-old-legacy.png"]; exists {
		t.Fatalf("state attachment_index should not include legacy asset")
	}
}

func TestPull_IncrementalCreateRetriesUntilRemotePageMaterializes(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	parentPath := filepath.Join(spaceDir, "Parent.md")
	if err := fs.WriteMarkdownDocument(parentPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Parent",
			ID:      "10",
			Version: 1,
		},
		Body: "old parent\n",
	}); err != nil {
		t.Fatalf("write Parent.md: %v", err)
	}

	watermark := "2026-03-09T09:00:00Z"
	modifiedAt := time.Date(2026, time.March, 9, 9, 30, 0, 0, time.UTC)
	emptyADF := map[string]any{"version": 1, "type": "doc", "content": []any{}}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "10", SpaceID: "space-1", Title: "Parent", Version: 1, LastModified: modifiedAt},
			{ID: "20", SpaceID: "space-1", Title: "Remote Child", ParentPageID: "10", Version: 1, LastModified: modifiedAt},
		},
		changes: []confluence.Change{
			{PageID: "20", SpaceKey: "ENG", Version: 1, LastModified: modifiedAt},
		},
		pagesByID: map[string]confluence.Page{
			"10": {ID: "10", SpaceID: "space-1", Title: "Parent", Version: 1, LastModified: modifiedAt, BodyADF: rawJSON(t, emptyADF)},
			"20": {ID: "20", SpaceID: "space-1", Title: "Remote Child", ParentPageID: "10", Version: 1, LastModified: modifiedAt, BodyADF: rawJSON(t, emptyADF)},
		},
	}
	childFetches := 0
	fake.getPageFunc = func(pageID string) (confluence.Page, error) {
		if pageID == "20" {
			childFetches++
			if childFetches == 1 {
				return confluence.Page{}, confluence.ErrNotFound
			}
		}
		page, ok := fake.pagesByID[pageID]
		if !ok {
			return confluence.Page{}, confluence.ErrNotFound
		}
		return page, nil
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		State: fs.SpaceState{
			LastPullHighWatermark: watermark,
			PagePathIndex: map[string]string{
				"Parent.md": "10",
			},
		},
		PullStartedAt: time.Date(2026, time.March, 9, 10, 0, 0, 0, time.UTC),
		OverlapWindow: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	if childFetches < 2 {
		t.Fatalf("expected child page fetch to retry, got %d attempt(s)", childFetches)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, "Parent", "Parent.md")); err != nil {
		t.Fatalf("expected moved parent markdown: %v", err)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, "Parent", "Remote-Child.md")); err != nil {
		t.Fatalf("expected new child markdown: %v", err)
	}
	if _, err := os.Stat(parentPath); !os.IsNotExist(err) {
		t.Fatalf("expected old Parent.md path to be removed, stat=%v", err)
	}
	if got := result.State.PagePathIndex["Parent/Remote-Child.md"]; got != "20" {
		t.Fatalf("state page_path_index[Parent/Remote-Child.md] = %q, want 20", got)
	}
}

func TestPull_IncrementalUpdateRetriesUntilExpectedVersionIsReadable(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	pagePath := filepath.Join(spaceDir, "Remote-Page.md")
	if err := fs.WriteMarkdownDocument(pagePath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Remote Page",
			ID:      "20",
			Version: 1,
		},
		Body: "old body\n",
	}); err != nil {
		t.Fatalf("write Remote-Page.md: %v", err)
	}

	changeTime := time.Date(2026, time.March, 9, 11, 30, 0, 0, time.UTC)
	stalePage := confluence.Page{
		ID:           "20",
		SpaceID:      "space-1",
		Title:        "Remote Page",
		Version:      1,
		LastModified: time.Date(2026, time.March, 9, 11, 0, 0, 0, time.UTC),
		BodyADF: rawJSON(t, map[string]any{
			"version": 1,
			"type":    "doc",
			"content": []any{
				map[string]any{
					"type": "paragraph",
					"content": []any{
						map[string]any{"type": "text", "text": "old body"},
					},
				},
			},
		}),
	}
	freshPage := confluence.Page{
		ID:           "20",
		SpaceID:      "space-1",
		Title:        "Remote Page",
		Version:      2,
		LastModified: changeTime,
		BodyADF: rawJSON(t, map[string]any{
			"version": 1,
			"type":    "doc",
			"content": []any{
				map[string]any{
					"type": "paragraph",
					"content": []any{
						map[string]any{"type": "text", "text": "fresh body"},
					},
				},
			},
		}),
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "20", SpaceID: "space-1", Title: "Remote Page", Version: 1, LastModified: stalePage.LastModified},
		},
		changes: []confluence.Change{
			{PageID: "20", SpaceKey: "ENG", Version: 2, LastModified: changeTime},
		},
		pagesByID: map[string]confluence.Page{
			"20": freshPage,
		},
	}
	updateFetches := 0
	fake.getPageFunc = func(pageID string) (confluence.Page, error) {
		if pageID != "20" {
			return confluence.Page{}, confluence.ErrNotFound
		}
		updateFetches++
		if updateFetches == 1 {
			return stalePage, nil
		}
		return freshPage, nil
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		State: fs.SpaceState{
			LastPullHighWatermark: "2026-03-09T11:00:00Z",
			PagePathIndex: map[string]string{
				"Remote-Page.md": "20",
			},
		},
		PullStartedAt: time.Date(2026, time.March, 9, 12, 0, 0, 0, time.UTC),
		OverlapWindow: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	if updateFetches < 2 {
		t.Fatalf("expected updated page fetch to retry, got %d attempt(s)", updateFetches)
	}

	doc, err := fs.ReadMarkdownDocument(pagePath)
	if err != nil {
		t.Fatalf("read Remote-Page.md: %v", err)
	}
	if doc.Frontmatter.Version != 2 {
		t.Fatalf("version = %d, want 2", doc.Frontmatter.Version)
	}
	if !strings.Contains(doc.Body, "fresh body") {
		t.Fatalf("expected updated body after incremental pull, got:\n%s", doc.Body)
	}
	if len(result.UpdatedMarkdown) != 1 || result.UpdatedMarkdown[0] != "Remote-Page.md" {
		t.Fatalf("unexpected updated markdown list: %+v", result.UpdatedMarkdown)
	}
}

func TestPull_TargetPageUsesFreshGetPageVersionWhenListingLags(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	pagePath := filepath.Join(spaceDir, "Conflict-E2E.md")
	if err := fs.WriteMarkdownDocument(pagePath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Conflict E2E",
			ID:      "20",
			Version: 2,
		},
		Body: "old body\n",
	}); err != nil {
		t.Fatalf("write Conflict-E2E.md: %v", err)
	}

	stalePage := confluence.Page{
		ID:           "20",
		SpaceID:      "space-1",
		Title:        "Conflict E2E",
		Version:      2,
		LastModified: time.Date(2026, time.March, 11, 9, 0, 0, 0, time.UTC),
		BodyADF: rawJSON(t, map[string]any{
			"version": 1,
			"type":    "doc",
			"content": []any{
				map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "stale body"}}},
			},
		}),
	}
	freshPage := confluence.Page{
		ID:           "20",
		SpaceID:      "space-1",
		Title:        "Conflict E2E",
		Version:      3,
		LastModified: time.Date(2026, time.March, 11, 9, 5, 0, 0, time.UTC),
		BodyADF: rawJSON(t, map[string]any{
			"version": 1,
			"type":    "doc",
			"content": []any{
				map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "fresh body"}}},
			},
		}),
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "20", SpaceID: "space-1", Title: "Conflict E2E", Version: stalePage.Version, LastModified: stalePage.LastModified},
		},
		pagesByID: map[string]confluence.Page{
			"20": freshPage,
		},
	}
	getPageCalls := 0
	fake.getPageFunc = func(pageID string) (confluence.Page, error) {
		if pageID != "20" {
			return confluence.Page{}, confluence.ErrNotFound
		}
		getPageCalls++
		if getPageCalls == 1 {
			// The target-page probe should see the fresh version before the later fetch loop runs.
			return freshPage, nil
		}
		return freshPage, nil
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey:     "ENG",
		SpaceDir:     spaceDir,
		TargetPageID: "20",
		State: fs.SpaceState{
			PagePathIndex: map[string]string{
				"Conflict-E2E.md": "20",
			},
		},
		PullStartedAt: time.Date(2026, time.March, 11, 9, 10, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	doc, err := fs.ReadMarkdownDocument(pagePath)
	if err != nil {
		t.Fatalf("read Conflict-E2E.md: %v", err)
	}
	if doc.Frontmatter.Version != 3 {
		t.Fatalf("version = %d, want 3", doc.Frontmatter.Version)
	}
	if !strings.Contains(doc.Body, "fresh body") {
		t.Fatalf("expected fresh body after target-page pull, got:\n%s", doc.Body)
	}
	if len(result.UpdatedMarkdown) != 1 || result.UpdatedMarkdown[0] != "Conflict-E2E.md" {
		t.Fatalf("unexpected updated markdown list: %+v", result.UpdatedMarkdown)
	}
}

func TestPull_PreservesAbsoluteCrossSpaceLinksWithoutUnresolvedWarnings(t *testing.T) {
	repo := t.TempDir()
	engDir := filepath.Join(repo, "Engineering (ENG)")
	tdDir := filepath.Join(repo, "Technical Docs (TD)")
	if err := os.MkdirAll(engDir, 0o750); err != nil {
		t.Fatalf("mkdir eng dir: %v", err)
	}
	if err := os.MkdirAll(tdDir, 0o750); err != nil {
		t.Fatalf("mkdir td dir: %v", err)
	}

	targetPath := filepath.Join(tdDir, "target.md")
	if err := fs.WriteMarkdownDocument(targetPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Target", ID: "200", Version: 1},
		Body:        "target\n",
	}); err != nil {
		t.Fatalf("write cross-space target: %v", err)
	}

	globalIndex, err := BuildGlobalPageIndex(repo)
	if err != nil {
		t.Fatalf("BuildGlobalPageIndex() error: %v", err)
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{{
			ID:           "1",
			SpaceID:      "space-1",
			Title:        "Root",
			Version:      2,
			LastModified: time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC),
		}},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      2,
				LastModified: time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC),
				BodyADF: rawJSON(t, map[string]any{
					"version": 1,
					"type":    "doc",
					"content": []any{
						map[string]any{
							"type": "paragraph",
							"content": []any{
								map[string]any{
									"type": "text",
									"text": "Cross Space",
									"marks": []any{
										map[string]any{
											"type": "link",
											"attrs": map[string]any{
												"href":   "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=200",
												"pageId": "200",
												"anchor": "section-a",
											},
										},
									},
								},
							},
						},
					},
				}),
			},
		},
		attachments: map[string][]byte{},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey:        "ENG",
		SpaceDir:        engDir,
		GlobalPageIndex: globalIndex,
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	rootDoc, err := fs.ReadMarkdownDocument(filepath.Join(engDir, "Root.md"))
	if err != nil {
		t.Fatalf("read Root.md: %v", err)
	}
	if !strings.Contains(rootDoc.Body, "[Cross Space](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=200#section-a)") {
		t.Fatalf("expected preserved absolute cross-space link, got:\n%s", rootDoc.Body)
	}

	foundPreserved := false
	for _, d := range result.Diagnostics {
		if d.Code == "unresolved_reference" {
			t.Fatalf("did not expect unresolved_reference diagnostic, got %+v", result.Diagnostics)
		}
		if d.Code == "CROSS_SPACE_LINK_PRESERVED" {
			foundPreserved = true
			if d.Category != "preserved_external_link" {
				t.Fatalf("CROSS_SPACE_LINK_PRESERVED category = %q, want preserved_external_link", d.Category)
			}
			if d.ActionRequired {
				t.Fatalf("CROSS_SPACE_LINK_PRESERVED should not require user action: %+v", d)
			}
		}
	}
	if !foundPreserved {
		t.Fatalf("expected CROSS_SPACE_LINK_PRESERVED diagnostic, got %+v", result.Diagnostics)
	}
}
