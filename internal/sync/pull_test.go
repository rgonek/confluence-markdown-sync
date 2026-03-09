package sync

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestPull_ReportsHierarchyPathMoveDiagnostics(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(filepath.Join(spaceDir, "Policies"), 0o750); err != nil {
		t.Fatalf("mkdir policies dir: %v", err)
	}

	writeDoc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Child",
			ID:      "2",
			Version: 1,
		},
		Body: "local child\n",
	}
	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "Policies", "Child.md"), writeDoc); err != nil {
		t.Fatalf("write old child doc: %v", err)
	}

	modifiedAt := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	emptyADF := map[string]any{"version": 1, "type": "doc", "content": []any{}}
	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-2", ParentType: "folder", Version: 2, LastModified: modifiedAt},
		},
		folderByID: map[string]confluence.Folder{
			"folder-2": {ID: "folder-2", Title: "Archive"},
		},
		pagesByID: map[string]confluence.Page{
			"2": {ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-2", ParentType: "folder", Version: 2, LastModified: modifiedAt, BodyADF: rawJSON(t, emptyADF)},
		},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		State: fs.SpaceState{
			PagePathIndex: map[string]string{
				"Policies/Child.md": "2",
			},
		},
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(spaceDir, "Archive", "Child.md")); err != nil {
		t.Fatalf("expected moved markdown at Archive/Child.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, "Policies", "Child.md")); !os.IsNotExist(err) {
		t.Fatalf("old markdown path should be deleted, stat error=%v", err)
	}

	movedDiag := findPullDiagnostic(result.Diagnostics, "PAGE_PATH_MOVED")
	if movedDiag == nil {
		t.Fatalf("expected PAGE_PATH_MOVED diagnostic, got %+v", result.Diagnostics)
	}
	if movedDiag.Path != "Policies/Child.md" {
		t.Fatalf("moved diagnostic path = %q, want Policies/Child.md", movedDiag.Path)
	}
	if !strings.Contains(movedDiag.Message, "Policies/Child.md") || !strings.Contains(movedDiag.Message, "Archive/Child.md") {
		t.Fatalf("move diagnostic message = %q, want old and new paths", movedDiag.Message)
	}
}

func TestPull_ReportsSanitizedPathMoveDiagnostics(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(filepath.Join(spaceDir, "Ops"), 0o750); err != nil {
		t.Fatalf("mkdir ops dir: %v", err)
	}

	writeDoc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Child",
			ID:      "2",
			Version: 1,
		},
		Body: "local child\n",
	}
	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "Ops", "Child.md"), writeDoc); err != nil {
		t.Fatalf("write old child doc: %v", err)
	}

	modifiedAt := time.Date(2026, time.March, 6, 13, 0, 0, 0, time.UTC)
	emptyADF := map[string]any{"version": 1, "type": "doc", "content": []any{}}
	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-1", ParentType: "folder", Version: 2, LastModified: modifiedAt},
		},
		folderByID: map[string]confluence.Folder{
			"folder-1": {ID: "folder-1", Title: "Ops!"},
		},
		pagesByID: map[string]confluence.Page{
			"2": {ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-1", ParentType: "folder", Version: 2, LastModified: modifiedAt, BodyADF: rawJSON(t, emptyADF)},
		},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		State: fs.SpaceState{
			PagePathIndex: map[string]string{
				"Ops/Child.md": "2",
			},
		},
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(spaceDir, "Ops!", "Child.md")); err != nil {
		t.Fatalf("expected moved markdown at Ops!/Child.md: %v", err)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, "Ops", "Child.md")); !os.IsNotExist(err) {
		t.Fatalf("old markdown path should be deleted, stat error=%v", err)
	}

	movedDiag := findPullDiagnostic(result.Diagnostics, "PAGE_PATH_MOVED")
	if movedDiag == nil {
		t.Fatalf("expected PAGE_PATH_MOVED diagnostic, got %+v", result.Diagnostics)
	}
	if movedDiag.Path != "Ops/Child.md" {
		t.Fatalf("moved diagnostic path = %q, want Ops/Child.md", movedDiag.Path)
	}
	if !strings.Contains(movedDiag.Message, "Ops/Child.md") || !strings.Contains(movedDiag.Message, "Ops!/Child.md") {
		t.Fatalf("move diagnostic message = %q, want old and new paths", movedDiag.Message)
	}
}

func findPullDiagnostic(diags []PullDiagnostic, code string) *PullDiagnostic {
	for i := range diags {
		if diags[i].Code == code {
			return &diags[i]
		}
	}
	return nil
}

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

func TestPull_FolderListFailureFallsBackToPageHierarchy(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{{
			ID:           "1",
			SpaceID:      "space-1",
			Title:        "Start Here",
			ParentPageID: "folder-1",
			ParentType:   "folder",
			Version:      1,
			LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
		}},
		folderErr: &confluence.APIError{
			StatusCode: 500,
			Method:     "GET",
			URL:        "/wiki/api/v2/folders",
			Message:    "Internal Server Error",
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Start Here",
				ParentPageID: "folder-1",
				ParentType:   "folder",
				Version:      1,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, sampleChildADF()),
			},
		},
		attachments: map[string][]byte{
			"att-2": []byte("inline-bytes"),
		},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
	})
	if err != nil {
		t.Fatalf("Pull() error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(spaceDir, "Start-Here.md")); err != nil {
		t.Fatalf("expected markdown to be written at top-level fallback path: %v", err)
	}

	foundFolderWarning := false
	for _, d := range result.Diagnostics {
		if d.Code == "FOLDER_LOOKUP_UNAVAILABLE" {
			foundFolderWarning = true
			break
		}
	}
	if !foundFolderWarning {
		t.Fatalf("expected FOLDER_LOOKUP_UNAVAILABLE diagnostic, got %+v", result.Diagnostics)
	}
}

func TestResolveFolderHierarchyFromPages_DeduplicatesFallbackDiagnostics(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	pages := []confluence.Page{
		{ID: "1", Title: "Root", ParentPageID: "folder-1", ParentType: "folder"},
		{ID: "2", Title: "Child", ParentPageID: "folder-2", ParentType: "folder"},
	}
	errBoom := &confluence.APIError{
		StatusCode: 500,
		Method:     "GET",
		URL:        "/wiki/api/v2/folders",
		Message:    "Internal Server Error",
	}
	remote := &fakePullRemote{
		folderErr: errBoom,
	}

	_, diagnostics, err := resolveFolderHierarchyFromPages(context.Background(), remote, pages)
	if err != nil {
		t.Fatalf("resolveFolderHierarchyFromPages() error: %v", err)
	}

	fallbackDiagnostics := 0
	for _, diag := range diagnostics {
		if diag.Code != "FOLDER_LOOKUP_UNAVAILABLE" {
			continue
		}
		fallbackDiagnostics++
		if strings.Contains(diag.Message, "Internal Server Error") {
			t.Fatalf("expected concise diagnostic without raw API error, got %q", diag.Message)
		}
		if strings.Contains(diag.Message, "/wiki/api/v2/folders") {
			t.Fatalf("expected concise diagnostic without raw API URL, got %q", diag.Message)
		}
		if !strings.Contains(diag.Message, "falling back to page-only hierarchy for affected pages") {
			t.Fatalf("expected concise fallback explanation, got %q", diag.Message)
		}
	}
	if fallbackDiagnostics != 1 {
		t.Fatalf("expected one deduplicated fallback diagnostic, got %+v", diagnostics)
	}

	gotLogs := logs.String()
	if strings.Count(gotLogs, "folder_lookup_unavailable_falling_back_to_pages") != 1 {
		t.Fatalf("expected one warning log with raw error details, got:\n%s", gotLogs)
	}
	if !strings.Contains(gotLogs, "Internal Server Error") {
		t.Fatalf("expected raw error details in logs, got:\n%s", gotLogs)
	}
	if !strings.Contains(gotLogs, "/wiki/api/v2/folders") {
		t.Fatalf("expected raw API URL in logs, got:\n%s", gotLogs)
	}
}

type folderLookupErrorByIDRemote struct {
	*fakePullRemote
	errorsByFolderID map[string]error
}

func (r *folderLookupErrorByIDRemote) GetFolder(ctx context.Context, folderID string) (confluence.Folder, error) {
	r.getFolderCalls = append(r.getFolderCalls, folderID)
	if err, ok := r.errorsByFolderID[folderID]; ok {
		return confluence.Folder{}, err
	}
	return r.fakePullRemote.GetFolder(ctx, folderID)
}

func TestResolveFolderHierarchyFromPages_DeduplicatesFallbackDiagnosticsAcrossFolderURLs(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	pages := []confluence.Page{
		{ID: "1", Title: "Root", ParentPageID: "folder-1", ParentType: "folder"},
		{ID: "2", Title: "Child", ParentPageID: "folder-2", ParentType: "folder"},
	}
	remote := &folderLookupErrorByIDRemote{
		fakePullRemote: &fakePullRemote{},
		errorsByFolderID: map[string]error{
			"folder-1": &confluence.APIError{
				StatusCode: 500,
				Method:     "GET",
				URL:        "/wiki/api/v2/folders/folder-1",
				Message:    "Internal Server Error",
			},
			"folder-2": &confluence.APIError{
				StatusCode: 500,
				Method:     "GET",
				URL:        "/wiki/api/v2/folders/folder-2",
				Message:    "Internal Server Error",
			},
		},
	}

	_, diagnostics, err := resolveFolderHierarchyFromPages(context.Background(), remote, pages)
	if err != nil {
		t.Fatalf("resolveFolderHierarchyFromPages() error: %v", err)
	}

	fallbackDiagnostics := 0
	for _, diag := range diagnostics {
		if diag.Code == "FOLDER_LOOKUP_UNAVAILABLE" {
			fallbackDiagnostics++
		}
	}
	if fallbackDiagnostics != 1 {
		t.Fatalf("expected one deduplicated fallback diagnostic across folder URLs, got %+v", diagnostics)
	}

	gotLogs := logs.String()
	if count := strings.Count(gotLogs, "folder_lookup_unavailable_falling_back_to_pages"); count != 1 {
		t.Fatalf("expected one warning log across folder URLs, got %d:\n%s", count, gotLogs)
	}
	if count := strings.Count(gotLogs, "folder_lookup_unavailable_repeats_suppressed"); count != 1 {
		t.Fatalf("expected one suppression log across folder URLs, got %d:\n%s", count, gotLogs)
	}
}

func TestPull_ForceFullPullsAllPagesWithoutIncrementalChanges(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	initialDoc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version:                1,
			ConfluenceLastModified: "2026-02-01T08:00:00Z",
		},
		Body: "old body\n",
	}
	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "root.md"), initialDoc); err != nil {
		t.Fatalf("write root.md: %v", err)
	}

	state := fs.SpaceState{
		LastPullHighWatermark: "2026-02-02T00:00:00Z",
		PagePathIndex: map[string]string{
			"root.md": "1",
		},
		AttachmentIndex: map[string]string{},
	}

	remotePage := confluence.Page{
		ID:           "1",
		SpaceID:      "space-1",
		Title:        "Root",
		Version:      2,
		LastModified: time.Date(2026, time.February, 1, 10, 0, 0, 0, time.UTC),
		BodyADF: rawJSON(t, map[string]any{
			"version": 1,
			"type":    "doc",
			"content": []any{
				map[string]any{
					"type": "paragraph",
					"content": []any{
						map[string]any{
							"type": "text",
							"text": "new body",
						},
					},
				},
			},
		}),
	}

	noForceRemote := &fakePullRemote{
		space:   confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages:   []confluence.Page{{ID: "1", SpaceID: "space-1", Title: "Root", Version: 2, LastModified: remotePage.LastModified}},
		changes: []confluence.Change{},
		pagesByID: map[string]confluence.Page{
			"1": remotePage,
		},
		attachments: map[string][]byte{},
	}

	resultNoForce, err := Pull(context.Background(), noForceRemote, PullOptions{
		SpaceKey:      "ENG",
		SpaceDir:      spaceDir,
		State:         state,
		PullStartedAt: time.Date(2026, time.February, 2, 1, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Pull() without force error: %v", err)
	}
	if len(resultNoForce.UpdatedMarkdown) != 1 || resultNoForce.UpdatedMarkdown[0] != "root.md" {
		t.Fatalf("expected incremental pull to update root.md without force, got %+v", resultNoForce.UpdatedMarkdown)
	}

	rootNoForce, err := fs.ReadMarkdownDocument(filepath.Join(spaceDir, "root.md"))
	if err != nil {
		t.Fatalf("read root.md without force: %v", err)
	}
	if !strings.Contains(rootNoForce.Body, "new body") {
		t.Fatalf("root.md should be updated without force when the remote version advances")
	}

	forceRemote := &fakePullRemote{
		space:   confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages:   []confluence.Page{{ID: "1", SpaceID: "space-1", Title: "Root", Version: 2, LastModified: remotePage.LastModified}},
		changes: []confluence.Change{},
		pagesByID: map[string]confluence.Page{
			"1": remotePage,
		},
		attachments: map[string][]byte{},
	}

	resultForce, err := Pull(context.Background(), forceRemote, PullOptions{
		SpaceKey:      "ENG",
		SpaceDir:      spaceDir,
		State:         state,
		PullStartedAt: time.Date(2026, time.February, 2, 1, 0, 0, 0, time.UTC),
		ForceFull:     true,
	})
	if err != nil {
		t.Fatalf("Pull() with force error: %v", err)
	}
	if len(resultForce.UpdatedMarkdown) != 1 {
		t.Fatalf("expected one updated markdown with force, got %+v", resultForce.UpdatedMarkdown)
	}

	rootForce, err := fs.ReadMarkdownDocument(filepath.Join(spaceDir, "root.md"))
	if err != nil {
		t.Fatalf("read root.md with force: %v", err)
	}
	if !strings.Contains(rootForce.Body, "new body") {
		t.Fatalf("root.md should be updated with force; got body:\n%s", rootForce.Body)
	}
}

func TestListAllChanges_UsesContinuationOffsets(t *testing.T) {
	starts := make([]int, 0)

	remote := &fakePullRemote{
		listChangesFunc: func(opts confluence.ChangeListOptions) (confluence.ChangeListResult, error) {
			starts = append(starts, opts.Start)
			switch opts.Start {
			case 0:
				return confluence.ChangeListResult{
					Changes:   []confluence.Change{{PageID: "1"}},
					NextStart: 50,
					HasMore:   true,
				}, nil
			case 50:
				return confluence.ChangeListResult{
					Changes:   []confluence.Change{{PageID: "2"}},
					NextStart: 100,
					HasMore:   true,
				}, nil
			case 100:
				return confluence.ChangeListResult{
					Changes: []confluence.Change{{PageID: "3"}},
					HasMore: false,
				}, nil
			default:
				return confluence.ChangeListResult{}, fmt.Errorf("unexpected start: %d", opts.Start)
			}
		},
	}

	changes, err := listAllChanges(context.Background(), remote, confluence.ChangeListOptions{
		SpaceKey: "ENG",
		Limit:    25,
	}, nil)
	if err != nil {
		t.Fatalf("listAllChanges() error: %v", err)
	}

	if len(changes) != 3 {
		t.Fatalf("changes count = %d, want 3", len(changes))
	}

	if len(starts) != 3 {
		t.Fatalf("starts count = %d, want 3", len(starts))
	}
	if starts[0] != 0 || starts[1] != 50 || starts[2] != 100 {
		t.Fatalf("starts = %v, want [0 50 100]", starts)
	}
}

func TestPull_DraftRecovery(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	// Local state knows about page 10 (draft)
	state := fs.SpaceState{
		PagePathIndex: map[string]string{
			"draft.md": "10",
		},
	}
	if err := fs.SaveState(spaceDir, state); err != nil {
		t.Fatalf("save state: %v", err)
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG"},
		// Remote space listing ONLY returns published pages (page 1)
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Published Page", Status: "current"},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:      "1",
				SpaceID: "space-1",
				Title:   "Published Page",
				Status:  "current",
				BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
			},
			"10": {
				ID:      "10",
				SpaceID: "space-1",
				Title:   "Draft Page",
				Status:  "draft", // This page is a draft
				BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
			},
		},
	}

	res, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		State:    state,
	})
	if err != nil {
		t.Fatalf("Pull() unexpected error: %v", err)
	}

	// Draft page should be preserved, not deleted
	foundDraft := false
	for _, p := range res.UpdatedMarkdown {
		if p == "draft.md" {
			foundDraft = true
			break
		}
	}
	if !foundDraft {
		t.Errorf("draft.md not found in updated markdown, was it erroneously deleted?")
	}

	// Verify draft frontmatter
	doc, err := fs.ReadMarkdownDocument(filepath.Join(spaceDir, "draft.md"))
	if err != nil {
		t.Fatalf("read draft.md: %v", err)
	}
	if doc.Frontmatter.State != "draft" {
		t.Errorf("draft.md status = %q, want draft", doc.Frontmatter.State)
	}
}

func TestPull_TrashedRecoveryDeletesLocalPage(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	trashedPath := filepath.Join(spaceDir, "trashed.md")
	if err := fs.WriteMarkdownDocument(trashedPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Trashed Page",
			ID:    "10",

			Version: 3,
			State:   "trashed",
		},
		Body: "stale local copy\n",
	}); err != nil {
		t.Fatalf("write trashed page: %v", err)
	}

	state := fs.SpaceState{
		SpaceKey: "ENG",
		PagePathIndex: map[string]string{
			"trashed.md": "10",
		},
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG"},
		pages: []confluence.Page{},
		pagesByID: map[string]confluence.Page{
			"10": {
				ID:      "10",
				SpaceID: "space-1",
				Title:   "Trashed Page",
				Status:  "trashed",
				BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
			},
		},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		State:    state,
	})
	if err != nil {
		t.Fatalf("Pull() unexpected error: %v", err)
	}

	if _, err := os.Stat(trashedPath); !os.IsNotExist(err) {
		t.Fatalf("trashed.md should be deleted, stat err=%v", err)
	}

	if _, exists := result.State.PagePathIndex["trashed.md"]; exists {
		t.Fatalf("state page_path_index should not include trashed.md")
	}

	foundDeleted := false
	for _, relPath := range result.DeletedMarkdown {
		if relPath == "trashed.md" {
			foundDeleted = true
			break
		}
	}
	if !foundDeleted {
		t.Fatalf("expected trashed.md in deleted markdown list, got %v", result.DeletedMarkdown)
	}
}

func TestPull_RemovesLocalAttachmentWhenRemoteNoLongerReferencesIt(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	staleAssetPath := filepath.Join(spaceDir, "assets", "1", "att-legacy-binary.bin")
	if err := os.MkdirAll(filepath.Dir(staleAssetPath), 0o750); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}
	if err := os.WriteFile(staleAssetPath, []byte("legacy"), 0o600); err != nil {
		t.Fatalf("write stale attachment: %v", err)
	}

	state := fs.SpaceState{
		SpaceKey: "ENG",
		PagePathIndex: map[string]string{
			"root.md": "1",
		},
		AttachmentIndex: map[string]string{
			filepath.ToSlash("assets/1/att-legacy-binary.bin"): "att-legacy",
		},
	}

	remote := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG"},
		pages: []confluence.Page{
			{
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      2,
				LastModified: time.Date(2026, time.February, 2, 11, 0, 0, 0, time.UTC),
			},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      2,
				LastModified: time.Date(2026, time.February, 2, 11, 0, 0, 0, time.UTC),
				BodyADF:      []byte(`{"version":1,"type":"doc","content":[]}`),
			},
		},
		attachments: map[string][]byte{},
	}

	result, err := Pull(context.Background(), remote, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		State:    state,
	})
	if err != nil {
		t.Fatalf("Pull() unexpected error: %v", err)
	}

	if _, err := os.Stat(staleAssetPath); !os.IsNotExist(err) {
		t.Fatalf("expected stale attachment to be deleted from local workspace, stat=%v", err)
	}

	if _, exists := result.State.AttachmentIndex[filepath.ToSlash("assets/1/att-legacy-binary.bin")]; exists {
		t.Fatalf("expected stale attachment index to be removed")
	}

	if len(result.DeletedAssets) != 1 || result.DeletedAssets[0] != filepath.ToSlash("assets/1/att-legacy-binary.bin") {
		t.Fatalf("expected deleted assets to include stale attachment, got %+v", result.DeletedAssets)
	}
}
