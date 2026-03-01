package fs

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseAndFormatMarkdownDocument_RoundTrip(t *testing.T) {
	input := `---
title: Page Title
created_by: jane@example.com
created_at: 2026-02-10T10:00:00Z
updated_by: john@example.com
updated_at: 2026-02-11T11:00:00Z
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
	if doc.Frontmatter.ID != "12345" {
		t.Fatalf("ID = %q, want 12345", doc.Frontmatter.ID)
	}
	if doc.Frontmatter.Extra["custom_field"] != "custom" {
		t.Fatalf("custom_field = %#v, want custom", doc.Frontmatter.Extra["custom_field"])
	}
	if doc.Frontmatter.CreatedBy != "jane@example.com" {
		t.Fatalf("CreatedBy = %q, want jane@example.com", doc.Frontmatter.CreatedBy)
	}
	if doc.Frontmatter.UpdatedBy != "john@example.com" {
		t.Fatalf("UpdatedBy = %q, want john@example.com", doc.Frontmatter.UpdatedBy)
	}
	if doc.Frontmatter.UpdatedAt != "2026-02-11T11:00:00Z" {
		t.Fatalf("UpdatedAt = %q, want 2026-02-11T11:00:00Z", doc.Frontmatter.UpdatedAt)
	}
	if doc.Body == "" {
		t.Fatal("body should not be empty")
	}

	out, err := FormatMarkdownDocument(doc)
	if err != nil {
		t.Fatalf("FormatMarkdownDocument() unexpected error: %v", err)
	}
	if strings.Contains(string(out), "confluence_page_id:") || strings.Contains(string(out), "confluence_space_key:") || strings.Contains(string(out), "confluence_version:") {
		t.Fatalf("formatted output should use canonical keys, got:\n%s", string(out))
	}
	if strings.Contains(string(out), "author:") || strings.Contains(string(out), "last_modified_by:") || strings.Contains(string(out), "last_modified_at:") {
		t.Fatalf("formatted output should use created_/updated_ keys, got:\n%s", string(out))
	}
	if !strings.Contains(string(out), "\nid:") || !strings.Contains(string(out), "\nversion:") {
		t.Fatalf("formatted output missing canonical keys, got:\n%s", string(out))
	}
	if strings.Contains(string(out), "\nspace:") {
		t.Fatalf("formatted output should omit space key, got:\n%s", string(out))
	}
	if !strings.Contains(string(out), "\ncreated_by:") || !strings.Contains(string(out), "\nupdated_by:") || !strings.Contains(string(out), "\nupdated_at:") {
		t.Fatalf("formatted output missing metadata keys, got:\n%s", string(out))
	}

	_, err = ParseMarkdownDocument(out)
	if err != nil {
		t.Fatalf("ParseMarkdownDocument(second pass) unexpected error: %v", err)
	}
}

func TestReadWriteMarkdownDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "space", "page.md")

	doc := MarkdownDocument{
		Frontmatter: Frontmatter{
			Title: "Test",
			ID:    "22",

			Version: 3,
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
	if got.Frontmatter.ID != "22" {
		t.Fatalf("ID = %q, want 22", got.Frontmatter.ID)
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
	if !result.IsValid() {
		t.Fatalf("ValidateFrontmatterSchema() unexpected issues for empty frontmatter: %#v", result.Issues)
	}

	result = ValidateFrontmatterSchema(Frontmatter{
		ID:      "10",
		Version: 2,
	})
	if !result.IsValid() {
		t.Fatalf("ValidateFrontmatterSchema() unexpected issues: %#v", result.Issues)
	}

	result = ValidateFrontmatterSchema(Frontmatter{
		State: "draft",
	})
	if !result.IsValid() {
		t.Fatalf("ValidateFrontmatterSchema(draft) unexpected issues: %#v", result.Issues)
	}

	result = ValidateFrontmatterSchema(Frontmatter{
		State: "invalid",
	})
	if result.IsValid() {
		t.Fatal("ValidateFrontmatterSchema(invalid) should fail")
	}
}

func TestNormalizeLabels_DedupesAndSorts(t *testing.T) {
	labels := []string{" team ", "OPS", "team", "ops", "", "  "}
	got := NormalizeLabels(labels)
	want := []string{"ops", "team"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeLabels() = %v, want %v", got, want)
	}
}

func TestValidateFrontmatterSchema_InvalidLabels(t *testing.T) {
	result := ValidateFrontmatterSchema(Frontmatter{

		Labels: []string{"", "  ", "ready to review", "tab\tlabel"},
	})
	if result.IsValid() {
		t.Fatal("ValidateFrontmatterSchema() should fail for invalid labels")
	}

	if len(result.Issues) != 4 {
		t.Fatalf("issues = %d, want 4", len(result.Issues))
	}

	messages := make([]string, 0, len(result.Issues))
	for _, issue := range result.Issues {
		messages = append(messages, issue.Message)
	}

	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, "empty after trimming") {
		t.Fatalf("expected empty label issue, got:\n%s", joined)
	}
	if !strings.Contains(joined, `"ready to review"`) {
		t.Fatalf("expected whitespace label issue to identify label value, got:\n%s", joined)
	}
	if !strings.Contains(joined, `"tab\tlabel"`) {
		t.Fatalf("expected tab whitespace label issue to identify label value, got:\n%s", joined)
	}
}

func TestValidateImmutableFrontmatter_State(t *testing.T) {
	previous := Frontmatter{
		ID:    "1",
		State: "current",
	}
	current := Frontmatter{
		ID:    "1",
		State: "draft",
	}

	result := ValidateImmutableFrontmatter(previous, current)
	if result.IsValid() {
		t.Fatal("ValidateImmutableFrontmatter() should block current -> draft transition")
	}

	// draft -> current should be allowed
	previous.State = "draft"
	current.State = "current"
	result = ValidateImmutableFrontmatter(previous, current)
	if !result.IsValid() {
		t.Fatalf("ValidateImmutableFrontmatter() should allow draft -> current transition: %v", result.Issues)
	}

	// draft -> draft should be allowed
	current.State = "draft"
	result = ValidateImmutableFrontmatter(previous, current)
	if !result.IsValid() {
		t.Fatalf("ValidateImmutableFrontmatter() should allow draft -> draft transition: %v", result.Issues)
	}
}

func TestValidateImmutableFrontmatter(t *testing.T) {
	previous := Frontmatter{
		ID: "1",
	}
	current := Frontmatter{
		ID: "2",
	}

	result := ValidateImmutableFrontmatter(previous, current)
	if result.IsValid() {
		t.Fatal("ValidateImmutableFrontmatter() should fail when immutable keys change")
	}
	if len(result.Issues) != 1 {
		t.Fatalf("issues = %d, want 1", len(result.Issues))
	}
}
