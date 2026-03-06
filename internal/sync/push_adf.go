package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

// ensureADFMediaCollection post-processes the ADF JSON to add required 'collection'
// attributes to 'media' nodes, which is often needed for Confluence v2 API storage conversion.
func ensureADFMediaCollection(adfJSON []byte, pageID string) ([]byte, error) {
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

	modified := walkAndFixMediaNodes(root, pageID)
	if !modified {
		return adfJSON, nil
	}

	out, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("marshal ADF: %w", err)
	}
	return out, nil
}

func walkAndFixMediaNodes(node any, pageID string) bool {
	modified := false
	switch n := node.(type) {
	case map[string]any:
		if nodeType, ok := n["type"].(string); ok && (nodeType == "media" || nodeType == "mediaInline") {
			if attrs, ok := n["attrs"].(map[string]any); ok {
				// If we have an id but no collection, add it
				_, hasID := attrs["id"]
				if !hasID {
					_, hasID = attrs["attachmentId"]
				}
				collection, hasCollection := attrs["collection"].(string)
				if hasID && (!hasCollection || collection == "") {
					attrs["collection"] = "contentId-" + pageID
					modified = true
				}
				if _, hasType := attrs["type"]; !hasType {
					attrs["type"] = "file"
					modified = true
				}
			}
		}
		for _, v := range n {
			if walkAndFixMediaNodes(v, pageID) {
				modified = true
			}
		}
	case []any:
		for _, item := range n {
			if walkAndFixMediaNodes(item, pageID) {
				modified = true
			}
		}
	}
	return modified
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
