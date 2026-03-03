package sync

import (
	"context"
	"fmt"
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
	if _, exists := result.State.AttachmentIndex["assets/2/att-2-inline.png"]; exists {
		t.Fatalf("state attachment_index should not include att-2 for unchanged page")
	}
	if _, exists := result.State.AttachmentIndex["assets/999/att-old-legacy.png"]; exists {
		t.Fatalf("state attachment_index should not include legacy asset")
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
	if len(resultNoForce.UpdatedMarkdown) != 0 {
		t.Fatalf("expected no updated markdown without force, got %+v", resultNoForce.UpdatedMarkdown)
	}

	rootNoForce, err := fs.ReadMarkdownDocument(filepath.Join(spaceDir, "root.md"))
	if err != nil {
		t.Fatalf("read root.md without force: %v", err)
	}
	if strings.Contains(rootNoForce.Body, "new body") {
		t.Fatalf("root.md should not be updated without force")
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
