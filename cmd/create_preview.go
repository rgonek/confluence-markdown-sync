package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

type localCreatePreview struct {
	RelPath             string
	Title               string
	ResolvedParent      string
	CanonicalTargetPath string
	AttachmentUploads   []string
	ADFBytes            int
	ADFTopLevelNodes    int
}

func buildLocalCreatePreview(
	ctx context.Context,
	spaceDir string,
	relPath string,
	domain string,
	attachmentIndex map[string]string,
	globalIndex syncflow.GlobalPageIndex,
) (localCreatePreview, error) {
	relPath = normalizeRepoRelPath(relPath)
	if relPath == "" {
		return localCreatePreview{}, fmt.Errorf("relative path is required")
	}

	absPath := filepath.Join(spaceDir, filepath.FromSlash(relPath))
	doc, err := fs.ReadMarkdownDocument(absPath)
	if err != nil {
		return localCreatePreview{}, err
	}

	title := strings.TrimSpace(doc.Frontmatter.Title)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
	}

	pageIndex, err := syncflow.BuildPageIndex(spaceDir)
	if err != nil {
		return localCreatePreview{}, err
	}
	if err := syncflow.SeedPendingPageIDsForFiles(spaceDir, pageIndex, []string{absPath}); err != nil {
		return localCreatePreview{}, err
	}

	referencedAssets, err := syncflow.CollectReferencedAssetPaths(spaceDir, absPath, doc.Body)
	if err != nil {
		return localCreatePreview{}, err
	}

	strictAttachmentIndex, _, err := syncflow.BuildStrictAttachmentIndex(spaceDir, absPath, doc.Body, attachmentIndex)
	if err != nil {
		return localCreatePreview{}, err
	}
	preparedBody, err := syncflow.PrepareMarkdownForAttachmentConversion(spaceDir, absPath, doc.Body, strictAttachmentIndex)
	if err != nil {
		return localCreatePreview{}, err
	}
	linkHook := syncflow.NewReverseLinkHookWithGlobalIndex(spaceDir, pageIndex, globalIndex, domain)
	mediaHook := syncflow.NewReverseMediaHook(spaceDir, strictAttachmentIndex)
	reverseResult, err := converter.Reverse(ctx, []byte(preparedBody), converter.ReverseConfig{
		LinkHook:  linkHook,
		MediaHook: mediaHook,
		Strict:    true,
	}, absPath)
	if err != nil {
		return localCreatePreview{}, err
	}

	return localCreatePreview{
		RelPath:             relPath,
		Title:               title,
		ResolvedParent:      resolvePreviewParent(relPath, doc.Frontmatter.ConfluenceParentPageID, pageIndex),
		CanonicalTargetPath: canonicalCreatePreviewPath(relPath, title),
		AttachmentUploads:   referencedAssets,
		ADFBytes:            len(reverseResult.ADF),
		ADFTopLevelNodes:    adfTopLevelNodeCount(reverseResult.ADF),
	}, nil
}

func resolvePreviewParent(relPath, fallbackParentID string, pageIndex syncflow.PageIndex) string {
	dirPath := normalizeRepoRelPath(filepath.Dir(relPath))
	if dirPath == "" || dirPath == "." {
		if parentID := strings.TrimSpace(fallbackParentID); parentID != "" {
			return "page " + parentID
		}
		return "space root"
	}

	for currentDir := dirPath; currentDir != "" && currentDir != "."; {
		indexPath := previewIndexPathForDir(currentDir)
		if indexPath != "" && normalizeRepoRelPath(indexPath) != normalizeRepoRelPath(relPath) {
			if pageID := strings.TrimSpace(pageIndex[indexPath]); pageID != "" {
				return fmt.Sprintf("page %s (%s)", pageID, indexPath)
			}
		}

		nextDir := normalizeRepoRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(currentDir))))
		if nextDir == currentDir {
			break
		}
		currentDir = nextDir
	}

	if parentID := strings.TrimSpace(fallbackParentID); parentID != "" {
		return "page " + parentID
	}
	return "space root"
}

func previewIndexPathForDir(dirPath string) string {
	dirPath = normalizeRepoRelPath(dirPath)
	if dirPath == "" || dirPath == "." {
		return ""
	}
	dirBase := strings.TrimSpace(filepath.Base(filepath.FromSlash(dirPath)))
	if dirBase == "" || dirBase == "." {
		return ""
	}
	return normalizeRepoRelPath(filepath.ToSlash(filepath.Join(dirPath, dirBase+".md")))
}

func canonicalCreatePreviewPath(relPath, title string) string {
	dirPath := normalizeRepoRelPath(filepath.Dir(relPath))
	fileName := fs.SanitizeMarkdownFilename(title)
	if dirPath == "" || dirPath == "." {
		return fileName
	}
	return normalizeRepoRelPath(filepath.ToSlash(filepath.Join(dirPath, fileName)))
}

func adfTopLevelNodeCount(adf []byte) int {
	var parsed struct {
		Content []json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(adf, &parsed); err != nil {
		return 0
	}
	return len(parsed.Content)
}
