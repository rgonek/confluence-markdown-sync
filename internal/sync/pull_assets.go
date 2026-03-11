package sync

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func removeAttachmentsForPage(attachmentIndex map[string]string, pageID string) []string {
	removed := []string{}
	for relPath := range attachmentIndex {
		if !attachmentBelongsToPage(relPath, pageID) {
			continue
		}
		removed = append(removed, normalizeRelPath(relPath))
		delete(attachmentIndex, relPath)
	}
	sort.Strings(removed)
	return removed
}

func removeStaleAttachmentsForPage(
	attachmentIndex map[string]string,
	pageID string,
	currentRefs map[string]attachmentRef,
) []string {
	removed := []string{}
	for relPath, attachmentID := range attachmentIndex {
		if !attachmentBelongsToPage(relPath, pageID) {
			continue
		}
		if _, keep := currentRefs[attachmentID]; keep {
			continue
		}
		removed = append(removed, normalizeRelPath(relPath))
		delete(attachmentIndex, relPath)
	}
	sort.Strings(removed)
	return removed
}

func attachmentBelongsToPage(relPath, pageID string) bool {
	relPath = normalizeRelPath(relPath)
	parts := strings.Split(relPath, "/")
	if len(parts) < 3 {
		return false
	}
	if parts[0] != "assets" {
		return false
	}
	return parts[1] == pageID
}

func collectAttachmentRefs(adfJSON []byte, defaultPageID string) (map[string]attachmentRef, *PullDiagnostic) {
	if len(adfJSON) == 0 {
		return map[string]attachmentRef{}, nil
	}

	var raw any
	if err := json.Unmarshal(adfJSON, &raw); err != nil {
		return map[string]attachmentRef{}, &PullDiagnostic{
			Path:    defaultPageID,
			Code:    "MALFORMED_ADF",
			Message: fmt.Sprintf("failed to parse ADF for page %s: %v", defaultPageID, err),
		}
	}

	out := map[string]attachmentRef{}
	unknownRefSeq := 0
	walkADFNode(raw, func(node map[string]any) {
		nodeType, _ := node["type"].(string)
		if nodeType != "media" && nodeType != "mediaInline" && nodeType != "image" && nodeType != "file" {
			return
		}
		attrs, _ := node["attrs"].(map[string]any)
		if len(attrs) == 0 {
			return
		}

		attachmentID := firstString(attrs,
			"attachmentId",
			"attachmentID",
		)
		renderID := firstString(attrs,
			"id",
			"mediaId",
			"fileId",
			"fileID",
		)
		if attachmentID == "" {
			attachmentID = renderID
		}
		if attachmentID == "" {
			return
		}

		pageID := firstString(attrs, "pageId", "pageID", "contentId")
		if pageID == "" {
			collection := firstString(attrs, "collection")
			if strings.HasPrefix(collection, "contentId-") {
				pageID = strings.TrimPrefix(collection, "contentId-")
			}
		}
		if pageID == "" {
			pageID = defaultPageID
		}

		filename := firstString(attrs, "filename", "fileName", "name", "alt", "title")

		refKey := attachmentID
		if isUnknownMediaID(attachmentID) {
			filenameKey := normalizeAttachmentFilename(filename)
			if filenameKey == "" {
				filenameKey = "attachment"
			}
			refKey = fmt.Sprintf("unknown-media-%s-%d", filenameKey, unknownRefSeq)
			unknownRefSeq++
		}

		out[refKey] = attachmentRef{
			PageID:       pageID,
			AttachmentID: attachmentID,
			RenderID:     renderID,
			Filename:     filename,
		}
	})

	return out, nil
}

func walkADFNode(node any, visit func(map[string]any)) {
	switch typed := node.(type) {
	case map[string]any:
		visit(typed)
		for _, value := range typed {
			walkADFNode(value, visit)
		}
	case []any:
		for _, item := range typed {
			walkADFNode(item, visit)
		}
	}
}

func firstString(attrs map[string]any, keys ...string) string {
	for _, key := range keys {
		raw, exists := attrs[key]
		if !exists {
			continue
		}
		value, ok := raw.(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func isUnknownMediaID(attachmentID string) bool {
	return strings.EqualFold(strings.TrimSpace(attachmentID), "UNKNOWN_MEDIA_ID")
}

func resolveAttachmentRefsByRemoteMetadata(
	refs map[string]attachmentRef,
	remoteAttachments []confluence.Attachment,
) (map[string]attachmentRef, int) {
	if len(refs) == 0 || len(remoteAttachments) == 0 {
		return refs, 0
	}

	attachmentIDByFileID := map[string]confluence.Attachment{}
	for _, attachment := range remoteAttachments {
		fileID := strings.TrimSpace(attachment.FileID)
		if fileID == "" {
			continue
		}
		attachmentIDByFileID[fileID] = attachment
	}

	resolved := 0
	refs = cloneAttachmentRefs(refs)
	for _, key := range sortedStringKeys(refs) {
		ref := refs[key]
		if isUnknownMediaID(ref.AttachmentID) {
			continue
		}

		attachment, ok := attachmentIDByFileID[strings.TrimSpace(ref.AttachmentID)]
		if !ok {
			continue
		}

		resolvedID := strings.TrimSpace(attachment.ID)
		if resolvedID == "" || resolvedID == ref.AttachmentID {
			continue
		}

		delete(refs, key)
		ref.AttachmentID = resolvedID
		if strings.TrimSpace(ref.Filename) == "" || normalizeAttachmentFilename(ref.Filename) == "attachment" {
			ref.Filename = strings.TrimSpace(attachment.Filename)
		}
		refs[resolvedID] = ref
		resolved++
	}

	return refs, resolved
}

func resolveUnknownAttachmentRefsByFilename(
	refs map[string]attachmentRef,
	attachmentIndex map[string]string,
	remoteAttachments []confluence.Attachment,
) (map[string]attachmentRef, int, int, error) {
	if len(refs) == 0 {
		return refs, 0, 0, nil
	}

	resolved := 0
	refs = cloneAttachmentRefs(refs)

	defaultPageID := ""
	for _, ref := range refs {
		defaultPageID = strings.TrimSpace(ref.PageID)
		if defaultPageID != "" {
			break
		}
	}

	localFilenameIndex := buildLocalAttachmentFilenameIndex(attachmentIndex, defaultPageID)
	unresolvedKeys := make([]string, 0)
	for _, key := range sortedStringKeys(refs) {
		ref := refs[key]
		if !isUnknownMediaID(ref.AttachmentID) {
			continue
		}

		if resolvedID, ok := resolveAttachmentIDByFilename(localFilenameIndex, ref.Filename); ok {
			delete(refs, key)
			ref.AttachmentID = resolvedID
			refs[resolvedID] = ref
			resolved++
			continue
		}

		unresolvedKeys = append(unresolvedKeys, key)
	}

	if len(unresolvedKeys) == 0 {
		return refs, resolved, 0, nil
	}

	remoteFilenameIndex := buildRemoteAttachmentFilenameIndex(remoteAttachments)

	unresolved := 0
	for _, key := range unresolvedKeys {
		ref, ok := refs[key]
		if !ok || !isUnknownMediaID(ref.AttachmentID) {
			continue
		}

		resolvedID, ok := resolveAttachmentIDByFilename(remoteFilenameIndex, ref.Filename)
		if !ok {
			unresolved++
			continue
		}

		delete(refs, key)
		ref.AttachmentID = resolvedID
		refs[resolvedID] = ref
		resolved++
	}

	return refs, resolved, unresolved, nil
}

func cloneAttachmentRefs(refs map[string]attachmentRef) map[string]attachmentRef {
	out := make(map[string]attachmentRef, len(refs))
	for key, ref := range refs {
		out[key] = ref
	}
	return out
}

func buildLocalAttachmentFilenameIndex(attachmentIndex map[string]string, pageID string) map[string][]string {
	pageID = strings.TrimSpace(pageID)
	byFilename := map[string][]string{}

	for relPath, attachmentID := range attachmentIndex {
		if strings.TrimSpace(attachmentID) == "" {
			continue
		}
		if pageID != "" && !attachmentBelongsToPage(relPath, pageID) {
			continue
		}

		filename := attachmentFilenameFromAssetPath(relPath, attachmentID)
		filenameKey := normalizeAttachmentFilename(filename)
		if filenameKey == "" {
			continue
		}
		byFilename[filenameKey] = appendUniqueString(byFilename[filenameKey], strings.TrimSpace(attachmentID))
	}

	return byFilename
}

func buildRemoteAttachmentFilenameIndex(attachments []confluence.Attachment) map[string][]string {
	byFilename := map[string][]string{}
	for _, attachment := range attachments {
		attachmentID := strings.TrimSpace(attachment.ID)
		if attachmentID == "" {
			continue
		}

		filenameKey := normalizeAttachmentFilename(attachment.Filename)
		if filenameKey == "" {
			continue
		}
		byFilename[filenameKey] = appendUniqueString(byFilename[filenameKey], attachmentID)
	}
	return byFilename
}

func resolveAttachmentIDByFilename(byFilename map[string][]string, filename string) (string, bool) {
	filenameKey := normalizeAttachmentFilename(filename)
	if filenameKey == "" {
		return "", false
	}

	matches := byFilename[filenameKey]
	if len(matches) != 1 {
		return "", false
	}

	attachmentID := strings.TrimSpace(matches[0])
	if attachmentID == "" {
		return "", false
	}
	return attachmentID, true
}

func attachmentFilenameFromAssetPath(relPath, attachmentID string) string {
	base := filepath.Base(relPath)
	prefix := fs.SanitizePathSegment(strings.TrimSpace(attachmentID))
	if prefix == "" {
		return base
	}
	prefix += "-"
	if strings.HasPrefix(base, prefix) {
		filename := strings.TrimPrefix(base, prefix)
		if strings.TrimSpace(filename) != "" {
			return filename
		}
	}
	return base
}

func normalizeAttachmentFilename(filename string) string {
	filename = strings.TrimSpace(filepath.Base(filename))
	if filename == "" {
		return ""
	}
	filename = fs.SanitizePathSegment(filename)
	if filename == "" {
		return ""
	}
	return strings.ToLower(filename)
}

func appendUniqueString(values []string, candidate string) []string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return values
	}
	for _, existing := range values {
		if existing == candidate {
			return values
		}
	}
	return append(values, candidate)
}

func buildAttachmentPath(ref attachmentRef) string {
	filename := filepath.Base(strings.TrimSpace(ref.Filename))
	filename = fs.SanitizePathSegment(filename)
	if filename == "" {
		filename = "attachment"
	}
	pageID := fs.SanitizePathSegment(ref.PageID)
	if pageID == "" {
		pageID = "unknown-page"
	}

	name := fs.SanitizePathSegment(ref.AttachmentID) + "-" + filename
	return filepath.ToSlash(filepath.Join("assets", pageID, name))
}
