package converter

import (
	"regexp"
	"strings"
)

var escapedInlineMarkdownLinkPattern = regexp.MustCompile(`\\\[((?:\\.|[^\\\]\n])+?)\\\]\\\(((?:\\.|[^\\\n])+?)\\\)`)

func normalizeForwardMarkdown(markdown string) string {
	if !strings.Contains(markdown, `\[`) || !strings.Contains(markdown, `\]`) || !strings.Contains(markdown, `\(`) {
		return markdown
	}

	return escapedInlineMarkdownLinkPattern.ReplaceAllStringFunc(markdown, func(match string) string {
		parts := escapedInlineMarkdownLinkPattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}

		label := strings.TrimSpace(unescapeMarkdownEscapes(parts[1]))
		destination := strings.TrimSpace(unescapeMarkdownEscapes(parts[2]))
		if label == "" || destination == "" {
			return match
		}
		if !isLikelyMarkdownLinkDestination(destination) {
			return match
		}

		return "[" + label + "](" + destination + ")"
	})
}

func unescapeMarkdownEscapes(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}

	var out strings.Builder
	out.Grow(len(value))
	escaped := false
	for _, r := range value {
		if escaped {
			out.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		out.WriteRune(r)
	}
	if escaped {
		out.WriteRune('\\')
	}
	return out.String()
}

func isLikelyMarkdownLinkDestination(destination string) bool {
	lower := strings.ToLower(strings.TrimSpace(destination))
	if lower == "" {
		return false
	}

	for _, prefix := range []string{"http://", "https://", "mailto:", "tel:", "#", "./", "../", "/", "assets/", "wiki/"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}

	if strings.Contains(lower, "pageid=") {
		return true
	}

	if strings.HasSuffix(lower, ".md") || strings.Contains(lower, ".md#") || strings.Contains(lower, ".md?") {
		return true
	}

	return false
}
