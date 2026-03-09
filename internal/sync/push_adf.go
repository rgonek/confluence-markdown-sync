package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

// ensureADFMediaCollection post-processes media nodes with the collection and
// attachment metadata Confluence needs to preserve uploaded attachments.
func ensureADFMediaCollection(adfJSON []byte, pageID string, refsByPath map[string]publishedAttachmentRef) ([]byte, error) {
	if len(adfJSON) == 0 {
		return adfJSON, nil
	}
	if strings.TrimSpace(pageID) == "" {
		return adfJSON, nil
	}

	var root any
	if err := json.Unmarshal(adfJSON, &root); err != nil {
		return nil, fmt.Errorf("unmarshal ADF: %w", err)
	}

	refByID := map[string]publishedAttachmentRef{}
	for _, ref := range refsByPath {
		if mediaID := strings.TrimSpace(ref.MediaID); mediaID != "" {
			refByID[mediaID] = ref
		}
		if attachmentID := strings.TrimSpace(ref.AttachmentID); attachmentID != "" {
			refByID[attachmentID] = ref
		}
	}

	modified := walkAndFixMediaNodes(root, pageID, refByID)
	if !modified {
		return adfJSON, nil
	}

	out, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("marshal ADF: %w", err)
	}
	return out, nil
}

func walkAndFixMediaNodes(node any, pageID string, refByID map[string]publishedAttachmentRef) bool {
	modified := false
	switch n := node.(type) {
	case map[string]any:
		if nodeType, ok := n["type"].(string); ok && (nodeType == "media" || nodeType == "mediaInline") {
			if attrs, ok := n["attrs"].(map[string]any); ok {
				ref := lookupPublishedAttachmentRef(attrs, refByID)
				resolvedPageID := strings.TrimSpace(pageID)
				if ref.PageID != "" {
					resolvedPageID = strings.TrimSpace(ref.PageID)
				}
				if ref.MediaID != "" && strings.TrimSpace(stringValue(attrs["id"])) != ref.MediaID {
					attrs["id"] = ref.MediaID
					modified = true
				}
				if ref.AttachmentID != "" && strings.TrimSpace(stringValue(attrs["attachmentId"])) != ref.AttachmentID {
					attrs["attachmentId"] = ref.AttachmentID
					modified = true
				}
				if ref.Filename != "" && strings.TrimSpace(stringValue(attrs["fileName"])) == "" {
					attrs["fileName"] = ref.Filename
					modified = true
				}
				if strings.TrimSpace(ref.PageID) != "" && strings.TrimSpace(stringValue(attrs["pageId"])) == "" {
					attrs["pageId"] = strings.TrimSpace(ref.PageID)
					modified = true
				}

				_, hasID := attrs["id"]
				if !hasID {
					_, hasID = attrs["attachmentId"]
				}
				collection, hasCollection := attrs["collection"].(string)
				if hasID && resolvedPageID != "" && (!hasCollection || collection == "") {
					attrs["collection"] = "contentId-" + resolvedPageID
					modified = true
				}
				if _, hasType := attrs["type"]; !hasType {
					mediaType := strings.TrimSpace(ref.MediaType)
					if mediaType == "" {
						mediaType = "file"
					}
					attrs["type"] = mediaType
					modified = true
				}
			}
		}
		for _, v := range n {
			if walkAndFixMediaNodes(v, pageID, refByID) {
				modified = true
			}
		}
	case []any:
		for _, item := range n {
			if walkAndFixMediaNodes(item, pageID, refByID) {
				modified = true
			}
		}
	}
	return modified
}

func lookupPublishedAttachmentRef(attrs map[string]any, refByID map[string]publishedAttachmentRef) publishedAttachmentRef {
	if len(refByID) == 0 {
		return publishedAttachmentRef{}
	}
	for _, candidate := range []string{
		strings.TrimSpace(stringValue(attrs["id"])),
		strings.TrimSpace(stringValue(attrs["attachmentId"])),
		strings.TrimSpace(stringValue(attrs["fileId"])),
	} {
		if candidate == "" {
			continue
		}
		if ref, ok := refByID[candidate]; ok {
			return ref
		}
	}
	return publishedAttachmentRef{}
}

func syncPageMetadata(ctx context.Context, remote PushRemote, pageID string, doc fs.MarkdownDocument, existingPage bool, capabilities *tenantCapabilityCache, diagnostics *[]PushDiagnostic) error {
	// 1. Sync Content Status
	targetStatus := strings.TrimSpace(doc.Frontmatter.Status)
	pageStatus := normalizePageLifecycleState(doc.Frontmatter.State)
	contentStatusMode := capabilities.currentPushContentStatusMode()
	if contentStatusMode != tenantContentStatusModeDisabled && shouldSyncContentStatus(existingPage, doc) {
		currentStatus, err := remote.GetContentStatus(ctx, pageID, pageStatus)
		if err != nil {
			if !isCompatibilityProbeError(err) {
				return fmt.Errorf("get content status: %w", err)
			}
			for _, diag := range capabilities.disablePushContentStatusMode() {
				appendPushDiagnostic(diagnostics, diag.Path, diag.Code, diag.Message)
			}
		} else if targetStatus != currentStatus {
			if targetStatus == "" {
				if err := remote.DeleteContentStatus(ctx, pageID, pageStatus); err != nil {
					if !isCompatibilityProbeError(err) {
						return fmt.Errorf("delete content status: %w", err)
					}
					for _, diag := range capabilities.disablePushContentStatusMode() {
						appendPushDiagnostic(diagnostics, diag.Path, diag.Code, diag.Message)
					}
				}
			} else {
				if err := remote.SetContentStatus(ctx, pageID, pageStatus, targetStatus); err != nil {
					if !isCompatibilityProbeError(err) {
						return fmt.Errorf("set content status: %w", err)
					}
					for _, diag := range capabilities.disablePushContentStatusMode() {
						appendPushDiagnostic(diagnostics, diag.Path, diag.Code, diag.Message)
					}
				}
			}
		}
	}

	// 2. Sync Labels
	remoteLabels, err := remote.GetLabels(ctx, pageID)
	if err != nil {
		return fmt.Errorf("get labels: %w", err)
	}

	remoteLabelSet := map[string]struct{}{}
	for _, l := range fs.NormalizeLabels(remoteLabels) {
		remoteLabelSet[l] = struct{}{}
	}

	localLabelSet := map[string]struct{}{}
	for _, l := range fs.NormalizeLabels(doc.Frontmatter.Labels) {
		localLabelSet[l] = struct{}{}
	}

	var toAdd []string
	for l := range localLabelSet {
		if _, ok := remoteLabelSet[l]; !ok {
			toAdd = append(toAdd, l)
		}
	}

	for l := range remoteLabelSet {
		if _, ok := localLabelSet[l]; !ok {
			if err := remote.RemoveLabel(ctx, pageID, l); err != nil {
				return fmt.Errorf("remove label %q: %w", l, err)
			}
		}
	}

	sort.Strings(toAdd)

	if len(toAdd) > 0 {
		if err := remote.AddLabels(ctx, pageID, toAdd); err != nil {
			return fmt.Errorf("add labels: %w", err)
		}
	}

	return nil
}

func shouldSyncContentStatus(existingPage bool, doc fs.MarkdownDocument) bool {
	return existingPage || strings.TrimSpace(doc.Frontmatter.Status) != ""
}
