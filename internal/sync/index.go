package sync

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

// PageIndex maps file paths to page IDs.
type PageIndex map[string]string

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
