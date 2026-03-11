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

// PlanPagePaths builds deterministic canonical markdown paths for remote pages.
//
// It always recomputes the canonical pull path from the current remote
// hierarchy, then allocates unique sanitized filenames if needed.
func PlanPagePaths(
	spaceDir string,
	previousPageIndex map[string]string,
	pages []confluence.Page,
	folderByID map[string]confluence.Folder,
) (map[string]string, map[string]string) {
	pageByID := map[string]confluence.Page{}
	hasChildren := map[string]bool{}
	for _, page := range pages {
		pageID := strings.TrimSpace(page.ID)
		pageByID[pageID] = page
		parentType := strings.ToLower(strings.TrimSpace(page.ParentType))
		if parentType == "" || parentType == "page" {
			parentID := strings.TrimSpace(page.ParentPageID)
			if parentID != "" {
				hasChildren[parentID] = true
			}
		}
	}
	if folderByID == nil {
		folderByID = map[string]confluence.Folder{}
	}
	for _, folder := range folderByID {
		if strings.EqualFold(strings.TrimSpace(folder.ParentType), "page") {
			parentID := strings.TrimSpace(folder.ParentID)
			if parentID != "" {
				hasChildren[parentID] = true
			}
		}
	}
	absByID := map[string]string{}
	relByID := map[string]string{}
	usedRelPaths := map[string]struct{}{}

	type pagePathPlan struct {
		ID          string
		BaseRelPath string
	}
	plans := make([]pagePathPlan, 0, len(pages))
	for _, page := range pages {
		baseRelPath := plannedPageRelPath(page, pageByID, folderByID, hasChildren)

		plans = append(plans, pagePathPlan{
			ID:          page.ID,
			BaseRelPath: baseRelPath,
		})
	}

	sort.Slice(plans, func(i, j int) bool {
		if plans[i].BaseRelPath == plans[j].BaseRelPath {
			return plans[i].ID < plans[j].ID
		}
		return plans[i].BaseRelPath < plans[j].BaseRelPath
	})

	for _, plan := range plans {
		relPath := ensureUniqueMarkdownPath(plan.BaseRelPath, usedRelPaths)
		usedRelPaths[relPath] = struct{}{}
		relByID[plan.ID] = relPath
		absByID[plan.ID] = filepath.Join(spaceDir, filepath.FromSlash(relPath))
	}

	return absByID, relByID
}

func plannedPageRelPath(page confluence.Page, pageByID map[string]confluence.Page, folderByID map[string]confluence.Folder, hasChildren map[string]bool) string {
	title := strings.TrimSpace(page.Title)
	if title == "" {
		title = "page-" + page.ID
	}
	filename := fs.SanitizeMarkdownFilename(title)

	ancestorSegments, ok := ancestorPathSegments(strings.TrimSpace(page.ParentPageID), strings.TrimSpace(page.ParentType), pageByID, folderByID)
	if !ok {
		// Fallback to flat if hierarchy is broken
		return normalizeRelPath(filename)
	}

	parts := append(ancestorSegments, filename)
	if hasChildren[page.ID] {
		// If the page has subpages, create a directory for it and place the page inside
		dirSegment := fs.SanitizePathSegment(title)
		parts = append(ancestorSegments, dirSegment, filename)
	}
	return normalizeRelPath(filepath.Join(parts...))
}

func ancestorPathSegments(parentID string, parentType string, pageByID map[string]confluence.Page, folderByID map[string]confluence.Folder) ([]string, bool) {
	currentID := strings.TrimSpace(parentID)
	currentType := strings.ToLower(strings.TrimSpace(parentType))
	if currentID == "" {
		return nil, true
	}
	if currentType == "" {
		currentType = "page"
	}

	visited := map[string]struct{}{}
	segmentsReversed := []string{}
	for currentID != "" {
		if _, seen := visited[currentID]; seen {
			return nil, false
		}
		visited[currentID] = struct{}{}

		var title string
		var nextID string
		var nextType string

		if currentType == "folder" {
			folder, ok := folderByID[currentID]
			if !ok {
				return nil, false
			}
			title = strings.TrimSpace(folder.Title)
			if title == "" {
				title = "folder-" + folder.ID
			}
			nextID = strings.TrimSpace(folder.ParentID)
			nextType = strings.ToLower(strings.TrimSpace(folder.ParentType))
			if nextType == "" {
				nextType = "folder"
			}
		} else {
			parentPage, ok := pageByID[currentID]
			if !ok {
				return nil, false
			}
			title = strings.TrimSpace(parentPage.Title)
			if title == "" {
				title = "page-" + parentPage.ID
			}
			nextID = strings.TrimSpace(parentPage.ParentPageID)
			nextType = strings.ToLower(strings.TrimSpace(parentPage.ParentType))
			if nextType == "" {
				nextType = "page"
			}
		}

		// All ancestors (folders and pages) contribute a directory segment to their descendants.
		segmentsReversed = append(segmentsReversed, fs.SanitizePathSegment(title))

		currentID = nextID
		currentType = nextType
	}

	segments := make([]string, 0, len(segmentsReversed))
	for i := len(segmentsReversed) - 1; i >= 0; i-- {
		segments = append(segments, segmentsReversed[i])
	}
	return segments, true
}

func ensureUniqueMarkdownPath(baseName string, used map[string]struct{}) string {
	baseName = normalizeRelPath(baseName)
	if baseName == "" {
		baseName = "untitled.md"
	}
	if _, exists := used[baseName]; !exists {
		return baseName
	}

	ext := filepath.Ext(baseName)
	stem := strings.TrimSuffix(baseName, ext)
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, exists := used[candidate]; !exists {
			return candidate
		}
	}
}

func deletedPageIDs(previousPageIndex map[string]string, remotePages map[string]confluence.Page) []string {
	set := map[string]struct{}{}
	for _, pageID := range previousPageIndex {
		if _, exists := remotePages[pageID]; !exists {
			set[pageID] = struct{}{}
		}
	}
	return sortedStringKeys(set)
}

// PlannedPagePathMove describes a tracked page whose planned markdown path changed.
type PlannedPagePathMove struct {
	PageID       string
	PreviousPath string
	PlannedPath  string
}

// PlannedPagePathMoves returns tracked pages whose planned relative markdown path changed.
func PlannedPagePathMoves(previousPageIndex map[string]string, nextPathByID map[string]string) []PlannedPagePathMove {
	previousPathByID := map[string]string{}
	for _, previousPath := range sortedStringKeys(previousPageIndex) {
		pageID := strings.TrimSpace(previousPageIndex[previousPath])
		if pageID == "" {
			continue
		}
		if _, exists := nextPathByID[pageID]; !exists {
			continue
		}
		normalizedPath := normalizeRelPath(previousPath)
		if normalizedPath == "" {
			continue
		}
		if _, exists := previousPathByID[pageID]; !exists {
			previousPathByID[pageID] = normalizedPath
		}
	}

	moves := make([]PlannedPagePathMove, 0, len(previousPathByID))
	for pageID, previousPath := range previousPathByID {
		nextPath := normalizeRelPath(nextPathByID[pageID])
		if previousPath == nextPath {
			continue
		}
		moves = append(moves, PlannedPagePathMove{
			PageID:       pageID,
			PreviousPath: previousPath,
			PlannedPath:  nextPath,
		})
	}

	sort.Slice(moves, func(i, j int) bool {
		if moves[i].PreviousPath == moves[j].PreviousPath {
			if moves[i].PlannedPath == moves[j].PlannedPath {
				return moves[i].PageID < moves[j].PageID
			}
			return moves[i].PlannedPath < moves[j].PlannedPath
		}
		return moves[i].PreviousPath < moves[j].PreviousPath
	})

	return moves
}

func pagePathMoveDiagnostic(move PlannedPagePathMove) PullDiagnostic {
	return PullDiagnostic{
		Path:     move.PreviousPath,
		Code:     "PAGE_PATH_MOVED",
		Message:  fmt.Sprintf("planned markdown path changed from %s to %s", move.PreviousPath, move.PlannedPath),
		Category: DiagnosticCategoryPathChange,
	}
}

// PagePathMoveDiagnostic reports a tracked page whose planned markdown path changed.
func PagePathMoveDiagnostic(move PlannedPagePathMove) PullDiagnostic {
	return pagePathMoveDiagnostic(move)
}

func invertPathByID(pathByID map[string]string) map[string]string {
	out := make(map[string]string, len(pathByID))
	for id, path := range pathByID {
		out[normalizeRelPath(path)] = id
	}
	return out
}

func normalizeRelPath(path string) string {
	path = strings.ReplaceAll(path, `\`, "/")
	path = filepath.ToSlash(filepath.Clean(path))
	path = strings.TrimPrefix(path, "./")
	if path == "." {
		return ""
	}
	return path
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[normalizeRelPath(key)] = value
	}
	return out
}

func normalizePullState(state fs.SpaceState) fs.SpaceState {
	state.PagePathIndex = cloneStringMap(state.PagePathIndex)
	state.AttachmentIndex = cloneStringMap(state.AttachmentIndex)
	state.FolderPathIndex = cloneStringMap(state.FolderPathIndex)
	return state
}

type recoveryRemote interface {
	GetPage(ctx context.Context, pageID string) (confluence.Page, error)
}

func recoverMissingPages(ctx context.Context, remote recoveryRemote, spaceID string, localPageIDs map[string]string, remotePages []confluence.Page) ([]confluence.Page, error) {
	remoteByID := make(map[string]struct{}, len(remotePages))
	for _, p := range remotePages {
		remoteByID[p.ID] = struct{}{}
	}

	result := remotePages
	processedIDs := make(map[string]struct{})
	for _, id := range localPageIDs {
		if id == "" {
			continue
		}
		if _, exists := remoteByID[id]; exists {
			continue
		}
		if _, processed := processedIDs[id]; processed {
			continue
		}
		processedIDs[id] = struct{}{}

		// Fetch missing page individually
		page, err := remote.GetPage(ctx, id)
		if err != nil {
			if errors.Is(err, confluence.ErrNotFound) || errors.Is(err, confluence.ErrArchived) {
				continue // Truly deleted
			}
			var apiErr *confluence.APIError
			if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
				continue
			}
			return nil, err
		}

		// If it belongs to the same space and is syncable, include it.
		if page.SpaceID == spaceID && IsSyncableRemotePageStatus(page.Status) {
			result = append(result, page)
			remoteByID[id] = struct{}{}
		}
	}
	return result, nil
}

func sortedStringKeys[V any](in map[string]V) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func buildFolderPathIndex(folderByID map[string]confluence.Folder, pageByID map[string]confluence.Page) map[string]string {
	if len(folderByID) == 0 {
		return nil
	}

	folderPathIndex := make(map[string]string)

	for folderID := range folderByID {
		localPath := buildFolderLocalPath(folderID, folderByID, pageByID)
		if localPath != "" {
			folderPathIndex[normalizeRelPath(localPath)] = folderID
		}
	}

	if len(folderPathIndex) == 0 {
		return nil
	}
	return folderPathIndex
}

func buildFolderLocalPath(folderID string, folderByID map[string]confluence.Folder, pageByID map[string]confluence.Page) string {
	segments := []string{}

	currentID := folderID
	currentType := "folder"

	for currentID != "" {
		var title string
		var nextID string
		var nextType string

		if currentType == "folder" {
			folder, ok := folderByID[currentID]
			if !ok {
				break
			}
			title = strings.TrimSpace(folder.Title)
			if title == "" {
				title = "folder-" + folder.ID
			}
			nextID = strings.TrimSpace(folder.ParentID)
			nextType = strings.ToLower(strings.TrimSpace(folder.ParentType))
			if nextType == "" {
				nextType = "folder"
			}
		} else {
			page, ok := pageByID[currentID]
			if !ok {
				break
			}
			title = strings.TrimSpace(page.Title)
			if title == "" {
				title = "page-" + page.ID
			}
			nextID = strings.TrimSpace(page.ParentPageID)
			nextType = strings.ToLower(strings.TrimSpace(page.ParentType))
			if nextType == "" {
				nextType = "page"
			}
		}

		segments = append(segments, fs.SanitizePathSegment(title))

		currentID = nextID
		currentType = nextType
	}

	if len(segments) == 0 {
		return ""
	}

	for i, j := 0, len(segments)-1; i < j; i, j = i+1, j-1 {
		segments[i], segments[j] = segments[j], segments[i]
	}

	return normalizeRelPath(filepath.Join(segments...))
}
