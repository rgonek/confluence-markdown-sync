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

func TestPush_KeepOrphanAssetsPreservesUnreferencedAttachment(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

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
			Title: "Root",
			ID:    "1",

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
			Title: "Root",
			ID:    "1",

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
			Title: "Root",
			ID:    "1",

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

func TestOutsideSpaceAssetError_ContainsSuggestedPath(t *testing.T) {
	spaceDir := t.TempDir()
	sourcePath := filepath.Join(spaceDir, "docs", "page.md")
	destination := "../../../somewhere/image.png"

	err := outsideSpaceAssetError(spaceDir, sourcePath, destination)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "image.png") {
		t.Errorf("error message missing filename: %q", msg)
	}
	if !strings.Contains(msg, "assets/") {
		t.Errorf("error message missing assets path hint: %q", msg)
	}
}

func TestOutsideSpaceAssetError_EmptyDestination(t *testing.T) {
	spaceDir := t.TempDir()
	sourcePath := filepath.Join(spaceDir, "page.md")

	err := outsideSpaceAssetError(spaceDir, sourcePath, "   ")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// empty destination should fall back to "file" placeholder
	if !strings.Contains(err.Error(), "file") {
		t.Errorf("expected 'file' placeholder in message: %q", err.Error())
	}
}
