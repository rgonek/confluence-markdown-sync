package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Markdown link regex: [text](url)
// We use a simplified regex that should capture most standard Markdown links.
var linkRegex = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^\)]+)\)`)

// RelinkResult contains statistics about a relink operation.
type RelinkResult struct {
	FilesSeen      int
	FilesChanged   int
	LinksConverted int
}

// ResolveLinksInFile replaces absolute Confluence URLs in a file with local relative paths.
// If dryRun is true, it only returns whether changes would be made and how many.
func ResolveLinksInFile(path string, index GlobalPageIndex, dryRun bool) (bool, int, error) {
	content, err := os.ReadFile(path) //nolint:gosec // path comes from workspace markdown traversal
	if err != nil {
		return false, 0, err
	}

	changed := false
	linksConverted := 0
	newContent := linkRegex.ReplaceAllStringFunc(string(content), func(match string) string {
		submatches := linkRegex.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}

		text := submatches[1]
		rawURL := submatches[2]

		// Extract anchor if present
		urlOnly := rawURL
		anchor := ""
		if idx := strings.Index(rawURL, "#"); idx >= 0 {
			urlOnly = rawURL[:idx]
			anchor = rawURL[idx:]
		}

		pageID := ExtractPageID(urlOnly)
		if pageID == "" {
			return match
		}

		targetPath, ok := index[pageID]
		if !ok {
			return match
		}

		// Calculate relative path
		relPath, err := filepath.Rel(filepath.Dir(path), targetPath)
		if err != nil {
			return match
		}

		newURL := filepath.ToSlash(relPath) + anchor
		changed = true
		linksConverted++
		return fmt.Sprintf("[%s](%s)", text, newURL)
	})

	if changed && !dryRun {
		err = os.WriteFile(path, []byte(newContent), 0644) //nolint:gosec // markdown files are intentionally group-readable
		if err != nil {
			return false, 0, err
		}
	}

	return changed, linksConverted, nil
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
