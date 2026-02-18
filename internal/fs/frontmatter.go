package fs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	frontmatterDelimiter = "---"
)

var (
	// ImmutableFrontmatterKeys contains keys that cannot be changed manually.
	ImmutableFrontmatterKeys = []string{
		"confluence_page_id",
		"confluence_space_key",
	}

	// MutableBySyncFrontmatterKeys contains keys that are managed by sync operations.
	MutableBySyncFrontmatterKeys = []string{
		"confluence_version",
		"confluence_last_modified",
		"confluence_parent_page_id",
	}
)

var (
	// ErrFrontmatterMissing indicates markdown frontmatter was not found.
	ErrFrontmatterMissing = errors.New("missing YAML frontmatter")
	// ErrFrontmatterInvalid indicates markdown frontmatter is malformed.
	ErrFrontmatterInvalid = errors.New("invalid YAML frontmatter")
)

// Frontmatter holds known Confluence sync metadata keys plus optional custom keys.
type Frontmatter struct {
	Title                  string         `yaml:"title,omitempty"`
	ConfluencePageID       string         `yaml:"confluence_page_id"`
	ConfluenceSpaceKey     string         `yaml:"confluence_space_key"`
	ConfluenceVersion      int            `yaml:"confluence_version"`
	ConfluenceLastModified string         `yaml:"confluence_last_modified"`
	ConfluenceParentPageID string         `yaml:"confluence_parent_page_id,omitempty"`
	Extra                  map[string]any `yaml:",inline"`
}

// MarkdownDocument represents a markdown file with YAML frontmatter.
type MarkdownDocument struct {
	Frontmatter Frontmatter
	Body        string
}

// ValidationIssue captures a schema or invariants validation issue.
type ValidationIssue struct {
	Field   string
	Code    string
	Message string
}

// ValidationResult is a list of validation issues.
type ValidationResult struct {
	Issues []ValidationIssue
}

// IsValid reports whether validation produced no issues.
func (r ValidationResult) IsValid() bool {
	return len(r.Issues) == 0
}

// ReadMarkdownDocument reads and parses a markdown file.
func ReadMarkdownDocument(path string) (MarkdownDocument, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return MarkdownDocument{}, err
	}
	return ParseMarkdownDocument(raw)
}

// WriteMarkdownDocument writes a markdown file from structured data.
func WriteMarkdownDocument(path string, doc MarkdownDocument) error {
	raw, err := FormatMarkdownDocument(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

// ParseMarkdownDocument parses a markdown document with YAML frontmatter.
func ParseMarkdownDocument(raw []byte) (MarkdownDocument, error) {
	content := strings.TrimPrefix(string(raw), "\uFEFF")
	frontmatterBlock, body, err := splitFrontmatter(content)
	if err != nil {
		return MarkdownDocument{}, err
	}

	var fm Frontmatter
	if err := yaml.Unmarshal([]byte(frontmatterBlock), &fm); err != nil {
		return MarkdownDocument{}, fmt.Errorf("%w: %v", ErrFrontmatterInvalid, err)
	}
	if fm.Extra == nil {
		fm.Extra = map[string]any{}
	}
	return MarkdownDocument{
		Frontmatter: fm,
		Body:        body,
	}, nil
}

// FormatMarkdownDocument renders a markdown document with YAML frontmatter.
func FormatMarkdownDocument(doc MarkdownDocument) ([]byte, error) {
	rawFrontmatter, err := yaml.Marshal(doc.Frontmatter)
	if err != nil {
		return nil, fmt.Errorf("marshal frontmatter: %w", err)
	}
	if len(rawFrontmatter) == 0 {
		rawFrontmatter = []byte("\n")
	}

	var builder strings.Builder
	builder.WriteString(frontmatterDelimiter)
	builder.WriteString("\n")
	builder.Write(rawFrontmatter)
	if !strings.HasSuffix(builder.String(), "\n") {
		builder.WriteString("\n")
	}
	builder.WriteString(frontmatterDelimiter)
	builder.WriteString("\n")
	builder.WriteString(doc.Body)

	return []byte(builder.String()), nil
}

// ReadFrontmatter reads only the frontmatter of a markdown file.
// It reads only the beginning of the file to avoid loading large bodies.
func ReadFrontmatter(path string) (Frontmatter, error) {
	file, err := os.Open(path)
	if err != nil {
		return Frontmatter{}, err
	}
	defer file.Close()

	// Read first 8KB (should be enough for frontmatter)
	buf := make([]byte, 8192)
	n, err := file.Read(buf)
	if err != nil && err != io.EOF {
		return Frontmatter{}, err
	}
	// Convert buffer to string, handling UTF-8 BOM if present at start
	content := string(buf[:n])
	content = strings.TrimPrefix(content, "\uFEFF")

	lines := strings.SplitAfter(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != frontmatterDelimiter {
		return Frontmatter{}, ErrFrontmatterMissing
	}

	var frontmatterLines []string
	foundEnd := false
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == frontmatterDelimiter {
			foundEnd = true
			break
		}
		frontmatterLines = append(frontmatterLines, line)
	}

	if !foundEnd {
		return Frontmatter{}, fmt.Errorf("%w or exceeds 8KB limit", ErrFrontmatterInvalid)
	}

	fmBlock := strings.Join(frontmatterLines, "")
	var fm Frontmatter
	if err := yaml.Unmarshal([]byte(fmBlock), &fm); err != nil {
		return Frontmatter{}, fmt.Errorf("%w: %v", ErrFrontmatterInvalid, err)
	}
	if fm.Extra == nil {
		fm.Extra = map[string]any{}
	}
	return fm, nil
}

// ValidateFrontmatterSchema validates required sync metadata and field formats.
func ValidateFrontmatterSchema(fm Frontmatter) ValidationResult {
	result := ValidationResult{}

	if strings.TrimSpace(fm.ConfluencePageID) == "" {
		result.Issues = append(result.Issues, ValidationIssue{
			Field:   "confluence_page_id",
			Code:    "required",
			Message: "confluence_page_id is required",
		})
	}
	if strings.TrimSpace(fm.ConfluenceSpaceKey) == "" {
		result.Issues = append(result.Issues, ValidationIssue{
			Field:   "confluence_space_key",
			Code:    "required",
			Message: "confluence_space_key is required",
		})
	}
	if fm.ConfluenceVersion <= 0 {
		result.Issues = append(result.Issues, ValidationIssue{
			Field:   "confluence_version",
			Code:    "invalid",
			Message: "confluence_version must be greater than zero",
		})
	}
	if strings.TrimSpace(fm.ConfluenceLastModified) == "" {
		result.Issues = append(result.Issues, ValidationIssue{
			Field:   "confluence_last_modified",
			Code:    "required",
			Message: "confluence_last_modified is required",
		})
	} else if _, err := time.Parse(time.RFC3339, fm.ConfluenceLastModified); err != nil {
		result.Issues = append(result.Issues, ValidationIssue{
			Field:   "confluence_last_modified",
			Code:    "invalid",
			Message: "confluence_last_modified must be RFC3339",
		})
	}

	return result
}

// ValidateImmutableFrontmatter checks immutable keys between previous and current metadata.
func ValidateImmutableFrontmatter(previous, current Frontmatter) ValidationResult {
	result := ValidationResult{}

	if strings.TrimSpace(previous.ConfluencePageID) != strings.TrimSpace(current.ConfluencePageID) {
		result.Issues = append(result.Issues, ValidationIssue{
			Field:   "confluence_page_id",
			Code:    "immutable",
			Message: "confluence_page_id is immutable and cannot be changed manually",
		})
	}
	if strings.TrimSpace(previous.ConfluenceSpaceKey) != strings.TrimSpace(current.ConfluenceSpaceKey) {
		result.Issues = append(result.Issues, ValidationIssue{
			Field:   "confluence_space_key",
			Code:    "immutable",
			Message: "confluence_space_key is immutable and cannot be changed manually",
		})
	}

	return result
}

func splitFrontmatter(content string) (frontmatter string, body string, err error) {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) == 0 {
		return "", "", ErrFrontmatterMissing
	}
	if strings.TrimSpace(lines[0]) != frontmatterDelimiter {
		return "", "", ErrFrontmatterMissing
	}

	var frontmatterLines []string
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == frontmatterDelimiter {
			return strings.Join(frontmatterLines, ""), strings.Join(lines[i+1:], ""), nil
		}
		frontmatterLines = append(frontmatterLines, line)
	}
	return "", "", fmt.Errorf("%w: missing closing delimiter", ErrFrontmatterInvalid)
}
