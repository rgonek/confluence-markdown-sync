package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestPush_BlocksImmutableIDTampering(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "2",
			Space:   "ENG",
			Version: 1,
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := &fakeFolderPushRemote{
		foldersByID: map[string]confluence.Folder{},
	}

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		Domain:   "https://example.atlassian.net",
		State: fs.SpaceState{
			SpaceKey:      "ENG",
			PagePathIndex: map[string]string{"root.md": "1"},
		},
		Changes: []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err == nil {
		t.Fatal("expected immutable id validation error")
	}
	if !strings.Contains(err.Error(), "changed immutable id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPush_IgnoresFrontmatterSpace(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "OPS",
			Version: 1,
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "current",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		Domain:   "https://example.atlassian.net",
		State: fs.SpaceState{
			SpaceKey:      "ENG",
			PagePathIndex: map[string]string{"root.md": "1"},
		},
		Changes: []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err != nil {
		t.Fatalf("expected push success with ignored space key, got: %v", err)
	}
}

func TestPush_BlocksCurrentToDraftTransition(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
			State:   "draft",
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := &fakeFolderPushRemote{
		foldersByID: map[string]confluence.Folder{},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:      "1",
				SpaceID: "space-1",
				Title:   "Root",
				Status:  "current",
				Version: 1,
				BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
			},
		},
		pages: []confluence.Page{{
			ID:      "1",
			SpaceID: "space-1",
			Title:   "Root",
			Status:  "current",
			Version: 1,
		}},
	}

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		Domain:   "https://example.atlassian.net",
		State: fs.SpaceState{
			SpaceKey:      "ENG",
			PagePathIndex: map[string]string{"root.md": "1"},
		},
		Changes: []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err == nil {
		t.Fatal("expected current-to-draft validation error")
	}
	if !strings.Contains(err.Error(), "cannot be transitioned from current to draft") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, statErr := os.Stat(mdPath); statErr != nil {
		t.Fatalf("markdown file should remain present: %v", statErr)
	}
}

func TestPush_KeepOrphanAssetsPreservesUnreferencedAttachment(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "current",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:            "ENG",
		SpaceDir:            spaceDir,
		Domain:              "https://example.atlassian.net",
		KeepOrphanAssets:    true,
		ConflictPolicy:      PushConflictPolicyCancel,
		State:               fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}, AttachmentIndex: map[string]string{"assets/1/orphan.png": "att-1"}},
		Changes:             []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
		ArchiveTimeout:      confluence.DefaultArchiveTaskTimeout,
		ArchivePollInterval: confluence.DefaultArchiveTaskPollInterval,
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	if len(remote.deleteAttachmentCalls) != 0 {
		t.Fatalf("delete attachment calls = %d, want 0", len(remote.deleteAttachmentCalls))
	}
	if got := strings.TrimSpace(result.State.AttachmentIndex["assets/1/orphan.png"]); got != "att-1" {
		t.Fatalf("attachment index value = %q, want att-1", got)
	}

	hasPreservedDiagnostic := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ATTACHMENT_PRESERVED" {
			hasPreservedDiagnostic = true
			break
		}
	}
	if !hasPreservedDiagnostic {
		t.Fatalf("expected ATTACHMENT_PRESERVED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_MigratesLocalRelativeAssetIntoPageHierarchy(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")
	legacyAssetPath := filepath.Join(spaceDir, "diagram.png")

	if err := os.WriteFile(legacyAssetPath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
		},
		Body: "![diagram](./diagram.png)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "current",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		ConflictPolicy: PushConflictPolicyCancel,
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		Changes:        []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	targetAssetRelPath := "assets/1/diagram.png"
	targetAssetAbsPath := filepath.Join(spaceDir, filepath.FromSlash(targetAssetRelPath))
	if _, statErr := os.Stat(targetAssetAbsPath); statErr != nil {
		t.Fatalf("expected migrated asset %s to exist: %v", targetAssetRelPath, statErr)
	}
	if _, statErr := os.Stat(legacyAssetPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected original asset path to be removed, stat=%v", statErr)
	}

	updatedDoc, err := fs.ReadMarkdownDocument(mdPath)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	if !strings.Contains(updatedDoc.Body, "assets/1/diagram.png") {
		t.Fatalf("expected markdown body to reference migrated asset path, body=%q", updatedDoc.Body)
	}

	if got := strings.TrimSpace(result.State.AttachmentIndex[targetAssetRelPath]); got == "" {
		t.Fatalf("expected state attachment index to include %s", targetAssetRelPath)
	}
}

func TestPush_UploadsLocalFileLinksAsAttachments(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")
	assetPath := filepath.Join(spaceDir, "assets", "manual.pdf")

	if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("pdf"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
		},
		Body: "[Manual](assets/manual.pdf)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "current",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		ConflictPolicy: PushConflictPolicyCancel,
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		Changes:        []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	if remote.uploadAttachmentCalls != 1 {
		t.Fatalf("upload attachment calls = %d, want 1", remote.uploadAttachmentCalls)
	}

	payload, ok := remote.updateInputsByPageID["1"]
	if !ok {
		t.Fatalf("expected update payload for page 1")
	}
	body := string(payload.BodyADF)
	if !strings.Contains(body, `"type":"mediaInline"`) {
		t.Fatalf("expected update ADF to include mediaInline node for linked file, body=%s", body)
	}
	if !strings.Contains(body, `"id":"att-1"`) {
		t.Fatalf("expected linked file to resolve to uploaded attachment id, body=%s", body)
	}

	updatedDoc, err := fs.ReadMarkdownDocument(mdPath)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	if !strings.Contains(updatedDoc.Body, "[Manual](assets/1/manual.pdf)") {
		t.Fatalf("expected markdown link to be normalized into per-page assets directory, body=%q", updatedDoc.Body)
	}

	if got := strings.TrimSpace(result.State.AttachmentIndex["assets/1/manual.pdf"]); got != "att-1" {
		t.Fatalf("attachment index value = %q, want att-1", got)
	}
}

func TestPush_UploadsInlineLocalFileLinksWithoutEmbeddedPlaceholder(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")
	assetPath := filepath.Join(spaceDir, "assets", "manual.pdf")

	if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("pdf"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
		},
		Body: "Please review [Manual](assets/manual.pdf) before sign-off.\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "current",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		ConflictPolicy: PushConflictPolicyCancel,
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		Changes:        []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	payload, ok := remote.updateInputsByPageID["1"]
	if !ok {
		t.Fatalf("expected update payload for page 1")
	}
	body := string(payload.BodyADF)
	if !strings.Contains(body, `"type":"mediaInline"`) {
		t.Fatalf("expected update ADF to include mediaInline node, body=%s", body)
	}
	if strings.Contains(body, `[Embedded content]`) {
		t.Fatalf("expected inline file link conversion to avoid embedded placeholder, body=%s", body)
	}
}

func TestPush_PreflightStrictFailureSkipsRemoteMutations(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "new.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New",
			Space: "ENG",
		},
		Body: "[Broken](missing.md)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeAdd,
			Path: "new.md",
		}},
	})
	if err == nil {
		t.Fatal("expected strict conversion error")
	}
	if !strings.Contains(err.Error(), "strict conversion failed") {
		t.Fatalf("unexpected error: %v", err)
	}

	if remote.createPageCalls != 0 {
		t.Fatalf("create page calls = %d, want 0", remote.createPageCalls)
	}
	if remote.updatePageCalls != 0 {
		t.Fatalf("update page calls = %d, want 0", remote.updatePageCalls)
	}
	if remote.uploadAttachmentCalls != 0 {
		t.Fatalf("upload attachment calls = %d, want 0", remote.uploadAttachmentCalls)
	}
}

func TestPush_PreflightStrictResolvesCrossSpaceLinkWithGlobalIndex(t *testing.T) {
	repo := t.TempDir()
	engDir := filepath.Join(repo, "Engineering (ENG)")
	tdDir := filepath.Join(repo, "Technical Docs (TD)")
	if err := os.MkdirAll(engDir, 0o750); err != nil {
		t.Fatalf("mkdir eng dir: %v", err)
	}
	if err := os.MkdirAll(tdDir, 0o750); err != nil {
		t.Fatalf("mkdir td dir: %v", err)
	}

	mdPath := filepath.Join(engDir, "new.md")
	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New",
			Space: "ENG",
		},
		Body: "[Cross Space](../Technical%20Docs%20(TD)/target.md)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	targetPath := filepath.Join(tdDir, "target.md")
	if err := fs.WriteMarkdownDocument(targetPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Target",
			ID:      "200",
			Space:   "TD",
			Version: 1,
		},
		Body: "target\n",
	}); err != nil {
		t.Fatalf("write cross-space markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:            "ENG",
		SpaceDir:            engDir,
		Domain:              "https://example.atlassian.net",
		State:               fs.SpaceState{SpaceKey: "ENG"},
		GlobalPageIndex:     GlobalPageIndex{"200": targetPath},
		ConflictPolicy:      PushConflictPolicyCancel,
		Changes:             []PushFileChange{{Type: PushChangeAdd, Path: "new.md"}},
		ArchiveTimeout:      confluence.DefaultArchiveTaskTimeout,
		ArchivePollInterval: confluence.DefaultArchiveTaskPollInterval,
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}
	if len(result.Commits) != 1 {
		t.Fatalf("commit count = %d, want 1", len(result.Commits))
	}
}

func TestPush_ResolvesLinksBetweenSimultaneousNewPages(t *testing.T) {
	spaceDir := t.TempDir()

	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "Fancy-Extensions.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Fancy Extensions",
			Space: "ENG",
		},
		Body: "[New page](New-Page.md)\n",
	}); err != nil {
		t.Fatalf("write Fancy-Extensions.md: %v", err)
	}

	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "New-Page.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New Page",
			Space: "ENG",
		},
		Body: "new page body\n",
	}); err != nil {
		t.Fatalf("write New-Page.md: %v", err)
	}

	remote := newRollbackPushRemote()
	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{
			{Type: PushChangeAdd, Path: "Fancy-Extensions.md"},
			{Type: PushChangeAdd, Path: "New-Page.md"},
		},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	fancyID := strings.TrimSpace(result.State.PagePathIndex["Fancy-Extensions.md"])
	newPageID := strings.TrimSpace(result.State.PagePathIndex["New-Page.md"])
	if fancyID == "" || newPageID == "" {
		t.Fatalf("expected IDs for both new pages, got state index: %+v", result.State.PagePathIndex)
	}

	updateInput, ok := remote.updateInputsByPageID[fancyID]
	if !ok {
		t.Fatalf("expected update payload for Fancy-Extensions page ID %s", fancyID)
	}

	body := string(updateInput.BodyADF)
	if !strings.Contains(body, "pageId="+newPageID) {
		t.Fatalf("expected Fancy-Extensions link to resolve to new page ID %s, body=%s", newPageID, body)
	}
	if strings.Contains(body, "pending-page-") {
		t.Fatalf("expected final ADF to avoid pending page IDs, body=%s", body)
	}
}

func TestPush_NewPageFailsWhenTrackedPageWithSameTitleExistsInSameDirectory(t *testing.T) {
	spaceDir := t.TempDir()

	existingPath := filepath.Join(spaceDir, "Conflict-Test-Page.md")
	if err := fs.WriteMarkdownDocument(existingPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Conflict Test Page",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
		},
		Body: "existing\n",
	}); err != nil {
		t.Fatalf("write existing markdown: %v", err)
	}

	newPath := filepath.Join(spaceDir, "Conflict-Test.md")
	if err := fs.WriteMarkdownDocument(newPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Conflict Test Page",
			Space: "ENG",
		},
		Body: "new\n",
	}); err != nil {
		t.Fatalf("write new markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"Conflict-Test-Page.md": "1"}},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeAdd,
			Path: "Conflict-Test.md",
		}},
	})
	if err == nil {
		t.Fatal("expected duplicate title validation error")
	}
	if !strings.Contains(err.Error(), "duplicates tracked page") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPush_DeleteAlreadyArchivedPageTreatsArchiveAsNoOp(t *testing.T) {
	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Old",
		Version: 5,
		WebURL:  "https://example.atlassian.net/wiki/pages/1",
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])
	remote.archivePagesErr = confluence.ErrArchived

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: t.TempDir(),
		State: fs.SpaceState{
			SpaceKey: "ENG",
			PagePathIndex: map[string]string{
				"old.md": "1",
			},
			AttachmentIndex: map[string]string{
				"assets/1/att-1-file.png": "att-1",
			},
		},
		Changes: []PushFileChange{{Type: PushChangeDelete, Path: "old.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	if len(result.Commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(result.Commits))
	}
	if _, exists := result.State.PagePathIndex["old.md"]; exists {
		t.Fatalf("page index should not contain old.md after successful archive no-op")
	}
	if len(remote.archiveTaskCalls) != 0 {
		t.Fatalf("archive task calls = %d, want 0 when archive is already applied", len(remote.archiveTaskCalls))
	}

	foundDiagnostic := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ARCHIVE_ALREADY_APPLIED" {
			foundDiagnostic = true
			break
		}
	}
	if !foundDiagnostic {
		t.Fatalf("expected ARCHIVE_ALREADY_APPLIED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_ArchivedRemotePageReturnsActionableError(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "archived",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		Domain:   "https://example.atlassian.net",
		State: fs.SpaceState{
			SpaceKey:      "ENG",
			PagePathIndex: map[string]string{"root.md": "1"},
		},
		Changes: []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err == nil {
		t.Fatal("expected archived page error")
	}
	if !strings.Contains(err.Error(), "is archived remotely") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPush_DeleteBlocksLocalStateWhenArchiveTaskDoesNotComplete(t *testing.T) {
	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Old",
		Version: 5,
		WebURL:  "https://example.atlassian.net/wiki/pages/1",
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])
	remote.archiveTaskStatus = confluence.ArchiveTaskStatus{TaskID: "task-1", State: confluence.ArchiveTaskStateInProgress, RawStatus: "RUNNING"}
	remote.archiveTaskWaitErr = confluence.ErrArchiveTaskTimeout

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: t.TempDir(),
		State: fs.SpaceState{
			SpaceKey: "ENG",
			PagePathIndex: map[string]string{
				"old.md": "1",
			},
			AttachmentIndex: map[string]string{
				"assets/1/att-1-file.png": "att-1",
			},
		},
		Changes: []PushFileChange{{Type: PushChangeDelete, Path: "old.md"}},
	})
	if err == nil {
		t.Fatal("expected archive wait failure")
	}
	if !strings.Contains(err.Error(), "wait for archive task") {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Commits) != 0 {
		t.Fatalf("commits = %d, want 0", len(result.Commits))
	}
	if got := strings.TrimSpace(result.State.PagePathIndex["old.md"]); got != "1" {
		t.Fatalf("page index old.md = %q, want 1", got)
	}
	if got := strings.TrimSpace(result.State.AttachmentIndex["assets/1/att-1-file.png"]); got != "att-1" {
		t.Fatalf("attachment index was mutated on archive failure: %q", got)
	}
	if len(remote.deleteAttachmentCalls) != 0 {
		t.Fatalf("delete attachment calls = %d, want 0", len(remote.deleteAttachmentCalls))
	}

	hasTimeoutDiagnostic := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ARCHIVE_TASK_TIMEOUT" {
			hasTimeoutDiagnostic = true
			break
		}
	}
	if !hasTimeoutDiagnostic {
		t.Fatalf("expected ARCHIVE_TASK_TIMEOUT diagnostic, got %+v", result.Diagnostics)
	}
}
