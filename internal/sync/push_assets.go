package sync

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func BuildStrictAttachmentIndex(spaceDir, sourcePath, body string, attachmentIndex map[string]string) (map[string]string, []string, error) {
	referencedAssetPaths, err := CollectReferencedAssetPaths(spaceDir, sourcePath, body)
	if err != nil {
		return nil, nil, err
	}

	strictAttachmentIndex := cloneStringMap(attachmentIndex)
	seedPendingAttachmentIDs(strictAttachmentIndex, referencedAssetPaths)
	return strictAttachmentIndex, referencedAssetPaths, nil
}

func seedPendingAttachmentIDs(attachmentIndex map[string]string, assetPaths []string) {
	for _, assetPath := range assetPaths {
		if strings.TrimSpace(attachmentIndex[assetPath]) != "" {
			continue
		}
		attachmentIndex[assetPath] = pendingAttachmentID(assetPath)
	}
}

func pendingAttachmentID(assetPath string) string {
	normalized := strings.TrimSpace(strings.ToLower(filepath.ToSlash(assetPath)))
	normalized = strings.ReplaceAll(normalized, "/", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")
	if normalized == "" {
		normalized = "asset"
	}
	return "pending-attachment-" + normalized
}

func pendingPageID(path string) string {
	normalized := strings.TrimSpace(strings.ToLower(filepath.ToSlash(path)))
	normalized = strings.ReplaceAll(normalized, "/", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")
	if normalized == "" {
		normalized = "page"
	}
	return "pending-page-" + normalized
}

func isPendingPageID(pageID string) bool {
	return strings.HasPrefix(strings.TrimSpace(strings.ToLower(pageID)), "pending-page-")
}

type markdownReferenceKind string

const (
	markdownReferenceKindLink  markdownReferenceKind = "link"
	markdownReferenceKindImage markdownReferenceKind = "image"
)

type markdownDestinationOccurrence struct {
	kind             markdownReferenceKind
	tokenStart       int
	tokenEnd         int
	destinationStart int
	destinationEnd   int
	raw              string
}

type localAssetReference struct {
	Occurrence markdownDestinationOccurrence
	AbsPath    string
	RelPath    string
}

type markdownDestinationRewrite struct {
	Occurrence             markdownDestinationOccurrence
	ReplacementDestination string
	ReplacementToken       string
	AddImagePrefix         bool
}

type assetPathMove struct {
	From string
	To   string
}

func CollectReferencedAssetPaths(spaceDir, sourcePath, body string) ([]string, error) {
	references, err := collectLocalAssetReferences(spaceDir, sourcePath, body)
	if err != nil {
		return nil, err
	}

	paths := map[string]struct{}{}
	for _, reference := range references {
		paths[reference.RelPath] = struct{}{}
	}

	return sortedStringKeys(paths), nil
}

// PrepareMarkdownForAttachmentConversion rewrites local file links ([]()) into
// inline media spans so strict reverse conversion can preserve attachment
// references without dropping inline context.
func PrepareMarkdownForAttachmentConversion(spaceDir, sourcePath, body string, attachmentIndex map[string]string) (string, error) {
	references, err := collectLocalAssetReferences(spaceDir, sourcePath, body)
	if err != nil {
		return "", err
	}

	rewrites := make([]markdownDestinationRewrite, 0)
	for _, reference := range references {
		if reference.Occurrence.kind != markdownReferenceKindLink {
			continue
		}

		attachmentID := strings.TrimSpace(attachmentIndex[reference.RelPath])
		if attachmentID == "" {
			attachmentID = pendingAttachmentID(reference.RelPath)
		}

		displayName := attachmentDisplayNameForPath(reference.RelPath, attachmentID)
		mediaType := mediaTypeForDestination(reference.RelPath)
		rewrites = append(rewrites, markdownDestinationRewrite{
			Occurrence:       reference.Occurrence,
			ReplacementToken: formatPandocInlineMediaToken(displayName, attachmentID, mediaType),
		})
	}

	if len(rewrites) == 0 {
		return body, nil
	}

	return applyMarkdownDestinationRewrites(body, rewrites), nil
}

func attachmentDisplayNameForPath(relPath, attachmentID string) string {
	name := strings.TrimSpace(filepath.Base(relPath))
	if name == "" || name == "." {
		name = "attachment"
	}

	idPrefix := fs.SanitizePathSegment(strings.TrimSpace(attachmentID))
	if idPrefix != "" {
		prefix := idPrefix + "-"
		if strings.HasPrefix(name, prefix) {
			trimmed := strings.TrimSpace(strings.TrimPrefix(name, prefix))
			if trimmed != "" {
				name = trimmed
			}
		}
	}

	return escapeMarkdownSpanText(name)
}

func formatPandocInlineMediaToken(displayName, attachmentID, mediaType string) string {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = "attachment"
	}

	attachmentID = strings.TrimSpace(attachmentID)
	if attachmentID == "" {
		attachmentID = "UNKNOWN_MEDIA_ID"
	}

	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "image" && mediaType != "file" {
		mediaType = "file"
	}

	return fmt.Sprintf(`[%s]{.media-inline media-id="%s" media-type="%s"}`,
		displayName,
		escapePandocAttrValue(attachmentID),
		mediaType,
	)
}

func escapeMarkdownSpanText(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`)
	return replacer.Replace(value)
}

func escapePandocAttrValue(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return replacer.Replace(value)
}

func collectLocalAssetReferences(spaceDir, sourcePath, body string) ([]localAssetReference, error) {
	occurrences := collectMarkdownDestinationOccurrences([]byte(body))
	if len(occurrences) == 0 {
		return nil, nil
	}

	references := make([]localAssetReference, 0, len(occurrences))
	for _, occurrence := range occurrences {
		destination := normalizeMarkdownDestination(occurrence.raw)
		if destination == "" || isExternalDestination(destination) {
			continue
		}

		destination = sanitizeDestinationForLookup(destination)
		if destination == "" {
			continue
		}
		destination = decodeMarkdownPath(destination)

		if occurrence.kind == markdownReferenceKindLink && isMarkdownFilePath(destination) {
			continue
		}

		assetAbsPath := filepath.Clean(filepath.Join(filepath.Dir(sourcePath), filepath.FromSlash(destination)))
		if !isSubpathOrSame(spaceDir, assetAbsPath) {
			return nil, outsideSpaceAssetError(spaceDir, sourcePath, destination)
		}

		info, statErr := os.Stat(assetAbsPath)
		if statErr != nil {
			return nil, fmt.Errorf("asset %s not found", destination)
		}
		if info.IsDir() {
			return nil, fmt.Errorf("asset %s is a directory, expected a file", destination)
		}

		relPath, err := filepath.Rel(spaceDir, assetAbsPath)
		if err != nil {
			return nil, err
		}
		relPath = normalizeRelPath(relPath)
		if relPath == "" || relPath == "." || strings.HasPrefix(relPath, "../") {
			return nil, outsideSpaceAssetError(spaceDir, sourcePath, destination)
		}

		references = append(references, localAssetReference{
			Occurrence: occurrence,
			AbsPath:    assetAbsPath,
			RelPath:    relPath,
		})
	}

	return references, nil
}

func collectMarkdownDestinationOccurrences(content []byte) []markdownDestinationOccurrence {
	occurrences := make([]markdownDestinationOccurrence, 0)

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

		if content[i] == '!' && i+1 < len(content) && content[i+1] == '[' {
			if occurrence, next, ok := parseInlineLinkOccurrence(content, i+1); ok {
				occurrences = append(occurrences, markdownDestinationOccurrence{
					kind:             markdownReferenceKindImage,
					tokenStart:       i + 1,
					tokenEnd:         next,
					destinationStart: occurrence.start,
					destinationEnd:   occurrence.end,
					raw:              occurrence.raw,
				})
				i = next
				lineStart = false
				continue
			}
		}

		if content[i] == '[' && (i == 0 || content[i-1] != '!') {
			if occurrence, next, ok := parseInlineLinkOccurrence(content, i); ok {
				occurrences = append(occurrences, markdownDestinationOccurrence{
					kind:             markdownReferenceKindLink,
					tokenStart:       i,
					tokenEnd:         next,
					destinationStart: occurrence.start,
					destinationEnd:   occurrence.end,
					raw:              occurrence.raw,
				})
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

func applyMarkdownDestinationRewrites(body string, rewrites []markdownDestinationRewrite) string {
	if len(rewrites) == 0 {
		return body
	}

	sort.Slice(rewrites, func(i, j int) bool {
		if rewrites[i].Occurrence.tokenStart == rewrites[j].Occurrence.tokenStart {
			return rewrites[i].Occurrence.destinationStart < rewrites[j].Occurrence.destinationStart
		}
		return rewrites[i].Occurrence.tokenStart < rewrites[j].Occurrence.tokenStart
	})

	content := []byte(body)
	var builder strings.Builder
	builder.Grow(len(content) + len(rewrites))

	last := 0
	for _, rewrite := range rewrites {
		tokenStart := rewrite.Occurrence.tokenStart
		tokenEnd := rewrite.Occurrence.tokenEnd
		destinationStart := rewrite.Occurrence.destinationStart
		destinationEnd := rewrite.Occurrence.destinationEnd

		if tokenStart < last || tokenEnd > len(content) || destinationStart < tokenStart || destinationEnd > tokenEnd || destinationStart > destinationEnd {
			continue
		}

		builder.Write(content[last:tokenStart])
		if strings.TrimSpace(rewrite.ReplacementToken) != "" {
			builder.WriteString(rewrite.ReplacementToken)
			last = tokenEnd
			continue
		}

		if rewrite.AddImagePrefix {
			builder.WriteByte('!')
		}
		builder.Write(content[tokenStart:destinationStart])

		replacementToken := string(content[destinationStart:destinationEnd])
		if strings.TrimSpace(rewrite.ReplacementDestination) != "" {
			replacementToken = formatRelinkDestinationToken(rewrite.Occurrence.raw, rewrite.ReplacementDestination)
		}
		builder.WriteString(replacementToken)
		builder.Write(content[destinationEnd:tokenEnd])

		last = tokenEnd
	}

	builder.Write(content[last:])
	return builder.String()
}

func migrateReferencedAssetsToPageHierarchy(
	spaceDir, sourcePath, pageID, body string,
	attachmentIDByPath map[string]string,
	stateAttachmentIndex map[string]string,
) (string, []string, []assetPathMove, error) {
	pageID = fs.SanitizePathSegment(strings.TrimSpace(pageID))
	if pageID == "" {
		return body, nil, nil, nil
	}

	references, err := collectLocalAssetReferences(spaceDir, sourcePath, body)
	if err != nil {
		return "", nil, nil, err
	}
	if len(references) == 0 {
		return body, nil, nil, nil
	}

	reservedTargets := map[string]string{}
	movesBySource := map[string]string{}
	pathMoves := map[string]string{}
	touchedPaths := map[string]struct{}{}
	rewrites := make([]markdownDestinationRewrite, 0, len(references))

	for _, reference := range references {
		targetAbsPath, targetRelPath, resolveErr := resolvePageAssetTargetPath(spaceDir, pageID, reference.AbsPath, reservedTargets)
		if resolveErr != nil {
			return "", nil, nil, resolveErr
		}

		if targetRelPath == reference.RelPath {
			continue
		}

		touchedPaths[reference.RelPath] = struct{}{}
		touchedPaths[targetRelPath] = struct{}{}
		movesBySource[reference.AbsPath] = targetAbsPath
		pathMoves[reference.RelPath] = targetRelPath

		relativeDestination, relErr := relativeEncodedDestination(sourcePath, targetAbsPath)
		if relErr != nil {
			return "", nil, nil, fmt.Errorf("resolve relative path from %s to %s: %w", sourcePath, targetAbsPath, relErr)
		}

		rewrites = append(rewrites, markdownDestinationRewrite{
			Occurrence:             reference.Occurrence,
			ReplacementDestination: relativeDestination,
		})
	}

	for sourceAbsPath, targetAbsPath := range movesBySource {
		sourceAbsPath = filepath.Clean(sourceAbsPath)
		targetAbsPath = filepath.Clean(targetAbsPath)
		if sourceAbsPath == targetAbsPath {
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetAbsPath), 0o750); err != nil {
			return "", nil, nil, fmt.Errorf("prepare asset directory %s: %w", filepath.Dir(targetAbsPath), err)
		}

		if err := os.Rename(sourceAbsPath, targetAbsPath); err != nil {
			return "", nil, nil, fmt.Errorf("move asset %s to %s: %w", sourceAbsPath, targetAbsPath, err)
		}
	}

	for oldPath, newPath := range pathMoves {
		if err := relocateAttachmentIndexPath(attachmentIDByPath, oldPath, newPath); err != nil {
			return "", nil, nil, err
		}
		if err := relocateAttachmentIndexPath(stateAttachmentIndex, oldPath, newPath); err != nil {
			return "", nil, nil, err
		}
	}

	updatedBody := body
	if len(rewrites) > 0 {
		updatedBody = applyMarkdownDestinationRewrites(body, rewrites)
	}

	moves := make([]assetPathMove, 0, len(pathMoves))
	for _, oldPath := range sortedStringKeys(pathMoves) {
		moves = append(moves, assetPathMove{From: oldPath, To: pathMoves[oldPath]})
	}

	return updatedBody, sortedStringKeys(touchedPaths), moves, nil
}

func resolvePageAssetTargetPath(spaceDir, pageID, sourceAbsPath string, reservedTargets map[string]string) (string, string, error) {
	filename := strings.TrimSpace(filepath.Base(sourceAbsPath))
	if filename == "" || filename == "." {
		filename = "attachment"
	}

	targetDir := filepath.Join(spaceDir, "assets", pageID)
	ext := filepath.Ext(filename)
	stem := strings.TrimSuffix(filename, ext)
	if stem == "" {
		stem = "attachment"
	}

	for index := 1; ; index++ {
		candidateName := filename
		if index > 1 {
			candidateName = stem + "-" + strconv.Itoa(index) + ext
		}

		candidateAbsPath := filepath.Join(targetDir, candidateName)
		candidateRelPath, err := filepath.Rel(spaceDir, candidateAbsPath)
		if err != nil {
			return "", "", err
		}
		candidateRelPath = normalizeRelPath(candidateRelPath)
		if candidateRelPath == "" || strings.HasPrefix(candidateRelPath, "../") {
			return "", "", fmt.Errorf("invalid target asset path %s", candidateAbsPath)
		}

		candidateKey := strings.ToLower(filepath.Clean(candidateAbsPath))
		sourceKey := strings.ToLower(filepath.Clean(sourceAbsPath))
		if reservedSource, exists := reservedTargets[candidateKey]; exists && strings.ToLower(filepath.Clean(reservedSource)) != sourceKey {
			continue
		}

		if strings.EqualFold(filepath.Clean(candidateAbsPath), filepath.Clean(sourceAbsPath)) {
			reservedTargets[candidateKey] = sourceAbsPath
			return candidateAbsPath, candidateRelPath, nil
		}

		if _, statErr := os.Stat(candidateAbsPath); statErr == nil {
			continue
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", "", statErr
		}

		reservedTargets[candidateKey] = sourceAbsPath
		return candidateAbsPath, candidateRelPath, nil
	}
}

func relativeEncodedDestination(sourcePath, targetAbsPath string) (string, error) {
	relPath, err := filepath.Rel(filepath.Dir(sourcePath), targetAbsPath)
	if err != nil {
		return "", err
	}
	return encodeMarkdownPath(filepath.ToSlash(relPath)), nil
}

func relocateAttachmentIndexPath(index map[string]string, oldRelPath, newRelPath string) error {
	if index == nil {
		return nil
	}

	oldRelPath = normalizeRelPath(oldRelPath)
	newRelPath = normalizeRelPath(newRelPath)
	if oldRelPath == "" || newRelPath == "" || oldRelPath == newRelPath {
		return nil
	}

	oldID := strings.TrimSpace(index[oldRelPath])
	if oldID == "" {
		return nil
	}

	if existingID := strings.TrimSpace(index[newRelPath]); existingID != "" && existingID != oldID {
		return fmt.Errorf("cannot remap attachment path %s to %s: destination is already mapped to %s", oldRelPath, newRelPath, existingID)
	}

	index[newRelPath] = oldID
	delete(index, oldRelPath)
	return nil
}

func sanitizeDestinationForLookup(destination string) string {
	if idx := strings.Index(destination, "#"); idx >= 0 {
		destination = destination[:idx]
	}
	if idx := strings.Index(destination, "?"); idx >= 0 {
		destination = destination[:idx]
	}
	return strings.TrimSpace(destination)
}

func isMarkdownFilePath(destination string) bool {
	return strings.EqualFold(filepath.Ext(strings.TrimSpace(destination)), ".md")
}

func outsideSpaceAssetError(spaceDir, sourcePath, destination string) error {
	filename := strings.TrimSpace(filepath.Base(destination))
	if filename == "" || filename == "." {
		filename = "file"
	}

	targetAbsPath := filepath.Join(spaceDir, "assets", filename)
	suggestedDestination, err := relativeEncodedDestination(sourcePath, targetAbsPath)
	if err != nil {
		suggestedDestination = filepath.ToSlash(filepath.Join("assets", filename))
	}

	spaceAssetsPath := filepath.ToSlash(filepath.Join(filepath.Base(spaceDir), "assets")) + "/"
	return fmt.Errorf(
		"asset %q is outside the space directory. move it into %q and update the link to %q",
		filename,
		spaceAssetsPath,
		suggestedDestination,
	)
}

func normalizeMarkdownDestination(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if strings.HasPrefix(raw, "<") {
		if end := strings.Index(raw, ">"); end > 0 {
			raw = raw[1:end]
		}
	}

	raw = strings.TrimSpace(raw)
	if idx := strings.IndexAny(raw, " \t"); idx >= 0 {
		raw = raw[:idx]
	}

	raw = strings.Trim(raw, "\"'")
	return strings.TrimSpace(raw)
}

func isExternalDestination(destination string) bool {
	lower := strings.ToLower(strings.TrimSpace(destination))
	if lower == "" {
		return true
	}
	if strings.HasPrefix(lower, "#") {
		return true
	}
	for _, prefix := range []string{"http://", "https://", "mailto:", "tel:", "data:", "//"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func collectPageAttachmentPaths(index map[string]string, pageID string) []string {
	paths := make([]string, 0)
	for relPath := range index {
		if attachmentBelongsToPage(relPath, pageID) {
			paths = append(paths, normalizeRelPath(relPath))
		}
	}
	sort.Strings(paths)
	return paths
}

func dedupeSortedPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, path := range paths {
		path = normalizeRelPath(path)
		if path == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		normalized = append(normalized, path)
	}
	sort.Strings(normalized)
	return normalized
}

func resolveLocalTitle(doc fs.MarkdownDocument, relPath string) string {
	title := strings.TrimSpace(doc.Frontmatter.Title)
	if title != "" {
		return title
	}

	for _, line := range strings.Split(doc.Body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
			if title != "" {
				return title
			}
		}
	}

	base := filepath.Base(relPath)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func buildLocalPageTitleIndex(spaceDir string) (map[string]string, error) {
	out := map[string]string{}
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

		relPath, err := filepath.Rel(spaceDir, path)
		if err != nil {
			return nil
		}
		relPath = normalizeRelPath(relPath)
		if relPath == "" {
			return nil
		}

		doc, err := fs.ReadMarkdownDocument(path)
		if err != nil {
			return nil
		}

		title := strings.TrimSpace(resolveLocalTitle(doc, relPath))
		if title == "" {
			return nil
		}
		out[relPath] = title
		return nil
	})
	return out, err
}

func findTrackedTitleConflict(relPath, title string, pagePathIndex map[string]string, pageTitleByPath map[string]string) (string, string) {
	titleKey := strings.ToLower(strings.TrimSpace(title))
	if titleKey == "" {
		return "", ""
	}

	normalizedPath := normalizeRelPath(relPath)
	currentDir := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(normalizedPath))))

	for trackedPath, trackedPageID := range pagePathIndex {
		trackedPath = normalizeRelPath(trackedPath)
		trackedPageID = strings.TrimSpace(trackedPageID)
		if trackedPath == "" || trackedPageID == "" {
			continue
		}
		if trackedPath == normalizedPath {
			continue
		}

		trackedTitle := strings.ToLower(strings.TrimSpace(pageTitleByPath[trackedPath]))
		if trackedTitle == "" || trackedTitle != titleKey {
			continue
		}

		trackedDir := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(trackedPath))))
		if trackedDir != currentDir {
			continue
		}

		return trackedPath, trackedPageID
	}

	return "", ""
}

func detectAssetContentType(filename string, raw []byte) string {
	extType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename)))
	if strings.TrimSpace(extType) != "" {
		return extType
	}

	if len(raw) == 0 {
		return "application/octet-stream"
	}
	sniffLen := len(raw)
	if sniffLen > 512 {
		sniffLen = 512
	}
	return http.DetectContentType(raw[:sniffLen])
}

func normalizePageLifecycleState(state string) string {
	normalized := strings.TrimSpace(strings.ToLower(state))
	if normalized == "" {
		return "current"
	}
	return normalized
}

func listAllPushPages(ctx context.Context, remote PushRemote, opts confluence.PageListOptions) ([]confluence.Page, error) {
	result := []confluence.Page{}
	cursor := opts.Cursor
	for {
		opts.Cursor = cursor
		pageResult, err := remote.ListPages(ctx, opts)
		if err != nil {
			return nil, err
		}
		result = append(result, pageResult.Pages...)
		if strings.TrimSpace(pageResult.NextCursor) == "" || pageResult.NextCursor == cursor {
			break
		}
		cursor = pageResult.NextCursor
	}
	return result, nil
}
