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

	hook := NewForwardLinkHook(sourcePath, pagePathByID)
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
	out, err = hook(ctx, in)
	if out.Handled {
		t.Error("Expected Handled=false for unknown ID")
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

func TestReverseMediaHook(t *testing.T) {
	// Need to create real files for Stat check
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "myspace")
	err := os.MkdirAll(filepath.Join(spaceDir, "assets"), 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Create dummy asset
	assetPath := filepath.Join(spaceDir, "assets", "image.png")
	err = os.WriteFile(assetPath, []byte("fake image"), 0644)
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

	// Test 2: New asset (not in index)
	newAssetPath := filepath.Join(spaceDir, "assets", "new.png")
	err = os.WriteFile(newAssetPath, []byte("new image"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	in.Destination = "assets/new.png"

	out, err = hook(ctx, in)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if out.ID != "new-attachment-placeholder" {
		t.Errorf("Expected placeholder ID, got %q", out.ID)
	}

	// Test 3: Missing asset (should fail)
	in.Destination = "assets/missing.png"
	_, err = hook(ctx, in)
	if err != mdconv.ErrUnresolved {
		t.Errorf("Expected ErrUnresolved for missing file, got %v", err)
	}
}
