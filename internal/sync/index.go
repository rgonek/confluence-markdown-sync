package sync

import (
	"fmt"
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

// ResolveGlobalIndexRoot returns the repository/worktree root to use for
// cross-space page indexing. If no git root can be discovered, it falls back to
// the parent of the provided space directory so sibling spaces remain visible.
func ResolveGlobalIndexRoot(spaceDir string) (string, error) {
	spaceDir = strings.TrimSpace(spaceDir)
	if spaceDir == "" {
		return "", fmt.Errorf("space directory is required")
	}

	absPath, err := filepath.Abs(spaceDir)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(absPath); statErr == nil && !info.IsDir() {
		absPath = filepath.Dir(absPath)
	}

	fallbackRoot := filepath.Dir(absPath)
	for current := absPath; ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(current, ".git")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}

	return fallbackRoot, nil
}

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
	root = strings.TrimSpace(root)
	if root == "" {
		return GlobalPageIndex{}, nil
	}

	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	global := make(GlobalPageIndex)
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "assets" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}

		fm, err := fs.ReadFrontmatter(path)
		if err != nil {
			return nil
		}

		pageID := strings.TrimSpace(fm.ID)
		if pageID == "" {
			return nil
		}

		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil
		}
		global[pageID] = absPath
		return nil
	})
	if err != nil {
		return nil, err
	}

	states, err := fs.FindAllStateFiles(root)
	if err != nil {
		return nil, err
	}

	for dir, state := range states {
		for relPath, pageID := range state.PagePathIndex {
			pageID = strings.TrimSpace(pageID)
			if pageID == "" {
				continue
			}
			if _, exists := global[pageID]; exists {
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

	path = trimWindowsDevicePrefix(path)
	path = filepath.Clean(path)
	path = filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func trimWindowsDevicePrefix(path string) string {
	if runtime.GOOS != "windows" {
		return path
	}

	switch {
	case strings.HasPrefix(path, `\\?\UNC\`):
		return `\\` + strings.TrimPrefix(path, `\\?\UNC\`)
	case strings.HasPrefix(path, `\\?\`):
		return strings.TrimPrefix(path, `\\?\`)
	default:
		return path
	}
}
