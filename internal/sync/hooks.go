package sync

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	adfconv "github.com/rgonek/jira-adf-converter/converter"
	mdconv "github.com/rgonek/jira-adf-converter/mdconverter"
)

// NewForwardLinkHook creates a link hook for ADF -> Markdown conversion.
// It resolves Confluence page IDs to relative Markdown paths.
// sourcePath is the absolute path of the file being generated.
// pagePathByID maps page IDs to absolute or relative-to-root paths of target files.
func NewForwardLinkHook(sourcePath string, pagePathByID map[string]string, currentSpaceKey string) adfconv.LinkRenderHook {
	return func(ctx context.Context, in adfconv.LinkRenderInput) (adfconv.LinkRenderOutput, error) {
		pageID := in.Meta.PageID
		if pageID == "" {
			pageID = ExtractPageID(in.Href)
		}

		// If page ID is present, try to resolve to local path
		if pageID != "" {
			targetPath, ok := pagePathByID[pageID]
			if ok {
				// Calculate relative path from source file to target file
				// We assume both paths are either absolute or relative to the same base.
				// For safety, let's assume they are absolute or relative to CWD/Root.
				// If sourcePath is absolute, targetPath should be absolute.

				// Ensure directory of sourcePath
				sourceDir := filepath.Dir(sourcePath)
				relPath, err := filepath.Rel(sourceDir, targetPath)
				if err == nil {
					// Handle anchor
					href := filepath.ToSlash(relPath)
					if in.Meta.Anchor != "" {
						href += "#" + in.Meta.Anchor
					}

					return adfconv.LinkRenderOutput{
						Href:    href,
						Title:   in.Title,
						Handled: true,
					}, nil
				}
			}

			if in.Meta.SpaceKey == "" || strings.EqualFold(in.Meta.SpaceKey, currentSpaceKey) {
				return adfconv.LinkRenderOutput{}, adfconv.ErrUnresolved
			}
		}
		// If not resolved, return unhandled (library default behavior)
		return adfconv.LinkRenderOutput{Handled: false}, nil
	}
}

// ExtractPageID parses a Confluence URL to extract the page ID.
func ExtractPageID(href string) string {
	if href == "" {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}

	// 1. ?pageId=123
	if id := u.Query().Get("pageId"); id != "" {
		return id
	}

	// 2. /pages/123/Title or /wiki/pages/123
	// 3. /spaces/KEY/pages/123/Title or /wiki/spaces/KEY/pages/123
	segments := strings.Split(u.Path, "/")
	for i, seg := range segments {
		if seg == "pages" && i+1 < len(segments) {
			id := segments[i+1]
			if _, err := strconv.Atoi(id); err == nil {
				return id
			}
		}
	}

	return ""
}

// NewForwardMediaHook creates a media hook for ADF -> Markdown conversion.
// It resolves attachment IDs to local asset paths.
func NewForwardMediaHook(sourcePath string, attachmentPathByID map[string]string) adfconv.MediaRenderHook {
	pageAssetPaths := buildPageAssetPathIndex(attachmentPathByID)

	return func(ctx context.Context, in adfconv.MediaRenderInput) (adfconv.MediaRenderOutput, error) {
		targetPath := ""
		if mappedPath, ok := resolveAttachmentPathByID(in, attachmentPathByID); ok {
			targetPath = mappedPath
		} else if fallbackPath := resolveForwardMediaFallbackPath(in, attachmentPathByID, pageAssetPaths); fallbackPath != "" {
			targetPath = fallbackPath
		}

		if targetPath != "" {
			sourceDir := filepath.Dir(sourcePath)
			relPath, err := filepath.Rel(sourceDir, targetPath)
			if err == nil {
				return adfconv.MediaRenderOutput{
					Markdown: fmt.Sprintf("![%s](%s)", in.Alt, filepath.ToSlash(relPath)),
					Handled:  true,
				}, nil
			}
		}

		if strings.TrimSpace(in.Meta.AttachmentID) != "" || strings.TrimSpace(in.ID) != "" {
			return adfconv.MediaRenderOutput{}, adfconv.ErrUnresolved
		}

		return adfconv.MediaRenderOutput{Handled: false}, nil
	}
}

func resolveAttachmentPathByID(in adfconv.MediaRenderInput, attachmentPathByID map[string]string) (string, bool) {
	attachmentID := strings.TrimSpace(in.Meta.AttachmentID)
	if attachmentID != "" {
		targetPath, ok := attachmentPathByID[attachmentID]
		if ok && strings.TrimSpace(targetPath) != "" {
			return targetPath, true
		}
	}

	mediaID := strings.TrimSpace(in.ID)
	if mediaID != "" {
		targetPath, ok := attachmentPathByID[mediaID]
		if ok && strings.TrimSpace(targetPath) != "" {
			return targetPath, true
		}
	}

	return "", false
}

func resolveForwardMediaFallbackPath(
	in adfconv.MediaRenderInput,
	attachmentPathByID map[string]string,
	pageAssetPaths map[string][]string,
) string {
	pageID := resolveMediaPageID(in)
	filename := fs.SanitizePathSegment(strings.TrimSpace(in.Meta.Filename))

	if pageID != "" {
		paths := pageAssetPaths[pageID]
		if len(paths) > 0 {
			if filename != "" {
				matches := make([]string, 0, len(paths))
				for _, candidate := range paths {
					if mediaPathMatchesFilename(candidate, filename) {
						matches = append(matches, candidate)
					}
				}
				if len(matches) == 1 {
					return matches[0]
				}
			}

			if len(paths) == 1 {
				return paths[0]
			}
		}
	}

	if filename == "" {
		return ""
	}

	globalMatches := make([]string, 0)
	for _, path := range attachmentPathByID {
		if mediaPathMatchesFilename(path, filename) {
			globalMatches = append(globalMatches, path)
		}
	}

	if len(globalMatches) == 1 {
		return globalMatches[0]
	}

	return ""
}

func resolveMediaPageID(in adfconv.MediaRenderInput) string {
	if pageID := fs.SanitizePathSegment(strings.TrimSpace(in.Meta.PageID)); pageID != "" {
		return pageID
	}

	collection := strings.TrimSpace(stringValue(in.Attrs["collection"]))
	if strings.HasPrefix(collection, "contentId-") {
		if pageID := fs.SanitizePathSegment(strings.TrimPrefix(collection, "contentId-")); pageID != "" {
			return pageID
		}
	}

	for _, key := range []string{"pageId", "pageID", "contentId"} {
		if pageID := fs.SanitizePathSegment(strings.TrimSpace(stringValue(in.Attrs[key]))); pageID != "" {
			return pageID
		}
	}

	return ""
}

func mediaPathMatchesFilename(path, filename string) bool {
	base := strings.ToLower(filepath.Base(path))
	filename = strings.ToLower(strings.TrimSpace(filename))
	if filename == "" {
		return false
	}
	if base == filename {
		return true
	}
	return strings.HasSuffix(base, "-"+filename)
}

func buildPageAssetPathIndex(attachmentPathByID map[string]string) map[string][]string {
	out := map[string][]string{}
	for _, rawPath := range attachmentPathByID {
		normalized := normalizeRelPath(rawPath)
		if normalized == "" {
			continue
		}
		parts := strings.Split(normalized, "/")
		if len(parts) < 3 || parts[0] != "assets" {
			continue
		}
		pageID := fs.SanitizePathSegment(strings.TrimSpace(parts[1]))
		if pageID == "" {
			continue
		}
		out[pageID] = append(out[pageID], rawPath)
	}
	return out
}

func stringValue(v any) string {
	if raw, ok := v.(string); ok {
		return raw
	}
	return ""
}

// NewReverseLinkHook creates a link hook for Markdown -> ADF conversion.
// It resolves relative links to Confluence page IDs using the provided index.
func NewReverseLinkHook(spaceDir string, index PageIndex, domain string) mdconv.LinkParseHook {
	return NewReverseLinkHookWithGlobalIndex(spaceDir, index, nil, domain)
}

// NewReverseLinkHookWithGlobalIndex resolves links using a local space index and,
// when needed, a global page index keyed by page ID.
func NewReverseLinkHookWithGlobalIndex(spaceDir string, index PageIndex, globalIndex GlobalPageIndex, domain string) mdconv.LinkParseHook {
	globalPathIndex := invertGlobalPageIndex(globalIndex)

	return func(ctx context.Context, in mdconv.LinkParseInput) (mdconv.LinkParseOutput, error) {
		// If absolute URL, let it pass (Handled=false)
		if strings.HasPrefix(in.Destination, "http://") || strings.HasPrefix(in.Destination, "https://") {
			return mdconv.LinkParseOutput{Handled: false}, nil
		}

		// Handle relative links
		// in.SourcePath is absolute path to the file being converted.
		// in.Destination is the link target.

		// If destination is anchor only, let it pass?
		if strings.HasPrefix(in.Destination, "#") {
			return mdconv.LinkParseOutput{Handled: false}, nil
		}

		destination := in.Destination
		anchor := ""
		if idx := strings.Index(destination, "#"); idx >= 0 {
			anchor = destination[idx+1:]
			destination = destination[:idx]
		}
		destination = strings.TrimSpace(destination)
		if destination == "" {
			return mdconv.LinkParseOutput{Handled: false}, nil
		}
		destination = decodeMarkdownPath(destination)

		// Resolve path relative to source file
		dir := filepath.Dir(in.SourcePath)
		destPath := filepath.Join(dir, destination)

		// Calculate relative path from space root to look up in index
		relPath, err := filepath.Rel(spaceDir, destPath)
		if err != nil {
			return mdconv.LinkParseOutput{}, mdconv.ErrUnresolved
		}
		relPath = filepath.ToSlash(relPath)
		targetPath := relPath

		// Look up in local space index first.
		pageID, ok := index[targetPath]
		if !ok {
			pageID, ok = globalPathIndex[normalizeAbsolutePathKey(destPath)]
			if !ok {
				pageID, ok = resolveGlobalPageIDBySameFile(destPath, globalIndex)
				if !ok {
					return mdconv.LinkParseOutput{}, mdconv.ErrUnresolved
				}
			}
		}

		// Construct Confluence URL
		// We use the viewpage.action URL format which is standard for ID-based links
		dest := strings.TrimRight(domain, "/") + "/wiki/pages/viewpage.action?pageId=" + pageID
		if strings.TrimSpace(anchor) != "" {
			dest += "#" + anchor
		}

		return mdconv.LinkParseOutput{
			Destination: dest,
			Handled:     true,
		}, nil
	}
}

func decodeMarkdownPath(path string) string {
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return path
	}
	return decoded
}

func resolveGlobalPageIDBySameFile(destPath string, globalIndex GlobalPageIndex) (string, bool) {
	destPath = strings.TrimSpace(destPath)
	if destPath == "" || len(globalIndex) == 0 {
		return "", false
	}

	destInfo, err := os.Stat(destPath)
	if err != nil {
		return "", false
	}
	destKey := normalizeAbsolutePathKey(destPath)

	for pageID, candidatePath := range globalIndex {
		if strings.TrimSpace(pageID) == "" {
			continue
		}

		candidateKey := normalizeAbsolutePathKey(candidatePath)
		if candidateKey == "" || candidateKey == destKey {
			continue
		}

		candidateInfo, err := os.Stat(candidatePath)
		if err != nil {
			continue
		}
		if os.SameFile(destInfo, candidateInfo) {
			return pageID, true
		}
	}

	return "", false
}

// NewReverseMediaHook creates a media hook for Markdown -> ADF conversion.
// It resolves local asset paths to Confluence attachment IDs/URLs.
func NewReverseMediaHook(spaceDir string, attachmentIndex map[string]string) mdconv.MediaParseHook {
	return func(ctx context.Context, in mdconv.MediaParseInput) (mdconv.MediaParseOutput, error) {
		destination := normalizeMarkdownDestination(in.Destination)
		if destination == "" || isExternalDestination(destination) {
			return mdconv.MediaParseOutput{}, mdconv.ErrUnresolved
		}
		if idx := strings.Index(destination, "#"); idx >= 0 {
			destination = destination[:idx]
		}
		if idx := strings.Index(destination, "?"); idx >= 0 {
			destination = destination[:idx]
		}
		if destination == "" {
			return mdconv.MediaParseOutput{}, mdconv.ErrUnresolved
		}

		dir := filepath.Dir(in.SourcePath)
		assetPath := filepath.Clean(filepath.Join(dir, filepath.FromSlash(destination)))
		if !isSubpathOrSame(spaceDir, assetPath) {
			return mdconv.MediaParseOutput{}, mdconv.ErrUnresolved
		}

		if _, err := os.Stat(assetPath); os.IsNotExist(err) {
			return mdconv.MediaParseOutput{}, mdconv.ErrUnresolved
		}

		relPath, err := filepath.Rel(spaceDir, assetPath)
		if err != nil {
			return mdconv.MediaParseOutput{}, mdconv.ErrUnresolved
		}
		relPath = normalizeRelPath(relPath)
		if relPath == "" || relPath == "." || strings.HasPrefix(relPath, "../") {
			return mdconv.MediaParseOutput{}, mdconv.ErrUnresolved
		}

		id, ok := attachmentIndex[relPath]
		if !ok || strings.TrimSpace(id) == "" {
			return mdconv.MediaParseOutput{}, mdconv.ErrUnresolved
		}

		return mdconv.MediaParseOutput{
			MediaType: mediaTypeForDestination(destination),
			ID:        id,
			Handled:   true,
			Alt:       in.Alt,
		}, nil
	}
}

func mediaTypeForDestination(destination string) string {
	ext := strings.ToLower(filepath.Ext(destination))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".bmp", ".ico", ".tif", ".tiff", ".avif", ".apng":
		return "image"
	default:
		return "file"
	}
}
