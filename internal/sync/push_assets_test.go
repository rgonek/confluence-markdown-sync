package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildStrictAttachmentIndex_AssignsPendingIDsForLocalAssets(t *testing.T) {
	spaceDir := t.TempDir()
	sourcePath := filepath.Join(spaceDir, "root.md")
	assetPath := filepath.Join(spaceDir, "assets", "new.png")

	if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	index, refs, err := BuildStrictAttachmentIndex(
		spaceDir,
		sourcePath,
		"![asset](assets/new.png)\n",
		map[string]string{},
	)
	if err != nil {
		t.Fatalf("BuildStrictAttachmentIndex() error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "assets/new.png" {
		t.Fatalf("referenced assets = %v, want [assets/new.png]", refs)
	}
	if got := strings.TrimSpace(index["assets/new.png"]); !strings.HasPrefix(got, "pending-attachment-") {
		t.Fatalf("expected pending attachment id for assets/new.png, got %q", got)
	}
}

func TestCollectReferencedAssetPaths_AllowsNonAssetsReferenceWithinSpace(t *testing.T) {
	spaceDir := t.TempDir()
	sourcePath := filepath.Join(spaceDir, "root.md")
	nonAssetPath := filepath.Join(spaceDir, "images", "outside.png")

	if err := os.MkdirAll(filepath.Dir(nonAssetPath), 0o750); err != nil {
		t.Fatalf("mkdir images dir: %v", err)
	}
	if err := os.WriteFile(nonAssetPath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	refs, err := CollectReferencedAssetPaths(spaceDir, sourcePath, "![asset](images/outside.png)\n")
	if err != nil {
		t.Fatalf("CollectReferencedAssetPaths() error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "images/outside.png" {
		t.Fatalf("referenced assets = %v, want [images/outside.png]", refs)
	}
}

func TestCollectReferencedAssetPaths_IncludesLocalFileLinks(t *testing.T) {
	spaceDir := t.TempDir()
	sourcePath := filepath.Join(spaceDir, "root.md")
	docPath := filepath.Join(spaceDir, "assets", "manual.pdf")

	if err := os.MkdirAll(filepath.Dir(docPath), 0o750); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}
	if err := os.WriteFile(docPath, []byte("pdf"), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	refs, err := CollectReferencedAssetPaths(spaceDir, sourcePath, "[Manual](assets/manual.pdf)\n")
	if err != nil {
		t.Fatalf("CollectReferencedAssetPaths() error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "assets/manual.pdf" {
		t.Fatalf("referenced assets = %v, want [assets/manual.pdf]", refs)
	}
}

func TestCollectReferencedAssetPaths_FailsForOutsideSpaceReference(t *testing.T) {
	rootDir := t.TempDir()
	spaceDir := filepath.Join(rootDir, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	sourcePath := filepath.Join(spaceDir, "root.md")
	_, err := CollectReferencedAssetPaths(spaceDir, sourcePath, "![asset](../outside.png)\n")
	if err == nil {
		t.Fatal("expected outside-space media reference to fail")
	}
	if !strings.Contains(err.Error(), "outside the space directory") {
		t.Fatalf("expected actionable outside-space message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "assets/") {
		t.Fatalf("expected assets destination hint, got: %v", err)
	}
}

func TestPrepareMarkdownForAttachmentConversion_RewritesLinksToInlineMediaSpan(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")
	assetPath := filepath.Join(spaceDir, "assets", "manual.pdf")

	if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("pdf"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	body := "Before [Manual](assets/manual.pdf) after\n"
	prepared, err := PrepareMarkdownForAttachmentConversion(spaceDir, mdPath, body, map[string]string{"assets/manual.pdf": "att-1"})
	if err != nil {
		t.Fatalf("PrepareMarkdownForAttachmentConversion() error: %v", err)
	}

	if !strings.Contains(prepared, `{.media-inline`) {
		t.Fatalf("expected prepared markdown to include inline media span, got: %q", prepared)
	}
	if !strings.Contains(prepared, `media-id="att-1"`) {
		t.Fatalf("expected prepared markdown to include resolved media id, got: %q", prepared)
	}
	if strings.Contains(prepared, `![Manual]`) {
		t.Fatalf("expected prepared markdown to avoid image-prefix rewrite for links, got: %q", prepared)
	}
}
