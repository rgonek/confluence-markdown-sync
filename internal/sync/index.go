package sync

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

// PageIndex maps file paths to page IDs.
type PageIndex map[string]string

// GlobalPageIndex maps page IDs to absolute local file paths.
type GlobalPageIndex map[string]string

// BuildPageIndex scans a space directory and returns a map of relative path -> page ID.

func BuildPageIndex(spaceDir string) (PageIndex, error) {
	index := make(PageIndex)
	err := filepath.WalkDir(spaceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "assets" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}

		// Use ReadFrontmatter to be faster
		fm, err := fs.ReadFrontmatter(path)
		if err != nil {
			// Skip files with invalid frontmatter or other errors during indexing
			return nil
		}

		rel, err := filepath.Rel(spaceDir, path)
		if err != nil {
			return nil
		}
		// Normalized path separator to forward slash for consistency in keys
		rel = filepath.ToSlash(rel)

		if fm.ID != "" {
			index[rel] = fm.ID
		}
		return nil
	})
	return index, err
}

// BuildPageIndexWithPending extends BuildPageIndex by assigning deterministic
// placeholder IDs for in-scope markdown files that do not yet have frontmatter id values.
func BuildPageIndexWithPending(spaceDir string, files []string) (PageIndex, error) {
	index, err := BuildPageIndex(spaceDir)
	if err != nil {
		return nil, err
	}
	if err := SeedPendingPageIDsForFiles(spaceDir, index, files); err != nil {
		return nil, err
	}
	return index, nil
}

// SeedPendingPageIDsForFiles fills missing page IDs with deterministic placeholders.
// Existing non-empty IDs are preserved.
func SeedPendingPageIDsForFiles(spaceDir string, index PageIndex, files []string) error {
	if index == nil {
		return nil
	}

	for _, file := range files {
		path := strings.TrimSpace(file)
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(spaceDir, filepath.FromSlash(path))
		}

		fm, err := fs.ReadFrontmatter(path)
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(spaceDir, path)
		if err != nil {
			return err
		}
		relPath = normalizeRelPath(relPath)
		if relPath == "" {
			continue
		}

		if pageID := strings.TrimSpace(fm.ID); pageID != "" {
			index[relPath] = pageID
			continue
		}

		if strings.TrimSpace(index[relPath]) != "" {
			continue
		}
		index[relPath] = pendingPageID(relPath)
	}

	return nil
}

// BuildGlobalPageIndex aggregates paths from all discovered spaces in root.
func BuildGlobalPageIndex(root string) (GlobalPageIndex, error) {
	global := make(GlobalPageIndex)
	states, err := fs.FindAllStateFiles(root)
	if err != nil {
		return nil, err
	}

	for dir, state := range states {
		for relPath, pageID := range state.PagePathIndex {
			if pageID == "" {
				continue
			}
			absPath := filepath.Join(dir, filepath.FromSlash(relPath))
			global[pageID] = absPath
		}
	}
	return global, nil
}

// BuildGlobalPathToPageIDIndex aggregates page IDs keyed by absolute file path.
func BuildGlobalPathToPageIDIndex(root string) (map[string]string, error) {
	globalByID, err := BuildGlobalPageIndex(root)
	if err != nil {
		return nil, err
	}
	return invertGlobalPageIndex(globalByID), nil
}

func invertGlobalPageIndex(index GlobalPageIndex) map[string]string {
	out := make(map[string]string, len(index))
	for pageID, rawPath := range index {
		pageID = strings.TrimSpace(pageID)
		if pageID == "" {
			continue
		}

		pathKey := normalizeAbsolutePathKey(rawPath)
		if pathKey == "" {
			continue
		}
		out[pathKey] = pageID
	}
	return out
}

func normalizeAbsolutePathKey(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	path = filepath.Clean(path)
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}
