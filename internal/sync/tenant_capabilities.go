package sync

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

type tenantFolderMode string

const (
	tenantFolderModeNative       tenantFolderMode = "native"
	tenantFolderModePageFallback tenantFolderMode = "page-fallback"
)

type tenantContentStatusMode string

const (
	tenantContentStatusModeEnabled  tenantContentStatusMode = "enabled"
	tenantContentStatusModeDisabled tenantContentStatusMode = "disabled"
)

type tenantCapabilityCache struct {
	pullFolderMode struct {
		resolved bool
		mode     tenantFolderMode
		diags    []PullDiagnostic
	}
	pullContentStatusMode struct {
		resolved bool
		mode     tenantContentStatusMode
		diags    []PullDiagnostic
	}
	pushContentStatusMode struct {
		resolved bool
		mode     tenantContentStatusMode
		diags    []PushDiagnostic
	}
}

func newTenantCapabilityCache() *tenantCapabilityCache {
	return &tenantCapabilityCache{}
}

func (c *tenantCapabilityCache) detectPullFolderMode(ctx context.Context, remote PullRemote, pages []confluence.Page) (tenantFolderMode, []PullDiagnostic, error) {
	if c.pullFolderMode.resolved {
		return c.pullFolderMode.mode, append([]PullDiagnostic(nil), c.pullFolderMode.diags...), nil
	}

	mode := tenantFolderModeNative
	diags := []PullDiagnostic{}

	for _, page := range pages {
		if !strings.EqualFold(strings.TrimSpace(page.ParentType), "folder") {
			continue
		}
		folderID := strings.TrimSpace(page.ParentPageID)
		if folderID == "" {
			continue
		}
		_, err := remote.GetFolder(ctx, folderID)
		switch {
		case err == nil, errors.Is(err, confluence.ErrNotFound):
		case shouldIgnoreFolderHierarchyError(err):
			mode = tenantFolderModePageFallback
			diags = append(diags, PullDiagnostic{
				Path:    folderLookupUnavailablePath,
				Code:    "FOLDER_LOOKUP_UNAVAILABLE",
				Message: folderLookupUnavailableMessage(err),
			})
		default:
			return "", nil, err
		}
		break
	}

	c.pullFolderMode.resolved = true
	c.pullFolderMode.mode = mode
	c.pullFolderMode.diags = append([]PullDiagnostic(nil), diags...)
	return mode, diags, nil
}

func (c *tenantCapabilityCache) detectPullContentStatusMode(ctx context.Context, remote PullRemote, pages []confluence.Page) (tenantContentStatusMode, []PullDiagnostic) {
	if c.pullContentStatusMode.resolved {
		return c.pullContentStatusMode.mode, append([]PullDiagnostic(nil), c.pullContentStatusMode.diags...)
	}

	mode := tenantContentStatusModeEnabled
	diags := []PullDiagnostic{}
	for _, page := range pages {
		pageID := strings.TrimSpace(page.ID)
		if pageID == "" {
			continue
		}
		if _, err := remote.GetContentStatus(ctx, pageID, page.Status); isCompatibilityProbeError(err) {
			mode = tenantContentStatusModeDisabled
			diags = append(diags, PullDiagnostic{
				Path:    "",
				Code:    "CONTENT_STATUS_COMPATIBILITY_MODE",
				Message: "compatibility mode active: content-status fetch disabled for this pull because the tenant does not support the endpoint",
			})
		}
		break
	}

	c.pullContentStatusMode.resolved = true
	c.pullContentStatusMode.mode = mode
	c.pullContentStatusMode.diags = append([]PullDiagnostic(nil), diags...)
	return mode, diags
}

func (c *tenantCapabilityCache) detectPushContentStatusMode(ctx context.Context, remote PushRemote, spaceDir string, pages []confluence.Page, changes []PushFileChange) (tenantContentStatusMode, error) {
	if c.pushContentStatusMode.resolved {
		return c.pushContentStatusMode.mode, nil
	}

	mode := tenantContentStatusModeEnabled
	if pageID, pageStatus, ok := pushContentStatusProbeTarget(spaceDir, pages, changes); ok {
		if _, err := remote.GetContentStatus(ctx, pageID, pageStatus); err != nil {
			if !isCompatibilityProbeError(err) {
				return "", fmt.Errorf("get content status: %w", err)
			}
			mode = tenantContentStatusModeDisabled
			c.pushContentStatusMode.diags = append(c.pushContentStatusMode.diags, PushDiagnostic{
				Path:    "",
				Code:    "CONTENT_STATUS_COMPATIBILITY_MODE",
				Message: "compatibility mode active: content-status metadata sync disabled for this push because the tenant does not support the endpoint",
			})
		}
	}

	c.pushContentStatusMode.resolved = true
	c.pushContentStatusMode.mode = mode
	return mode, nil
}

func (c *tenantCapabilityCache) pushContentStatusDiagnostics() []PushDiagnostic {
	return append([]PushDiagnostic(nil), c.pushContentStatusMode.diags...)
}

func (c *tenantCapabilityCache) currentPushContentStatusMode() tenantContentStatusMode {
	if c == nil {
		return tenantContentStatusModeEnabled
	}
	if c.pushContentStatusMode.mode == "" {
		return tenantContentStatusModeEnabled
	}
	return c.pushContentStatusMode.mode
}

func (c *tenantCapabilityCache) disablePushContentStatusMode() []PushDiagnostic {
	if c == nil {
		return nil
	}
	if c.pushContentStatusMode.mode != tenantContentStatusModeDisabled {
		c.pushContentStatusMode.mode = tenantContentStatusModeDisabled
	}
	c.pushContentStatusMode.resolved = true
	if len(c.pushContentStatusMode.diags) == 0 {
		c.pushContentStatusMode.diags = append(c.pushContentStatusMode.diags, PushDiagnostic{
			Path:    "",
			Code:    "CONTENT_STATUS_COMPATIBILITY_MODE",
			Message: "compatibility mode active: content-status metadata sync disabled for this push because the tenant does not support the endpoint",
		})
		return []PushDiagnostic{c.pushContentStatusMode.diags[0]}
	}
	return nil
}

func isCompatibilityProbeError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *confluence.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	default:
		return false
	}
}

func pushContentStatusProbeTarget(spaceDir string, pages []confluence.Page, changes []PushFileChange) (string, string, bool) {
	needsContentStatusSync := false
	for _, change := range changes {
		if change.Type != PushChangeAdd && change.Type != PushChangeModify {
			continue
		}
		relPath := normalizeRelPath(change.Path)
		if relPath == "" {
			continue
		}
		frontmatter, err := fs.ReadFrontmatter(filepath.Join(spaceDir, filepath.FromSlash(relPath)))
		if err != nil {
			continue
		}
		pageID := strings.TrimSpace(frontmatter.ID)
		if pageID == "" && strings.TrimSpace(frontmatter.Status) == "" {
			continue
		}
		needsContentStatusSync = true
		if pageID != "" {
			return pageID, normalizePageLifecycleState(frontmatter.State), true
		}
	}
	if !needsContentStatusSync {
		return "", "", false
	}
	for _, page := range pages {
		pageID := strings.TrimSpace(page.ID)
		if pageID == "" {
			continue
		}
		return pageID, normalizePageLifecycleState(page.Status), true
	}
	return "", "", false
}
