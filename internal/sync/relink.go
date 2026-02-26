package sync

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// RelinkResult contains statistics about a relink operation.
type RelinkResult struct {
	FilesSeen      int
	FilesChanged   int
	LinksConverted int
}

type linkDestinationOccurrence struct {
	start int
	end   int
	raw   string
}

// ResolveLinksInFile replaces absolute Confluence URLs in a file with local relative paths.
// If dryRun is true, it only returns whether changes would be made and how many.
func ResolveLinksInFile(path string, index GlobalPageIndex, dryRun bool) (bool, int, error) {
	content, err := os.ReadFile(path) //nolint:gosec // path comes from workspace markdown traversal
	if err != nil {
		return false, 0, err
	}

	replacements := buildRelinkReplacementPlan(content, path, index)
	if len(replacements) == 0 {
		return false, 0, nil
	}

	newContent, linksConverted := applyRelinkDestinationRewrites(content, replacements)
	if linksConverted == 0 {
		return false, 0, nil
	}

	if !dryRun {
		err = os.WriteFile(path, newContent, 0o644) //nolint:gosec // markdown files are intentionally group-readable
		if err != nil {
			return false, 0, err
		}
	}

	return true, linksConverted, nil
}

// ResolveLinksInSpace scans a space directory for absolute links and resolves them.
// If targetPageIDs is non-empty, it only resolves links pointing to those IDs.
// If dryRun is true, it does not modify any files.
func ResolveLinksInSpace(spaceDir string, index GlobalPageIndex, targetPageIDs map[string]struct{}, dryRun bool) (RelinkResult, error) {
	var result RelinkResult

	err := filepath.WalkDir(spaceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		result.FilesSeen++

		// Filter index if targetPageIDs is provided
		var fileIndex GlobalPageIndex
		if len(targetPageIDs) > 0 {
			fileIndex = make(GlobalPageIndex)
			for id, p := range index {
				if _, ok := targetPageIDs[id]; ok {
					fileIndex[id] = p
				}
			}
		} else {
			fileIndex = index
		}

		changed, count, err := ResolveLinksInFile(path, fileIndex, dryRun)
		if err != nil {
			return err
		}

		if changed {
			result.FilesChanged++
			result.LinksConverted += count
		}

		return nil
	})

	return result, err
}

func buildRelinkReplacementPlan(content []byte, sourcePath string, index GlobalPageIndex) map[string]string {
	replacements := map[string]string{}
	doc := goldmark.New().Parser().Parse(text.NewReader(content))

	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		link, ok := node.(*ast.Link)
		if !ok {
			return ast.WalkContinue, nil
		}

		original := canonicalRelinkDestination(string(link.Destination))
		if original == "" {
			return ast.WalkContinue, nil
		}

		replacement, rewrite := resolveRelinkDestination(original, sourcePath, index)
		if !rewrite || replacement == original {
			return ast.WalkContinue, nil
		}

		replacements[original] = replacement
		return ast.WalkContinue, nil
	})

	return replacements
}

func resolveRelinkDestination(rawDestination, sourcePath string, index GlobalPageIndex) (string, bool) {
	normalized := canonicalRelinkDestination(rawDestination)
	if normalized == "" {
		return "", false
	}

	urlOnly := normalized
	anchor := ""
	if idx := strings.Index(normalized, "#"); idx >= 0 {
		urlOnly = normalized[:idx]
		anchor = normalized[idx:]
	}

	pageID := ExtractPageID(urlOnly)
	if pageID == "" {
		return "", false
	}

	targetPath, ok := index[pageID]
	if !ok {
		return "", false
	}

	relPath, err := filepath.Rel(filepath.Dir(sourcePath), targetPath)
	if err != nil {
		return "", false
	}

	encodedRelPath := encodeMarkdownPath(filepath.ToSlash(relPath))
	return encodedRelPath + anchor, true
}

func canonicalRelinkDestination(raw string) string {
	return normalizeMarkdownDestination(raw)
}

func applyRelinkDestinationRewrites(content []byte, replacements map[string]string) ([]byte, int) {
	if len(content) == 0 || len(replacements) == 0 {
		return content, 0
	}

	occurrences := collectLinkDestinationOccurrences(content)
	if len(occurrences) == 0 {
		return content, 0
	}

	var builder strings.Builder
	builder.Grow(len(content))

	last := 0
	conversions := 0
	for _, occurrence := range occurrences {
		key := canonicalRelinkDestination(occurrence.raw)
		replacement, ok := replacements[key]
		if !ok {
			continue
		}

		replacementToken := formatRelinkDestinationToken(occurrence.raw, replacement)
		if replacementToken == occurrence.raw {
			continue
		}

		builder.Write(content[last:occurrence.start])
		builder.WriteString(replacementToken)
		last = occurrence.end
		conversions++
	}

	if conversions == 0 {
		return content, 0
	}

	builder.Write(content[last:])
	return []byte(builder.String()), conversions
}

func formatRelinkDestinationToken(originalToken, replacement string) string {
	token := strings.TrimSpace(originalToken)
	if strings.HasPrefix(token, "<") && strings.HasSuffix(token, ">") {
		return "<" + replacement + ">"
	}
	return replacement
}

func encodeMarkdownPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			continue
		}
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func collectLinkDestinationOccurrences(content []byte) []linkDestinationOccurrence {
	occurrences := make([]linkDestinationOccurrence, 0)

	inFence := false
	var fenceChar byte
	fenceLen := 0
	inlineCodeDelimiterLen := 0
	lineStart := true

	for i := 0; i < len(content); {
		if lineStart {
			if toggled, newFence, newFenceChar, newFenceLen, next := maybeToggleFenceState(content, i, inFence, fenceChar, fenceLen); toggled {
				inFence = newFence
				fenceChar = newFenceChar
				fenceLen = newFenceLen
				i = next
				lineStart = true
				continue
			}
		}

		if inFence {
			if content[i] == '\n' {
				lineStart = true
			} else {
				lineStart = false
			}
			i++
			continue
		}

		if content[i] == '`' {
			run := countRepeatedByte(content, i, '`')
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

		if inlineCodeDelimiterLen > 0 {
			if content[i] == '\n' {
				lineStart = true
			} else {
				lineStart = false
			}
			i++
			continue
		}

		if content[i] == '[' && (i == 0 || content[i-1] != '!') {
			if occurrence, next, ok := parseInlineLinkOccurrence(content, i); ok {
				occurrences = append(occurrences, occurrence)
				i = next
				lineStart = false
				continue
			}
		}

		if content[i] == '\n' {
			lineStart = true
		} else {
			lineStart = false
		}
		i++
	}

	return occurrences
}

func parseInlineLinkOccurrence(content []byte, start int) (linkDestinationOccurrence, int, bool) {
	labelEnd, ok := parseLinkLabelEnd(content, start)
	if !ok {
		return linkDestinationOccurrence{}, start + 1, false
	}

	if labelEnd+1 >= len(content) || content[labelEnd+1] != '(' {
		return linkDestinationOccurrence{}, start + 1, false
	}

	destinationStart, destinationEnd, closeParen, ok := parseLinkDestinationAndCloser(content, labelEnd+1)
	if !ok {
		return linkDestinationOccurrence{}, start + 1, false
	}

	return linkDestinationOccurrence{
		start: destinationStart,
		end:   destinationEnd,
		raw:   string(content[destinationStart:destinationEnd]),
	}, closeParen + 1, true
}

func parseLinkLabelEnd(content []byte, start int) (int, bool) {
	depth := 1
	for i := start + 1; i < len(content); i++ {
		switch content[i] {
		case '\\':
			if i+1 < len(content) {
				i++
			}
		case '`':
			run := countRepeatedByte(content, i, '`')
			if run == 0 {
				continue
			}
			if end := findClosingBacktickRun(content, i+run, run); end >= 0 {
				i = end + run - 1
			} else {
				i += run - 1
			}
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}

	return -1, false
}

func parseLinkDestinationAndCloser(content []byte, openParen int) (int, int, int, bool) {
	i := openParen + 1
	for i < len(content) && (content[i] == ' ' || content[i] == '\t') {
		i++
	}
	if i >= len(content) {
		return 0, 0, 0, false
	}

	destinationStart := i
	destinationEnd := i

	if content[i] == ')' {
		return destinationStart, destinationEnd, i, true
	}

	if content[i] == '<' {
		i++
		for i < len(content) {
			if content[i] == '\\' && i+1 < len(content) {
				i += 2
				continue
			}
			if content[i] == '>' {
				i++
				destinationEnd = i
				break
			}
			if content[i] == '\n' {
				return 0, 0, 0, false
			}
			i++
		}
		if destinationEnd == destinationStart {
			return 0, 0, 0, false
		}
	} else {
		opened := 0
		for i < len(content) {
			switch content[i] {
			case '\\':
				if i+1 < len(content) {
					i += 2
					continue
				}
			case '(':
				opened++
			case ')':
				if opened == 0 {
					destinationEnd = i
					goto parsedDestination
				}
				opened--
			case ' ', '\t':
				destinationEnd = i
				goto parsedDestination
			case '\n':
				return 0, 0, 0, false
			}
			i++
		}
		destinationEnd = i
	}

parsedDestination:
	if destinationEnd < destinationStart {
		return 0, 0, 0, false
	}

	i = destinationEnd
	for i < len(content) && (content[i] == ' ' || content[i] == '\t') {
		i++
	}

	if i < len(content) && content[i] == ')' {
		return destinationStart, destinationEnd, i, true
	}

	if i >= len(content) {
		return 0, 0, 0, false
	}

	titleEnd, ok := parseLinkTitleEnd(content, i)
	if !ok {
		return 0, 0, 0, false
	}

	i = titleEnd
	for i < len(content) && (content[i] == ' ' || content[i] == '\t') {
		i++
	}
	if i >= len(content) || content[i] != ')' {
		return 0, 0, 0, false
	}

	return destinationStart, destinationEnd, i, true
}

func parseLinkTitleEnd(content []byte, start int) (int, bool) {
	if start >= len(content) {
		return 0, false
	}

	opener := content[start]
	closer := opener
	if opener == '(' {
		closer = ')'
	}
	if opener != '"' && opener != '\'' && opener != '(' {
		return 0, false
	}

	for i := start + 1; i < len(content); i++ {
		if content[i] == '\\' && i+1 < len(content) {
			i++
			continue
		}
		if content[i] == closer {
			return i + 1, true
		}
		if content[i] == '\n' {
			return 0, false
		}
	}

	return 0, false
}

func maybeToggleFenceState(content []byte, start int, inFence bool, fenceChar byte, fenceLen int) (bool, bool, byte, int, int) {
	lineStart := start
	indent := 0
	for lineStart < len(content) && indent < 4 && content[lineStart] == ' ' {
		indent++
		lineStart++
	}
	if indent > 3 || lineStart >= len(content) {
		return false, inFence, fenceChar, fenceLen, start
	}

	marker := content[lineStart]
	if marker != '`' && marker != '~' {
		return false, inFence, fenceChar, fenceLen, start
	}

	run := countRepeatedByte(content, lineStart, marker)
	if run < 3 {
		return false, inFence, fenceChar, fenceLen, start
	}

	if !inFence {
		return true, true, marker, run, advanceToNextLine(content, lineStart+run)
	}
	if marker == fenceChar && run >= fenceLen {
		return true, false, 0, 0, advanceToNextLine(content, lineStart+run)
	}

	return false, inFence, fenceChar, fenceLen, start
}

func advanceToNextLine(content []byte, start int) int {
	i := start
	for i < len(content) {
		if content[i] == '\n' {
			return i + 1
		}
		i++
	}
	return len(content)
}

func findClosingBacktickRun(content []byte, start int, run int) int {
	for i := start; i < len(content); i++ {
		if content[i] != '`' {
			continue
		}
		if countRepeatedByte(content, i, '`') == run {
			return i
		}
	}
	return -1
}

func countRepeatedByte(content []byte, start int, b byte) int {
	count := 0
	for i := start; i < len(content) && content[i] == b; i++ {
		count++
	}
	return count
}
