package sync

import (
	"path/filepath"
	"sort"
	"strings"
)

// FolderTitleConflict describes directory-backed folders that would collide on
// Confluence's space-wide folder-title uniqueness constraint.
type FolderTitleConflict struct {
	Title string
	Paths []string
}

// DetectFolderTitleConflicts returns duplicate pure-folder titles within the
// provided validation scope. A "pure folder" is a directory segment that does
// not already have a page-backed index file at <Dir>/<Dir>.md.
func DetectFolderTitleConflicts(spaceDir string, files []string) []FolderTitleConflict {
	markdownPaths := map[string]struct{}{}
	for _, file := range files {
		path := strings.TrimSpace(file)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(spaceDir, filepath.FromSlash(path))
		}
		relPath, err := filepath.Rel(spaceDir, path)
		if err != nil {
			continue
		}
		normalized := normalizeRelPath(relPath)
		if normalized == "" {
			continue
		}
		markdownPaths[normalized] = struct{}{}
	}

	pathsByTitle := map[string]map[string]struct{}{}
	for relPath := range markdownPaths {
		currentDir := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(relPath))))
		for currentDir != "" && currentDir != "." {
			if indexPath := indexPagePathForDir(currentDir); indexPath != "" {
				if _, exists := markdownPaths[indexPath]; exists {
					currentDir = normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(currentDir))))
					continue
				}
			}

			title := strings.TrimSpace(filepath.Base(filepath.FromSlash(currentDir)))
			if title != "" && title != "." {
				if pathsByTitle[title] == nil {
					pathsByTitle[title] = map[string]struct{}{}
				}
				pathsByTitle[title][currentDir] = struct{}{}
			}

			nextDir := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(currentDir))))
			if nextDir == currentDir {
				break
			}
			currentDir = nextDir
		}
	}

	conflicts := make([]FolderTitleConflict, 0)
	for title, pathSet := range pathsByTitle {
		if len(pathSet) <= 1 {
			continue
		}
		paths := sortedStringKeys(pathSet)
		conflicts = append(conflicts, FolderTitleConflict{
			Title: title,
			Paths: paths,
		})
	}

	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Title == conflicts[j].Title {
			return strings.Join(conflicts[i].Paths, "|") < strings.Join(conflicts[j].Paths, "|")
		}
		return conflicts[i].Title < conflicts[j].Title
	})
	return conflicts
}
