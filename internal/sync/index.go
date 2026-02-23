package sync

import (
	"os"
	"path/filepath"
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
