package sync

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// FindOrphanAssets returns asset files in assets/ that are not referenced by
// any markdown file in the same space directory.
func FindOrphanAssets(spaceDir string) ([]string, error) {
	referenced := map[string]struct{}{}

	err := filepath.WalkDir(spaceDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == "assets" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		doc, err := fs.ReadMarkdownDocument(path)
		if err != nil {
			return nil
		}

		paths, err := collectReferencedAssetPathsFromMarkdown(spaceDir, path, doc.Body)
		if err != nil {
			return err
		}
		for _, relPath := range paths {
			referenced[relPath] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	assetsRoot := filepath.Join(spaceDir, "assets")
	if _, err := os.Stat(assetsRoot); os.IsNotExist(err) {
		return nil, nil
	}

	orphans := make([]string, 0)
	err = filepath.WalkDir(assetsRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(spaceDir, path)
		if err != nil {
			return err
		}
		relPath = normalizeRelPath(relPath)
		if relPath == "" {
			return nil
		}
		if _, ok := referenced[relPath]; ok {
			return nil
		}

		orphans = append(orphans, relPath)
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(orphans)
	return orphans, nil
}

func collectReferencedAssetPathsFromMarkdown(spaceDir, sourcePath, markdown string) ([]string, error) {
	doc := goldmark.New().Parser().Parse(text.NewReader([]byte(markdown)))
	paths := map[string]struct{}{}

	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		rawDestination := ""
		switch n := node.(type) {
		case *ast.Link:
			rawDestination = string(n.Destination)
		case *ast.Image:
			rawDestination = string(n.Destination)
		default:
			return ast.WalkContinue, nil
		}

		destination := normalizeMarkdownDestination(rawDestination)
		if destination == "" || isExternalDestination(destination) {
			return ast.WalkContinue, nil
		}
		destination = decodeMarkdownPath(destination)

		if idx := strings.Index(destination, "#"); idx >= 0 {
			destination = destination[:idx]
		}
		if idx := strings.Index(destination, "?"); idx >= 0 {
			destination = destination[:idx]
		}
		if destination == "" {
			return ast.WalkContinue, nil
		}

		assetAbsPath := filepath.Clean(filepath.Join(filepath.Dir(sourcePath), filepath.FromSlash(destination)))
		if !isSubpathOrSame(spaceDir, assetAbsPath) {
			return ast.WalkContinue, nil
		}

		relPath, err := filepath.Rel(spaceDir, assetAbsPath)
		if err != nil {
			return ast.WalkContinue, nil
		}
		relPath = normalizeRelPath(relPath)
		if relPath == "" || !strings.HasPrefix(relPath, "assets/") {
			return ast.WalkContinue, nil
		}

		paths[relPath] = struct{}{}
		return ast.WalkContinue, nil
	})

	return sortedStringKeys(paths), nil
}
