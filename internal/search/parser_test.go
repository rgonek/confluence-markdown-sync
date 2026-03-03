package search

import (
	"testing"
)

func TestParseMarkdownStructure_EmptyInput(t *testing.T) {
	result := ParseMarkdownStructure([]byte(""))
	if len(result.Sections) != 0 {
		t.Errorf("expected 0 sections, got %d", len(result.Sections))
	}
	if len(result.CodeBlocks) != 0 {
		t.Errorf("expected 0 code blocks, got %d", len(result.CodeBlocks))
	}
}

func TestParseMarkdownStructure_NoHeadings(t *testing.T) {
	src := `This is just a paragraph.

Another paragraph without headings.
`
	result := ParseMarkdownStructure([]byte(src))
	if len(result.Sections) != 0 {
		t.Errorf("expected 0 sections, got %d", len(result.Sections))
	}
}

func TestParseMarkdownStructure_SingleHeading(t *testing.T) {
	src := `# Overview

This is the overview text.
Some more content here.
`
	result := ParseMarkdownStructure([]byte(src))
	if len(result.Sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(result.Sections))
	}

	s := result.Sections[0]
	if s.HeadingText != "Overview" {
		t.Errorf("expected heading text 'Overview', got %q", s.HeadingText)
	}
	if s.HeadingLevel != 1 {
		t.Errorf("expected heading level 1, got %d", s.HeadingLevel)
	}
	if s.Line != 1 {
		t.Errorf("expected heading line 1, got %d", s.Line)
	}
	if len(s.HeadingPath) != 1 {
		t.Errorf("expected 1 path entry, got %d", len(s.HeadingPath))
	}
	if s.HeadingPath[0] != "# Overview" {
		t.Errorf("unexpected heading path: %v", s.HeadingPath)
	}
	if s.Content == "" {
		t.Error("expected non-empty section content")
	}
}

func TestParseMarkdownStructure_NestedHeadings(t *testing.T) {
	src := `# Top Level

Top level content.

## Sub Section

Sub section content.

### Deep Section

Deep content.

## Another Sub

Another sub content.
`
	result := ParseMarkdownStructure([]byte(src))
	if len(result.Sections) != 4 {
		t.Fatalf("expected 4 sections, got %d: %+v", len(result.Sections), result.Sections)
	}

	// Sections are appended in closure order (innermost first):
	// 0. "### Deep Section" closes when "## Another Sub" arrives
	// 1. "## Sub Section" closes when "## Another Sub" arrives
	// 2. "## Another Sub" closes at end of file
	// 3. "# Top Level" closes at end of file (outermost, last)

	// Verify top level is among sections.
	foundTop := false
	for _, s := range result.Sections {
		if s.HeadingText == "Top Level" && s.HeadingLevel == 1 {
			foundTop = true
		}
	}
	if !foundTop {
		t.Error("expected to find 'Top Level' section")
	}

	// Verify "Deep Section" has 3-level path.
	for _, s := range result.Sections {
		if s.HeadingText == "Deep Section" {
			if len(s.HeadingPath) != 3 {
				t.Errorf("Deep Section: expected 3-level path, got %d: %v", len(s.HeadingPath), s.HeadingPath)
			}
			if s.HeadingPath[0] != "# Top Level" {
				t.Errorf("Deep Section path[0]: expected '# Top Level', got %q", s.HeadingPath[0])
			}
			if s.HeadingPath[1] != "## Sub Section" {
				t.Errorf("Deep Section path[1]: expected '## Sub Section', got %q", s.HeadingPath[1])
			}
			if s.HeadingPath[2] != "### Deep Section" {
				t.Errorf("Deep Section path[2]: expected '### Deep Section', got %q", s.HeadingPath[2])
			}
		}
	}
}

func TestParseMarkdownStructure_CodeBlock(t *testing.T) {
	src := `# Auth Flow

Some auth description.

## Token Refresh

` + "```go" + `
func refresh(token string) error {
    return nil
}
` + "```" + `

More text after.
`
	result := ParseMarkdownStructure([]byte(src))

	if len(result.CodeBlocks) != 1 {
		t.Fatalf("expected 1 code block, got %d", len(result.CodeBlocks))
	}

	cb := result.CodeBlocks[0]
	if cb.Language != "go" {
		t.Errorf("expected language 'go', got %q", cb.Language)
	}
	if cb.HeadingText != "Token Refresh" {
		t.Errorf("expected heading text 'Token Refresh', got %q", cb.HeadingText)
	}
	if cb.HeadingLevel != 2 {
		t.Errorf("expected heading level 2, got %d", cb.HeadingLevel)
	}
	if len(cb.HeadingPath) != 2 {
		t.Errorf("expected 2-level heading path, got %d: %v", len(cb.HeadingPath), cb.HeadingPath)
	}
	if cb.Content == "" {
		t.Error("expected non-empty code content")
	}
	if cb.Line == 0 {
		t.Error("expected non-zero code block line")
	}
}

func TestParseMarkdownStructure_CodeBlockBeforeHeading(t *testing.T) {
	src := "```bash\necho hello\n```\n\n# Heading After\n\nContent.\n"
	result := ParseMarkdownStructure([]byte(src))

	if len(result.CodeBlocks) != 1 {
		t.Fatalf("expected 1 code block, got %d", len(result.CodeBlocks))
	}
	cb := result.CodeBlocks[0]
	if cb.HeadingText != "" {
		t.Errorf("expected empty heading text for pre-heading code block, got %q", cb.HeadingText)
	}
	if len(cb.HeadingPath) != 0 {
		t.Errorf("expected empty heading path for pre-heading code block, got %v", cb.HeadingPath)
	}
}

func TestParseMarkdownStructure_LineNumbers(t *testing.T) {
	src := "# First\n\nContent.\n\n## Second\n\nMore content.\n"
	result := ParseMarkdownStructure([]byte(src))

	if len(result.Sections) < 2 {
		t.Fatalf("expected at least 2 sections, got %d", len(result.Sections))
	}

	// Find sections by heading text.
	sectionLines := map[string]int{}
	for _, s := range result.Sections {
		sectionLines[s.HeadingText] = s.Line
	}

	if sectionLines["First"] != 1 {
		t.Errorf("expected 'First' at line 1, got %d", sectionLines["First"])
	}
	if sectionLines["Second"] != 5 {
		t.Errorf("expected 'Second' at line 5, got %d", sectionLines["Second"])
	}
}

func TestParseMarkdownStructure_MultipleCodeBlocks(t *testing.T) {
	src := `# Section

` + "```sql" + `
SELECT 1;
` + "```" + `

` + "```python" + `
print("hello")
` + "```" + `
`
	result := ParseMarkdownStructure([]byte(src))

	if len(result.CodeBlocks) != 2 {
		t.Fatalf("expected 2 code blocks, got %d", len(result.CodeBlocks))
	}
	if result.CodeBlocks[0].Language != "sql" {
		t.Errorf("first block: expected 'sql', got %q", result.CodeBlocks[0].Language)
	}
	if result.CodeBlocks[1].Language != "python" {
		t.Errorf("second block: expected 'python', got %q", result.CodeBlocks[1].Language)
	}
}

func TestParseMarkdownStructure_FrontmatterIgnored(t *testing.T) {
	// The parser receives the body only (frontmatter already stripped by ReadMarkdownDocument).
	// Verify graceful handling of YAML-fence-like content inside body.
	src := `# Title

Content with --- dashes --- mid-sentence.
`
	result := ParseMarkdownStructure([]byte(src))
	if len(result.Sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(result.Sections))
	}
}
