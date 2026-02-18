package fs

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	invalidPathChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)
	separatorRun     = regexp.MustCompile(`[\s-]+`)
)

// SanitizePathSegment converts arbitrary text into a safe single path segment.
func SanitizePathSegment(v string) string {
	s := strings.TrimSpace(v)
	s = invalidPathChars.ReplaceAllString(s, "-")
	s = strings.Trim(s, ". ")
	s = separatorRun.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	if s == "" {
		s = "untitled"
	}
	if isWindowsReservedName(s) {
		s += "-item"
	}
	return s
}

// SanitizeMarkdownFilename sanitizes a page title and enforces a .md suffix.
func SanitizeMarkdownFilename(title string) string {
	name := SanitizePathSegment(title)
	if !strings.HasSuffix(strings.ToLower(name), ".md") {
		name += ".md"
	}
	return name
}

// JoinSanitizedPath joins multiple sanitized path segments.
func JoinSanitizedPath(segments ...string) string {
	clean := make([]string, 0, len(segments))
	for _, segment := range segments {
		clean = append(clean, SanitizePathSegment(segment))
	}
	return filepath.Join(clean...)
}

func isWindowsReservedName(v string) bool {
	base := strings.ToUpper(strings.TrimSpace(v))
	if idx := strings.Index(base, "."); idx >= 0 {
		base = base[:idx]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	default:
		return false
	}
}

