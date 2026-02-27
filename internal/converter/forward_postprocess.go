package converter

import (
	"regexp"
	"strings"
)

var escapedInlineMarkdownLinkPattern = regexp.MustCompile(`\\\[((?:\\.|[^\\\]\n])+?)\\\]\\\(((?:\\.|[^\\\n])+?)\\\)`)

func normalizeForwardMarkdown(markdown string) string {
	if !strings.Contains(markdown, `\[`) || !strings.Contains(markdown, `\]`) || !strings.Contains(markdown, `\(`) {
		return normalizeEscapedParentheses(markdown)
	}

	normalized := escapedInlineMarkdownLinkPattern.ReplaceAllStringFunc(markdown, func(match string) string {
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

	return normalizeEscapedParentheses(normalized)
}

func normalizeEscapedParentheses(markdown string) string {
	if !strings.Contains(markdown, `\(`) && !strings.Contains(markdown, `\)`) {
		return markdown
	}

	var out strings.Builder
	out.Grow(len(markdown))

	lineStart := true
	inlineCodeDelimiterLen := 0
	inFence := false
	var fenceChar byte
	fenceLen := 0
	linkDestinationDepth := 0

	for i := 0; i < len(markdown); {
		if lineStart && inlineCodeDelimiterLen == 0 {
			if toggled, nextInFence, nextFenceChar, nextFenceLen, nextIndex := maybeToggleMarkdownFence(markdown, i, inFence, fenceChar, fenceLen); toggled {
				out.WriteString(markdown[i:nextIndex])
				inFence = nextInFence
				fenceChar = nextFenceChar
				fenceLen = nextFenceLen
				i = nextIndex
				lineStart = true
				continue
			}
		}

		if inFence {
			ch := markdown[i]
			out.WriteByte(ch)
			lineStart = ch == '\n'
			i++
			continue
		}

		if markdown[i] == '`' {
			run := countRepeatedByte(markdown, i, '`')
			out.WriteString(markdown[i : i+run])
			switch inlineCodeDelimiterLen {
			case 0:
				inlineCodeDelimiterLen = run
			case run:
				inlineCodeDelimiterLen = 0
			}
			i += run
			lineStart = false
			continue
		}

		if inlineCodeDelimiterLen == 0 {
			if linkDestinationDepth == 0 && markdown[i] == '(' && i > 0 && markdown[i-1] == ']' {
				linkDestinationDepth = 1
				out.WriteByte(markdown[i])
				i++
				lineStart = false
				continue
			}

			if linkDestinationDepth > 0 {
				if markdown[i] == '\\' && i+1 < len(markdown) && (markdown[i+1] == '(' || markdown[i+1] == ')') {
					out.WriteByte(markdown[i])
					out.WriteByte(markdown[i+1])
					i += 2
					lineStart = false
					continue
				}

				switch markdown[i] {
				case '(':
					linkDestinationDepth++
				case ')':
					linkDestinationDepth--
				}

				out.WriteByte(markdown[i])
				i++
				lineStart = false
				continue
			}

			if markdown[i] == '\\' && i+1 < len(markdown) && (markdown[i+1] == '(' || markdown[i+1] == ')') {
				out.WriteByte(markdown[i+1])
				i += 2
				lineStart = false
				continue
			}
		}

		ch := markdown[i]
		out.WriteByte(ch)
		lineStart = ch == '\n'
		i++
	}

	return out.String()
}

func maybeToggleMarkdownFence(markdown string, start int, inFence bool, fenceChar byte, fenceLen int) (bool, bool, byte, int, int) {
	i := start
	for i < len(markdown) && (markdown[i] == ' ' || markdown[i] == '\t') {
		i++
	}
	if i >= len(markdown) {
		return false, inFence, fenceChar, fenceLen, start
	}

	marker := markdown[i]
	if marker != '`' && marker != '~' {
		return false, inFence, fenceChar, fenceLen, start
	}

	run := countRepeatedByte(markdown, i, marker)
	if run < 3 {
		return false, inFence, fenceChar, fenceLen, start
	}

	if !inFence {
		return true, true, marker, run, i + run
	}

	if marker == fenceChar && run >= fenceLen {
		return true, false, 0, 0, i + run
	}

	return false, inFence, fenceChar, fenceLen, start
}

func countRepeatedByte(input string, start int, marker byte) int {
	count := 0
	for i := start; i < len(input); i++ {
		if input[i] != marker {
			break
		}
		count++
	}
	return count
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
