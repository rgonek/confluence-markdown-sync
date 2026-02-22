package fs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	frontmatterDelimiter = "---"
)

var (
	// ImmutableFrontmatterKeys contains keys that cannot be changed manually.
	ImmutableFrontmatterKeys = []string{
		"id",
		"space",
	}

	// MutableBySyncFrontmatterKeys contains keys that are managed by sync operations.
	MutableBySyncFrontmatterKeys = []string{
		"version",
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
	Title   string
	ID      string
	Space   string
	Version int

	// Legacy metadata retained in-memory only for transitional behavior.
	ConfluenceLastModified string `yaml:"-"`
	ConfluenceParentPageID string `yaml:"-"`

	Extra map[string]any
}

type frontmatterYAML struct {
	Title   string `yaml:"title,omitempty"`
	ID      string `yaml:"id,omitempty"`
	Space   string `yaml:"space,omitempty"`
	Version int    `yaml:"version,omitempty"`

	LegacyPageID       string `yaml:"confluence_page_id,omitempty"`
	LegacySpaceKey     string `yaml:"confluence_space_key,omitempty"`
	LegacyVersion      int    `yaml:"confluence_version,omitempty"`
	LegacyLastModified string `yaml:"confluence_last_modified,omitempty"`
	LegacyParentPageID string `yaml:"confluence_parent_page_id,omitempty"`

	Extra map[string]any `yaml:",inline"`
}

func (fm Frontmatter) MarshalYAML() (any, error) {
	extra := map[string]any{}
	for key, value := range fm.Extra {
		switch key {
		case "title", "id", "space", "version",
			"confluence_page_id", "confluence_space_key", "confluence_version",
			"confluence_last_modified", "confluence_parent_page_id":
			continue
		default:
			extra[key] = value
		}
	}

	return frontmatterYAML{
		Title:   fm.Title,
		ID:      fm.ID,
		Space:   fm.Space,
		Version: fm.Version,
		Extra:   extra,
	}, nil
}

func (fm *Frontmatter) UnmarshalYAML(value *yaml.Node) error {
	var decoded frontmatterYAML
	if err := value.Decode(&decoded); err != nil {
		return err
	}

	fm.Title = strings.TrimSpace(decoded.Title)
	fm.ID = strings.TrimSpace(decoded.ID)
	fm.Space = strings.TrimSpace(decoded.Space)
	fm.Version = decoded.Version

	if fm.ID == "" {
		fm.ID = strings.TrimSpace(decoded.LegacyPageID)
	}
	if fm.Space == "" {
		fm.Space = strings.TrimSpace(decoded.LegacySpaceKey)
	}
	if fm.Version == 0 {
		fm.Version = decoded.LegacyVersion
	}

	fm.ConfluenceLastModified = strings.TrimSpace(decoded.LegacyLastModified)
	fm.ConfluenceParentPageID = strings.TrimSpace(decoded.LegacyParentPageID)

	if decoded.Extra == nil {
		fm.Extra = map[string]any{}
		return nil
	}

	delete(decoded.Extra, "title")
	delete(decoded.Extra, "id")
	delete(decoded.Extra, "space")
	delete(decoded.Extra, "version")
	delete(decoded.Extra, "confluence_page_id")
	delete(decoded.Extra, "confluence_space_key")
	delete(decoded.Extra, "confluence_version")
	delete(decoded.Extra, "confluence_last_modified")
	delete(decoded.Extra, "confluence_parent_page_id")
	fm.Extra = decoded.Extra
	return nil
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

	// id is optional for new pages but must be valid if present
	// space is always required
	if strings.TrimSpace(fm.Space) == "" {
		result.Issues = append(result.Issues, ValidationIssue{
			Field:   "space",
			Code:    "required",
			Message: "space is required",
		})
	}

	if strings.TrimSpace(fm.ID) != "" {
		if fm.Version <= 0 {
			result.Issues = append(result.Issues, ValidationIssue{
				Field:   "version",
				Code:    "invalid",
				Message: "version must be greater than zero for existing pages",
			})
		}
	}

	return result
}

// ValidateImmutableFrontmatter checks immutable keys between previous and current metadata.
func ValidateImmutableFrontmatter(previous, current Frontmatter) ValidationResult {
	result := ValidationResult{}

	if strings.TrimSpace(previous.ID) != strings.TrimSpace(current.ID) {
		result.Issues = append(result.Issues, ValidationIssue{
			Field:   "id",
			Code:    "immutable",
			Message: "id is immutable and cannot be changed manually",
		})
	}
	if strings.TrimSpace(previous.Space) != strings.TrimSpace(current.Space) {
		result.Issues = append(result.Issues, ValidationIssue{
			Field:   "space",
			Code:    "immutable",
			Message: "space is immutable and cannot be changed manually",
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
