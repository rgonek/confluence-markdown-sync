package sync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	adfconv "github.com/rgonek/jira-adf-converter/converter"
	mdconv "github.com/rgonek/jira-adf-converter/mdconverter"
)

func TestForwardLinkHook(t *testing.T) {
	sourcePath, _ := filepath.Abs("myspace/index.md")
	targetPath, _ := filepath.Abs("myspace/child.md")

	pagePathByID := map[string]string{
		"123": targetPath,
	}

	hook := NewForwardLinkHook(sourcePath, pagePathByID, "MYSPACE")
	ctx := context.Background()

	// Test 1: Known page ID
	in := adfconv.LinkRenderInput{
		Meta:  adfconv.LinkMetadata{PageID: "123"},
		Title: "My Link",
	}
	out, err := hook(ctx, in)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !out.Handled {
		t.Error("Expected Handled=true")
	}
	if out.Href != "child.md" {
		t.Errorf("Expected Href child.md, got %q", out.Href)
	}

	// Test 2: Unknown page ID
	in.Meta.PageID = "999"
	in.Meta.SpaceKey = "MYSPACE"
	_, err = hook(ctx, in)
	if err != adfconv.ErrUnresolved {
		t.Errorf("Expected ErrUnresolved for unknown same-space ID, got %v", err)
	}

	// Test 3: Unknown page ID in other space should fall back.
	in.Meta.SpaceKey = "OTHER"
	out, err = hook(ctx, in)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if out.Handled {
		t.Error("Expected Handled=false for cross-space unknown ID")
	}
}

func TestForwardMediaHook(t *testing.T) {
	sourcePath, _ := filepath.Abs("myspace/index.md")
	targetPath, _ := filepath.Abs("myspace/assets/image.png")

	attachmentPathByID := map[string]string{
		"att-123": targetPath,
	}

	hook := NewForwardMediaHook(sourcePath, attachmentPathByID)
	ctx := context.Background()

	// Test 1: Known attachment ID
	in := adfconv.MediaRenderInput{
		Meta: adfconv.MediaMetadata{AttachmentID: "att-123"},
		Alt:  "My Image",
	}
	out, err := hook(ctx, in)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !out.Handled {
		t.Error("Expected Handled=true")
	}
	// filepath.Rel on Windows uses backslash, but NewForwardMediaHook uses ToSlash
	expected := "![My Image](assets/image.png)"
	if out.Markdown != expected {
		t.Errorf("Expected markdown %q, got %q", expected, out.Markdown)
	}

	// Test 2: Unknown attachment should return unresolved.
	in.Meta.AttachmentID = "att-999"
	_, err = hook(ctx, in)
	if err != adfconv.ErrUnresolved {
		t.Errorf("Expected ErrUnresolved, got %v", err)
	}
}

func TestForwardMediaHook_RendersFileAsMarkdownLink(t *testing.T) {
	sourcePath, _ := filepath.Abs("myspace/index.md")
	targetPath, _ := filepath.Abs("myspace/assets/manual.pdf")

	hook := NewForwardMediaHook(sourcePath, map[string]string{"att-file": targetPath})
	out, err := hook(context.Background(), adfconv.MediaRenderInput{
		ID:        "att-file",
		MediaType: "file",
		Meta: adfconv.MediaMetadata{
			AttachmentID: "att-file",
			Filename:     "manual.pdf",
		},
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !out.Handled {
		t.Fatal("Expected Handled=true")
	}
	if got, want := out.Markdown, "[manual.pdf](assets/manual.pdf)"; got != want {
		t.Fatalf("Expected markdown %q, got %q", want, got)
	}
}

func TestForwardMediaHook_FallbackByPageAndFilename(t *testing.T) {
	sourcePath, _ := filepath.Abs("myspace/page.md")
	targetPath, _ := filepath.Abs("myspace/assets/42/att-123-diagram.png")

	attachmentPathByID := map[string]string{
		"att-123": targetPath,
	}

	hook := NewForwardMediaHook(sourcePath, attachmentPathByID)
	ctx := context.Background()

	out, err := hook(ctx, adfconv.MediaRenderInput{
		ID: "UNKNOWN_MEDIA_ID",
		Meta: adfconv.MediaMetadata{
			PageID:   "42",
			Filename: "diagram.png",
		},
		Alt: "Diagram",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !out.Handled {
		t.Fatal("Expected Handled=true for filename fallback")
	}

	expected := "![Diagram](assets/42/att-123-diagram.png)"
	if out.Markdown != expected {
		t.Fatalf("Expected markdown %q, got %q", expected, out.Markdown)
	}
}

func TestForwardMediaHook_FallbackStaysUnresolvedWhenAmbiguous(t *testing.T) {
	sourcePath, _ := filepath.Abs("myspace/page.md")
	firstPath, _ := filepath.Abs("myspace/assets/41/att-111-diagram.png")
	secondPath, _ := filepath.Abs("myspace/assets/42/att-222-diagram.png")

	attachmentPathByID := map[string]string{
		"att-111": firstPath,
		"att-222": secondPath,
	}

	hook := NewForwardMediaHook(sourcePath, attachmentPathByID)
	ctx := context.Background()

	_, err := hook(ctx, adfconv.MediaRenderInput{
		ID: "UNKNOWN_MEDIA_ID",
		Meta: adfconv.MediaMetadata{
			Filename: "diagram.png",
		},
	})
	if err != adfconv.ErrUnresolved {
		t.Fatalf("Expected ErrUnresolved for ambiguous fallback, got %v", err)
	}
}

func TestReverseLinkHook(t *testing.T) {
	spaceDir, _ := filepath.Abs("myspace")
	index := PageIndex{
		"other.md": "12345",
	}
	domain := "https://example.atlassian.net"
	hook := NewReverseLinkHook(spaceDir, index, domain)
	ctx := context.Background()

	// Test 1: Valid relative link
	in := mdconv.LinkParseInput{
		SourcePath:  filepath.Join(spaceDir, "index.md"),
		Destination: "other.md",
	}
	out, err := hook(ctx, in)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !out.Handled {
		t.Error("Expected Handled=true")
	}
	expected := "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=12345"
	if out.Destination != expected {
		t.Errorf("Expected %q, got %q", expected, out.Destination)
	}

	// Test 2: Unknown link (should fail)
	in.Destination = "unknown.md"
	_, err = hook(ctx, in)
	if err != mdconv.ErrUnresolved {
		t.Errorf("Expected ErrUnresolved, got %v", err)
	}

	// Test 3: Absolute URL (should be ignored)
	in.Destination = "https://google.com"
	out, err = hook(ctx, in)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if out.Handled {
		t.Error("Expected Handled=false for absolute URL")
	}
}

func TestReverseLinkHook_DecodesURLEncodedPath(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	hook := NewReverseLinkHook(spaceDir, PageIndex{"Target Page.md": "12345"}, "https://example.atlassian.net")
	out, err := hook(context.Background(), mdconv.LinkParseInput{
		SourcePath:  filepath.Join(spaceDir, "index.md"),
		Destination: "Target%20Page.md",
	})
	if err != nil {
		t.Fatalf("hook returned error: %v", err)
	}
	if !out.Handled {
		t.Fatal("expected encoded destination to be handled")
	}
	if got, want := out.Destination, "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=12345"; got != want {
		t.Fatalf("destination = %q, want %q", got, want)
	}
}

func TestReverseLinkHookWithGlobalIndex_ResolvesCrossSpaceLink(t *testing.T) {
	tmpDir := t.TempDir()
	engDir := filepath.Join(tmpDir, "Engineering (ENG)")
	tdDir := filepath.Join(tmpDir, "Technical Docs (TD)")
	if err := os.MkdirAll(engDir, 0o750); err != nil {
		t.Fatalf("mkdir eng dir: %v", err)
	}
	if err := os.MkdirAll(tdDir, 0o750); err != nil {
		t.Fatalf("mkdir td dir: %v", err)
	}

	targetPath := filepath.Join(tdDir, "Target Page.md")
	hook := NewReverseLinkHookWithGlobalIndex(
		engDir,
		PageIndex{"index.md": "1"},
		GlobalPageIndex{"77": targetPath},
		"https://example.atlassian.net",
	)

	out, err := hook(context.Background(), mdconv.LinkParseInput{
		SourcePath:  filepath.Join(engDir, "index.md"),
		Destination: "../Technical%20Docs%20(TD)/Target%20Page.md#section-a",
	})
	if err != nil {
		t.Fatalf("hook returned error: %v", err)
	}
	if !out.Handled {
		t.Fatal("expected cross-space destination to be handled")
	}
	if got, want := out.Destination, "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=77#section-a"; got != want {
		t.Fatalf("destination = %q, want %q", got, want)
	}
}

func TestReverseLinkHookWithGlobalIndex_ResolvesViaSameFileFallback(t *testing.T) {
	tmpDir := t.TempDir()
	engDir := filepath.Join(tmpDir, "Engineering (ENG)")
	tdDir := filepath.Join(tmpDir, "Technical Docs (TD)")
	if err := os.MkdirAll(engDir, 0o750); err != nil {
		t.Fatalf("mkdir eng dir: %v", err)
	}
	if err := os.MkdirAll(tdDir, 0o750); err != nil {
		t.Fatalf("mkdir td dir: %v", err)
	}

	realTargetPath := filepath.Join(tdDir, "Target Page.md")
	if err := os.WriteFile(realTargetPath, []byte("target"), 0o600); err != nil {
		t.Fatalf("write target file: %v", err)
	}

	aliasPath := filepath.Join(tdDir, "Target Alias.md")
	if err := os.Link(realTargetPath, aliasPath); err != nil {
		t.Skipf("hard links are not supported in this environment: %v", err)
	}

	hook := NewReverseLinkHookWithGlobalIndex(
		engDir,
		PageIndex{"index.md": "1"},
		GlobalPageIndex{"77": aliasPath},
		"https://example.atlassian.net",
	)

	out, err := hook(context.Background(), mdconv.LinkParseInput{
		SourcePath:  filepath.Join(engDir, "index.md"),
		Destination: "../Technical%20Docs%20(TD)/Target%20Page.md",
	})
	if err != nil {
		t.Fatalf("hook returned error: %v", err)
	}
	if !out.Handled {
		t.Fatal("expected same-file fallback to resolve destination")
	}
	if got, want := out.Destination, "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=77"; got != want {
		t.Fatalf("destination = %q, want %q", got, want)
	}
}

func TestReverseMediaHook(t *testing.T) {
	// Need to create real files for Stat check
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "myspace")
	err := os.MkdirAll(filepath.Join(spaceDir, "assets"), 0o750)
	if err != nil {
		t.Fatal(err)
	}

	// Create dummy asset
	assetPath := filepath.Join(spaceDir, "assets", "image.png")
	err = os.WriteFile(assetPath, []byte("fake image"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	attachmentIndex := map[string]string{
		filepath.ToSlash("assets/image.png"): "att-123",
	}

	hook := NewReverseMediaHook(spaceDir, attachmentIndex)
	ctx := context.Background()

	// Test 1: Known asset
	in := mdconv.MediaParseInput{
		SourcePath:  filepath.Join(spaceDir, "index.md"),
		Destination: "assets/image.png",
	}
	out, err := hook(ctx, in)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !out.Handled {
		t.Error("Expected Handled=true")
	}
	if out.ID != "att-123" {
		t.Errorf("Expected ID att-123, got %q", out.ID)
	}
	if out.MediaType != "image" {
		t.Errorf("Expected MediaType image, got %q", out.MediaType)
	}

	// Test 2: Non-image assets should map to file media type.
	pdfAssetPath := filepath.Join(spaceDir, "assets", "manual.PDF")
	err = os.WriteFile(pdfAssetPath, []byte("fake pdf"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	attachmentIndex[filepath.ToSlash("assets/manual.PDF")] = "att-124"
	in.Destination = "assets/manual.PDF"
	out, err = hook(ctx, in)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if out.MediaType != "file" {
		t.Errorf("Expected MediaType file, got %q", out.MediaType)
	}

	// Test 3: New asset (not in index)
	newAssetPath := filepath.Join(spaceDir, "assets", "new.png")
	err = os.WriteFile(newAssetPath, []byte("new image"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	in.Destination = "assets/new.png"

	_, err = hook(ctx, in)
	if err != mdconv.ErrUnresolved {
		t.Errorf("Expected ErrUnresolved for missing attachment mapping, got %v", err)
	}

	// Test 4: In-space non-assets path resolves when mapped.
	nonAssetPath := filepath.Join(spaceDir, "image-outside.png")
	err = os.WriteFile(nonAssetPath, []byte("outside"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	attachmentIndex[filepath.ToSlash("image-outside.png")] = "att-999"
	in.Destination = "image-outside.png"

	out, err = hook(ctx, in)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !out.Handled {
		t.Error("Expected Handled=true for in-space non-assets reference")
	}
	if out.ID != "att-999" {
		t.Errorf("Expected ID att-999, got %q", out.ID)
	}

	// Test 5: Missing asset (should fail)
	in.Destination = "assets/missing.png"
	_, err = hook(ctx, in)
	if err != mdconv.ErrUnresolved {
		t.Errorf("Expected ErrUnresolved for missing file, got %v", err)
	}
}
