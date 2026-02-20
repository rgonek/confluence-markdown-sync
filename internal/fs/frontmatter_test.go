package fs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParseAndFormatMarkdownDocument_RoundTrip(t *testing.T) {
	input := `---
title: Page Title
confluence_page_id: "12345"
confluence_space_key: DOCS
confluence_version: 7
confluence_last_modified: 2026-02-18T10:11:12Z
confluence_parent_page_id: "999"
custom_field: custom
---
# Heading

Body text.
`

	doc, err := ParseMarkdownDocument([]byte(input))
	if err != nil {
		t.Fatalf("ParseMarkdownDocument() unexpected error: %v", err)
	}
	if doc.Frontmatter.ConfluencePageID != "12345" {
		t.Fatalf("ConfluencePageID = %q, want 12345", doc.Frontmatter.ConfluencePageID)
	}
	if doc.Frontmatter.Extra["custom_field"] != "custom" {
		t.Fatalf("custom_field = %#v, want custom", doc.Frontmatter.Extra["custom_field"])
	}
	if doc.Body == "" {
		t.Fatal("body should not be empty")
	}

	out, err := FormatMarkdownDocument(doc)
	if err != nil {
		t.Fatalf("FormatMarkdownDocument() unexpected error: %v", err)
	}

	parsedAgain, err := ParseMarkdownDocument(out)
	if err != nil {
		t.Fatalf("ParseMarkdownDocument(second pass) unexpected error: %v", err)
	}
	if parsedAgain.Frontmatter.ConfluenceSpaceKey != "DOCS" {
		t.Fatalf("ConfluenceSpaceKey(second pass) = %q, want DOCS", parsedAgain.Frontmatter.ConfluenceSpaceKey)
	}
}

func TestReadWriteMarkdownDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "space", "page.md")

	doc := MarkdownDocument{
		Frontmatter: Frontmatter{
			Title:                  "Test",
			ConfluencePageID:       "22",
			ConfluenceSpaceKey:     "ENG",
			ConfluenceVersion:      3,
			ConfluenceLastModified: "2026-02-18T10:00:00Z",
		},
		Body: "# Body\n",
	}

	if err := WriteMarkdownDocument(path, doc); err != nil {
		t.Fatalf("WriteMarkdownDocument() unexpected error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("written file not found: %v", err)
	}

	got, err := ReadMarkdownDocument(path)
	if err != nil {
		t.Fatalf("ReadMarkdownDocument() unexpected error: %v", err)
	}
	if got.Frontmatter.ConfluencePageID != "22" {
		t.Fatalf("ConfluencePageID = %q, want 22", got.Frontmatter.ConfluencePageID)
	}
}

func TestParseMarkdownDocument_MissingFrontmatter(t *testing.T) {
	_, err := ParseMarkdownDocument([]byte("# No frontmatter"))
	if !errors.Is(err, ErrFrontmatterMissing) {
		t.Fatalf("error = %v, want ErrFrontmatterMissing", err)
	}
}

func TestValidateFrontmatterSchema(t *testing.T) {
	result := ValidateFrontmatterSchema(Frontmatter{})
	if result.IsValid() {
		t.Fatal("ValidateFrontmatterSchema() should fail for empty frontmatter")
	}

	result = ValidateFrontmatterSchema(Frontmatter{
		ConfluencePageID:       "10",
		ConfluenceSpaceKey:     "OPS",
		ConfluenceVersion:      2,
		ConfluenceLastModified: "2026-02-18T10:00:00Z",
	})
	if !result.IsValid() {
		t.Fatalf("ValidateFrontmatterSchema() unexpected issues: %#v", result.Issues)
	}
}

func TestValidateImmutableFrontmatter(t *testing.T) {
	previous := Frontmatter{
		ConfluencePageID:   "1",
		ConfluenceSpaceKey: "ENG",
	}
	current := Frontmatter{
		ConfluencePageID:   "2",
		ConfluenceSpaceKey: "OPS",
	}

	result := ValidateImmutableFrontmatter(previous, current)
	if result.IsValid() {
		t.Fatal("ValidateImmutableFrontmatter() should fail when immutable keys change")
	}
	if len(result.Issues) != 2 {
		t.Fatalf("issues = %d, want 2", len(result.Issues))
	}
}
