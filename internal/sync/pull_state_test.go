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
