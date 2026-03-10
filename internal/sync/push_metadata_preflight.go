package sync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func buildPushContentStateCatalog(
	ctx context.Context,
	remote PushRemote,
	spaceKey string,
	spaceDir string,
	changes []PushFileChange,
	pageIDByPath PageIndex,
) (pushContentStateCatalog, error) {
	catalog := pushContentStateCatalog{
		space:            map[string]confluence.ContentState{},
		global:           map[string]confluence.ContentState{},
		perPage:          map[string]map[string]confluence.ContentState{},
		perPageAvailable: map[string]bool{},
	}

	if !pushChangesNeedContentStatus(spaceDir, changes) {
		return catalog, nil
	}

	if states, err := remote.ListContentStates(ctx); err == nil {
		catalog.globalAvailable = true
		for _, state := range states {
			catalog.global[strings.ToLower(strings.TrimSpace(state.Name))] = state
		}
	} else if !isCompatibilityProbeError(err) {
		return pushContentStateCatalog{}, fmt.Errorf("list content states: %w", err)
	}

	if states, err := remote.ListSpaceContentStates(ctx, spaceKey); err == nil {
		catalog.spaceAvailable = true
		for _, state := range states {
			catalog.space[strings.ToLower(strings.TrimSpace(state.Name))] = state
		}
	} else if !isCompatibilityProbeError(err) {
		return pushContentStateCatalog{}, fmt.Errorf("list space content states: %w", err)
	}

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
			return pushContentStateCatalog{}, fmt.Errorf("read frontmatter %s: %w", relPath, err)
		}
		pageID := strings.TrimSpace(frontmatter.ID)
		if pageID == "" {
			pageID = strings.TrimSpace(pageIDByPath[relPath])
		}
		if pageID == "" || isPendingPageID(pageID) {
			continue
		}
		if _, exists := catalog.perPage[pageID]; exists {
			continue
		}

		states, err := remote.GetAvailableContentStates(ctx, pageID)
		if err != nil {
			if errorsIsNotFoundOrCompatibility(err) {
				continue
			}
			return pushContentStateCatalog{}, fmt.Errorf("list available content states for page %s: %w", pageID, err)
		}
		catalog.perPageAvailable[pageID] = true
		stateMap := map[string]confluence.ContentState{}
		for _, state := range states {
			stateMap[strings.ToLower(strings.TrimSpace(state.Name))] = state
		}
		catalog.perPage[pageID] = stateMap
	}

	return catalog, nil
}

func validatePushContentStatuses(spaceKey string, spaceDir string, changes []PushFileChange, pageIDByPath PageIndex, catalog pushContentStateCatalog) error {
	unresolved := make([]string, 0)
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
			return fmt.Errorf("read frontmatter %s: %w", relPath, err)
		}
		statusName := strings.TrimSpace(frontmatter.Status)
		if statusName == "" {
			continue
		}

		pageID := strings.TrimSpace(frontmatter.ID)
		if pageID == "" {
			pageID = strings.TrimSpace(pageIDByPath[relPath])
		}
		if _, ok := resolvePushContentStateInput(statusName, pageID, catalog); ok {
			continue
		}
		if !catalog.hasUsableStatusCatalog(pageID) {
			continue
		}

		unresolved = append(unresolved, fmt.Sprintf("%s (%q)", relPath, statusName))
	}

	if len(unresolved) == 0 {
		return nil
	}

	sort.Strings(unresolved)
	return fmt.Errorf(
		"content status preflight failed in space %s: unknown or unavailable status values for %s; verify the status exists in Confluence before retrying",
		strings.TrimSpace(spaceKey),
		strings.Join(unresolved, ", "),
	)
}

func resolvePushContentStateInput(statusName, pageID string, catalog pushContentStateCatalog) (confluence.ContentState, bool) {
	key := strings.ToLower(strings.TrimSpace(statusName))
	if key == "" {
		return confluence.ContentState{}, false
	}

	pageID = strings.TrimSpace(pageID)
	if pageID != "" {
		if perPage := catalog.perPage[pageID]; len(perPage) > 0 {
			if state, ok := perPage[key]; ok {
				return state, true
			}
		}
	}
	if state, ok := catalog.space[key]; ok {
		return state, true
	}
	if state, ok := catalog.global[key]; ok {
		return state, true
	}
	return confluence.ContentState{}, false
}

func resolvePushContentStateUpdateInput(statusName, pageID string, catalog pushContentStateCatalog) (confluence.ContentState, bool) {
	stateName := strings.TrimSpace(statusName)
	if stateName == "" {
		return confluence.ContentState{}, false
	}
	if state, ok := resolvePushContentStateInput(stateName, pageID, catalog); ok {
		return state, true
	}
	if !catalog.hasUsableStatusCatalog(pageID) {
		return confluence.ContentState{Name: stateName}, true
	}
	return confluence.ContentState{}, false
}

func (c pushContentStateCatalog) hasUsableStatusCatalog(pageID string) bool {
	pageID = strings.TrimSpace(pageID)
	if pageID != "" && c.perPageAvailable[pageID] {
		return true
	}
	return c.spaceAvailable || c.globalAvailable
}

func pushChangesNeedContentStatus(spaceDir string, changes []PushFileChange) bool {
	for _, change := range changes {
		if change.Type != PushChangeAdd && change.Type != PushChangeModify {
			continue
		}
		frontmatter, err := fs.ReadFrontmatter(filepath.Join(spaceDir, filepath.FromSlash(normalizeRelPath(change.Path))))
		if err != nil {
			continue
		}
		if strings.TrimSpace(frontmatter.Status) != "" {
			return true
		}
	}
	return false
}

func errorsIsNotFoundOrCompatibility(err error) bool {
	return err == nil || errors.Is(err, confluence.ErrNotFound) || isCompatibilityProbeError(err)
}
