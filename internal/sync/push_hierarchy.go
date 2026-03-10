package sync

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func resolveParentIDFromHierarchy(relPath, pageID, fallbackParentID string, pageIDByPath PageIndex, folderIDByPath map[string]string) string {
	resolvedFallback := strings.TrimSpace(fallbackParentID)
	resolvedPageID := strings.TrimSpace(pageID)
	normalizedRelPath := normalizeRelPath(relPath)

	dirPath := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(relPath))))
	if dirPath == "" || dirPath == "." {
		return resolvedFallback
	}

	currentDir := dirPath
	for currentDir != "" && currentDir != "." {
		dirBase := strings.TrimSpace(filepath.Base(filepath.FromSlash(currentDir)))
		if dirBase != "" && dirBase != "." {
			candidatePath := indexPagePathForDir(currentDir)
			if candidatePath != "" && candidatePath != normalizedRelPath {
				candidateID := strings.TrimSpace(pageIDByPath[candidatePath])
				if candidateID != "" && candidateID != resolvedPageID {
					return candidateID
				}
			}

			if folderID, ok := folderIDByPath[currentDir]; ok && strings.TrimSpace(folderID) != "" {
				return strings.TrimSpace(folderID)
			}
		}

		nextDir := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(currentDir))))
		if nextDir == "" || nextDir == "." || nextDir == currentDir {
			break
		}
		currentDir = nextDir
	}

	return resolvedFallback
}

func ensureFolderHierarchy(
	ctx context.Context,
	remote PushRemote,
	spaceID, dirPath string,
	currentRelPath string,
	opts PushOptions,
	pageIDByPath PageIndex,
	folderIDByPath map[string]string,
	diagnostics *[]PushDiagnostic,
) (map[string]string, error) {
	if dirPath == "" || dirPath == "." {
		return folderIDByPath, nil
	}
	if folderIDByPath == nil {
		folderIDByPath = map[string]string{}
	}

	segments := strings.Split(filepath.ToSlash(dirPath), "/")
	var currentPath string
	parentID := ""
	parentType := "space"

	for _, seg := range segments {
		if currentPath == "" {
			currentPath = seg
		} else {
			currentPath = filepath.ToSlash(filepath.Join(currentPath, seg))
		}

		if isIndexFile(currentRelPath) {
			dirOfCurrent := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(currentRelPath))))
			if currentPath == dirOfCurrent {
				continue
			}
		}

		if indexParentID, hasIndexParent := indexPageParentIDForDir(currentPath, currentRelPath, pageIDByPath); hasIndexParent {
			parentID = indexParentID
			parentType = "page"
			continue
		}

		if existingID, ok := folderIDByPath[currentPath]; ok && strings.TrimSpace(existingID) != "" {
			parentID = strings.TrimSpace(existingID)
			if opts.folderMode == tenantFolderModePageFallback {
				parentType = "page"
			} else {
				parentType = "folder"
			}
			continue
		}

		if opts.folderMode == tenantFolderModePageFallback {
			pageCreated, pageErr := remote.CreatePage(ctx, confluence.PageUpsertInput{
				SpaceID:      spaceID,
				ParentPageID: parentID,
				Title:        seg,
				Status:       "current",
				BodyADF:      []byte(`{"version":1,"type":"doc","content":[]}`),
			})
			if pageErr != nil {
				return nil, fmt.Errorf("create compatibility hierarchy page %q: %w", currentPath, pageErr)
			}

			createdID := strings.TrimSpace(pageCreated.ID)
			if createdID == "" {
				return nil, fmt.Errorf("create compatibility hierarchy page %q returned empty page ID", currentPath)
			}

			folderIDByPath[currentPath] = createdID
			parentID = createdID
			parentType = "page"
			continue
		}

		// Check if folder already exists remotely by title
		if f, ok := opts.RemoteFolderByTitle[strings.ToLower(strings.TrimSpace(seg))]; ok {
			createdID := strings.TrimSpace(f.ID)
			if createdID != "" {
				folderIDByPath[currentPath] = createdID
				parentID = createdID
				parentType = "folder"
				continue
			}
		}

		createInput := confluence.FolderCreateInput{
			SpaceID: spaceID,
			Title:   seg,
		}
		if strings.TrimSpace(parentID) != "" {
			createInput.ParentID = parentID
			createInput.ParentType = parentType
		}

		created, err := remote.CreateFolder(ctx, createInput)
		if err != nil {
			slog.Info("folder_creation_failed", "path", currentPath, "error", err.Error())

			foundExisting := false
			// 1. Try to find it in pre-fetched folders
			if f, ok := opts.RemoteFolderByTitle[strings.ToLower(strings.TrimSpace(seg))]; ok {
				created = f
				err = nil
				foundExisting = true
			}

			// 2. If not found and it's a conflict, try robust listing
			if !foundExisting && strings.Contains(err.Error(), "400") && (strings.Contains(strings.ToLower(err.Error()), "folder exists with the same title") || strings.Contains(strings.ToLower(err.Error()), "already exists with the same title")) {
				folders, listErr := listAllPushFoldersWithTracking(ctx, remote, confluence.FolderListOptions{
					SpaceID: spaceID,
					Title:   seg,
				}, opts.folderListTracker, currentPath)
				if listErr == nil {
					for _, f := range folders {
						if strings.EqualFold(strings.TrimSpace(f.Title), strings.TrimSpace(seg)) {
							created = f
							err = nil
							foundExisting = true
							break
						}
					}
				}
			}

			// 3. Fallback: if it's still failing, check if it exists as a PAGE
			if !foundExisting {
				pages, listErr := remote.ListPages(ctx, confluence.PageListOptions{
					SpaceID: spaceID,
					Title:   seg,
					Status:  "current",
				})
				if listErr == nil {
					for _, p := range pages.Pages {
						if strings.EqualFold(strings.TrimSpace(p.Title), strings.TrimSpace(seg)) {
							created = confluence.Folder{
								ID:         p.ID,
								SpaceID:    p.SpaceID,
								Title:      p.Title,
								ParentID:   p.ParentPageID,
								ParentType: p.ParentType,
							}
							err = nil
							foundExisting = true
							break
						}
					}
				}
			}

			// 4. Radical fallback: if it's STILL failing, create it as a PAGE
			if !foundExisting {
				slog.Warn("folder_api_broken_falling_back_to_page", "path", currentPath)
				pageCreated, pageErr := remote.CreatePage(ctx, confluence.PageUpsertInput{
					SpaceID:      spaceID,
					ParentPageID: parentID,
					Title:        seg,
					Status:       "current",
					BodyADF:      []byte(`{"version":1,"type":"doc","content":[]}`),
				})
				if pageErr == nil {
					created = confluence.Folder{
						ID:         pageCreated.ID,
						SpaceID:    pageCreated.SpaceID,
						Title:      pageCreated.Title,
						ParentID:   pageCreated.ParentPageID,
						ParentType: pageCreated.ParentType,
					}
					err = nil
					foundExisting = true
				}
			}

			if err != nil {
				return nil, fmt.Errorf("create folder %q: %w", currentPath, err)
			}
		}

		createdID := strings.TrimSpace(created.ID)
		if createdID == "" {
			return nil, fmt.Errorf("create folder %q returned empty folder ID", currentPath)
		}

		folderIDByPath[currentPath] = createdID
		parentID = createdID
		parentType = "folder"

		if diagnostics != nil {
			*diagnostics = append(*diagnostics, PushDiagnostic{
				Path:    currentPath,
				Code:    "FOLDER_CREATED",
				Message: fmt.Sprintf("Auto-created Confluence folder %q (id=%s)", currentPath, created.ID),
			})
		}
	}

	return folderIDByPath, nil
}

func collapseFolderParentIfIndexPage(
	ctx context.Context,
	remote PushRemote,
	relPath, pageID string,
	folderIDByPath map[string]string,
	remotePageByID map[string]confluence.Page,
	diagnostics *[]PushDiagnostic,
) {
	if !isIndexFile(relPath) {
		return
	}

	pageID = strings.TrimSpace(pageID)
	if pageID == "" {
		return
	}

	dirPath := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(relPath))))
	if dirPath == "" || dirPath == "." {
		return
	}

	folderID := strings.TrimSpace(folderIDByPath[dirPath])
	if folderID == "" {
		return
	}

	movedChildren := 0
	for _, remoteID := range sortedStringKeys(remotePageByID) {
		remotePage := remotePageByID[remoteID]
		if strings.TrimSpace(remotePage.ID) == "" || strings.TrimSpace(remotePage.ID) == pageID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(remotePage.ParentType), "folder") {
			continue
		}
		if strings.TrimSpace(remotePage.ParentPageID) != folderID {
			continue
		}

		if err := remote.MovePage(ctx, remotePage.ID, pageID); err != nil {
			appendPushDiagnostic(
				diagnostics,
				relPath,
				"FOLDER_COLLAPSE_MOVE_FAILED",
				fmt.Sprintf("failed to move page %s from folder %s under index page %s: %v", remotePage.ID, folderID, pageID, err),
			)
			continue
		}

		remotePage.ParentType = "page"
		remotePage.ParentPageID = pageID
		remotePageByID[remotePage.ID] = remotePage
		movedChildren++
		appendPushDiagnostic(
			diagnostics,
			relPath,
			"FOLDER_CHILD_REPARENTED",
			fmt.Sprintf("moved page %s under index page %s", remotePage.ID, pageID),
		)
	}

	delete(folderIDByPath, dirPath)
	appendPushDiagnostic(
		diagnostics,
		relPath,
		"FOLDER_COLLAPSED",
		fmt.Sprintf("collapsed folder %q (id=%s) into index page %s; moved %d child page(s)", dirPath, folderID, pageID, movedChildren),
	)
}

func indexPagePathForDir(dirPath string) string {
	dirPath = normalizeRelPath(dirPath)
	if dirPath == "" || dirPath == "." {
		return ""
	}
	dirBase := strings.TrimSpace(filepath.Base(filepath.FromSlash(dirPath)))
	if dirBase == "" || dirBase == "." {
		return ""
	}
	return normalizeRelPath(filepath.ToSlash(filepath.Join(dirPath, dirBase+".md")))
}

func indexPageParentIDForDir(dirPath, currentRelPath string, pageIDByPath PageIndex) (string, bool) {
	if len(pageIDByPath) == 0 {
		return "", false
	}
	indexPath := indexPagePathForDir(dirPath)
	if indexPath == "" || indexPath == normalizeRelPath(currentRelPath) {
		return "", false
	}
	indexPageID := strings.TrimSpace(pageIDByPath[indexPath])
	if indexPageID == "" {
		return "", false
	}
	return indexPageID, true
}

func normalizePushState(state fs.SpaceState) fs.SpaceState {
	if state.PagePathIndex == nil {
		state.PagePathIndex = map[string]string{}
	}
	if state.AttachmentIndex == nil {
		state.AttachmentIndex = map[string]string{}
	}
	normalizedPageIndex := make(map[string]string, len(state.PagePathIndex))
	for path, id := range state.PagePathIndex {
		normalizedPageIndex[normalizeRelPath(path)] = id
	}
	state.PagePathIndex = normalizedPageIndex
	state.AttachmentIndex = cloneStringMap(state.AttachmentIndex)
	state.FolderPathIndex = cloneStringMap(state.FolderPathIndex)
	return state
}

func normalizeConflictPolicy(policy PushConflictPolicy) PushConflictPolicy {
	switch policy {
	case PushConflictPolicyPullMerge, PushConflictPolicyForce, PushConflictPolicyCancel:
		return policy
	default:
		return PushConflictPolicyCancel
	}
}

func normalizePushChanges(changes []PushFileChange) []PushFileChange {
	out := make([]PushFileChange, 0, len(changes))
	for _, change := range changes {
		path := normalizeRelPath(change.Path)
		if path == "" {
			continue
		}
		switch change.Type {
		case PushChangeAdd, PushChangeModify, PushChangeDelete:
			out = append(out, PushFileChange{Type: change.Type, Path: path})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		pi := out[i].Path
		pj := out[j].Path

		if pi == pj {
			return out[i].Type < out[j].Type
		}

		// Count segments to sort by depth (shallowest first)
		di := strings.Count(pi, "/")
		dj := strings.Count(pj, "/")

		if di != dj {
			return di < dj
		}

		// Within same depth, check if it's an "index" file (BaseName/BaseName.md)
		// Index files should be pushed before their siblings to establish hierarchy.
		bi := isIndexFile(pi)
		bj := isIndexFile(pj)

		if bi != bj {
			return bi // true (index) comes before false
		}

		return pi < pj
	})
	return out
}

func seedPendingPageIDsForPushChanges(spaceDir string, changes []PushFileChange, pageIDByPath PageIndex) error {
	for _, change := range changes {
		switch change.Type {
		case PushChangeAdd, PushChangeModify:
			// continue
		default:
			continue
		}

		relPath := normalizeRelPath(change.Path)
		if relPath == "" {
			continue
		}
		if strings.TrimSpace(pageIDByPath[relPath]) != "" {
			continue
		}

		absPath := filepath.Join(spaceDir, filepath.FromSlash(relPath))
		fm, err := fs.ReadFrontmatter(absPath)
		if err != nil {
			return fmt.Errorf("read frontmatter %s: %w", relPath, err)
		}
		if strings.TrimSpace(fm.ID) != "" {
			pageIDByPath[relPath] = strings.TrimSpace(fm.ID)
			continue
		}

		pageIDByPath[relPath] = pendingPageID(relPath)
	}
	return nil
}

func runPushUpsertPreflight(
	ctx context.Context,
	opts PushOptions,
	changes []PushFileChange,
	pageIDByPath PageIndex,
	attachmentIDByPath map[string]string,
) error {
	for _, change := range changes {
		switch change.Type {
		case PushChangeAdd, PushChangeModify:
			// continue
		default:
			continue
		}

		relPath := normalizeRelPath(change.Path)
		if relPath == "" {
			continue
		}

		absPath := filepath.Join(opts.SpaceDir, filepath.FromSlash(relPath))
		doc, err := fs.ReadMarkdownDocument(absPath)
		if err != nil {
			return fmt.Errorf("read markdown %s: %w", relPath, err)
		}

		linkHook := NewReverseLinkHookWithGlobalIndex(opts.SpaceDir, pageIDByPath, opts.GlobalPageIndex, opts.Domain)
		strictAttachmentIndex, _, err := BuildStrictAttachmentIndex(opts.SpaceDir, absPath, doc.Body, attachmentIDByPath)
		if err != nil {
			return fmt.Errorf("resolve assets for %s: %w", relPath, err)
		}
		preparedBody, err := PrepareMarkdownForAttachmentConversion(opts.SpaceDir, absPath, doc.Body, strictAttachmentIndex)
		if err != nil {
			return fmt.Errorf("prepare attachment conversion for %s: %w", relPath, err)
		}
		mediaHook := NewReverseMediaHook(opts.SpaceDir, strictAttachmentIndex)

		if _, err := converter.Reverse(ctx, []byte(preparedBody), converter.ReverseConfig{
			LinkHook:  linkHook,
			MediaHook: mediaHook,
			Strict:    true,
		}, absPath); err != nil {
			return fmt.Errorf("strict conversion failed for %s: %w", relPath, err)
		}
	}

	return nil
}

func precreatePendingPushPages(
	ctx context.Context,
	remote PushRemote,
	space confluence.Space,
	opts PushOptions,
	state fs.SpaceState,
	changes []PushFileChange,
	pageIDByPath PageIndex,
	pageTitleByPath map[string]string,
	folderIDByPath map[string]string,
	diagnostics *[]PushDiagnostic,
) (map[string]confluence.Page, error) {
	precreated := map[string]confluence.Page{}

	for _, change := range changes {
		switch change.Type {
		case PushChangeAdd, PushChangeModify:
			// continue
		default:
			continue
		}

		relPath := normalizeRelPath(change.Path)
		if relPath == "" {
			continue
		}

		if !isPendingPageID(pageIDByPath[relPath]) {
			continue
		}

		absPath := filepath.Join(opts.SpaceDir, filepath.FromSlash(relPath))
		doc, err := fs.ReadMarkdownDocument(absPath)
		if err != nil {
			return nil, fmt.Errorf("read markdown %s: %w", relPath, err)
		}

		title := resolveLocalTitle(doc, relPath)
		pageTitleByPath[normalizeRelPath(relPath)] = title
		if conflictingPath, conflictingID := findTrackedTitleConflict(relPath, title, state.PagePathIndex, pageTitleByPath); conflictingPath != "" {
			return nil, fmt.Errorf(
				"new page %q duplicates tracked page %q (id=%s) with title %q; update the existing file instead of creating a duplicate",
				relPath,
				conflictingPath,
				conflictingID,
				title,
			)
		}

		dirPath := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(relPath))))
		if dirPath != "" && dirPath != "." {
			folderIDByPath, err = ensureFolderHierarchy(ctx, remote, space.ID, dirPath, relPath, opts, pageIDByPath, folderIDByPath, diagnostics)
			if err != nil {
				return nil, fmt.Errorf("ensure folder hierarchy for %s: %w", relPath, err)
			}
		}

		fallbackParentID := strings.TrimSpace(doc.Frontmatter.ConfluenceParentPageID)
		resolvedParentID := resolveParentIDFromHierarchy(relPath, "", fallbackParentID, pageIDByPath, folderIDByPath)
		created, err := remote.CreatePage(ctx, confluence.PageUpsertInput{
			SpaceID:      space.ID,
			ParentPageID: resolvedParentID,
			Title:        title,
			Status:       normalizePageLifecycleState(doc.Frontmatter.State),
			BodyADF:      []byte(`{"version":1,"type":"doc","content":[]}`),
		})
		if err != nil {
			if isDuplicateTitleCreateError(err) {
				conflictPage, conflictStatuses, resolved := findRemoteTitleCollision(ctx, remote, space.ID, title)
				if resolved {
					return nil, fmt.Errorf(
						"create placeholder page for %s: remote page title collision for %q (id=%s status=%s title=%q); rename the new file or reconcile the conflicting remote page first",
						relPath,
						title,
						conflictPage.ID,
						conflictPage.Status,
						conflictPage.Title,
					)
				}
				return nil, fmt.Errorf(
					"create placeholder page for %s: remote page title collision for %q, but the conflicting page was not discoverable through current/draft/archived title lookups (checked: %s); inspect the space for hidden or permission-restricted pages before retrying",
					relPath,
					title,
					strings.Join(conflictStatuses, ", "),
				)
			}
			if err != nil {
				return nil, fmt.Errorf("create placeholder page for %s: %w", relPath, err)
			}
		}

		createdID := strings.TrimSpace(created.ID)
		if createdID == "" {
			return nil, fmt.Errorf("create placeholder page for %s returned empty page ID", relPath)
		}

		pageIDByPath[relPath] = createdID
		precreated[relPath] = created
	}

	return precreated, nil
}

func isDuplicateTitleCreateError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "title already exists") || strings.Contains(lower, "already exists with the same title") || strings.Contains(lower, "a page with this title already exists")
}

func findRemoteTitleCollision(ctx context.Context, remote PushRemote, spaceID, title string) (confluence.Page, []string, bool) {
	statuses := []string{"current", "draft", "archived"}
	for _, status := range statuses {
		pages, err := remote.ListPages(ctx, confluence.PageListOptions{
			SpaceID: spaceID,
			Title:   title,
			Status:  status,
		})
		if err != nil {
			continue
		}
		for _, page := range pages.Pages {
			if strings.EqualFold(strings.TrimSpace(page.Title), strings.TrimSpace(title)) {
				return page, statuses, true
			}
		}
	}
	return confluence.Page{}, statuses, false
}

func cleanupPendingPrecreatedPages(
	ctx context.Context,
	remote PushRemote,
	precreatedPages map[string]confluence.Page,
	diagnostics *[]PushDiagnostic,
) {
	for _, relPath := range sortedStringKeys(precreatedPages) {
		pageID := strings.TrimSpace(precreatedPages[relPath].ID)
		if pageID == "" {
			continue
		}

		deleteOpts := deleteOptionsForPageLifecycle(precreatedPages[relPath].Status, false)
		if err := remote.DeletePage(ctx, pageID, deleteOpts); err != nil && !errors.Is(err, confluence.ErrNotFound) {
			appendPushDiagnostic(
				diagnostics,
				relPath,
				"ROLLBACK_PRECREATED_PAGE_FAILED",
				fmt.Sprintf("failed to delete pre-created placeholder page %s: %v", pageID, err),
			)
			continue
		}

		appendPushDiagnostic(
			diagnostics,
			relPath,
			"ROLLBACK_PRECREATED_PAGE_DELETED",
			fmt.Sprintf("deleted pre-created placeholder page %s", pageID),
		)
	}
}

func clonePageMap(in map[string]confluence.Page) map[string]confluence.Page {
	if in == nil {
		return map[string]confluence.Page{}
	}
	out := make(map[string]confluence.Page, len(in))
	for key, page := range in {
		out[key] = page
	}
	return out
}

func isIndexFile(path string) bool {
	base := filepath.Base(filepath.FromSlash(path))
	if !strings.HasSuffix(base, ".md") {
		return false
	}
	name := strings.TrimSuffix(base, ".md")
	dir := filepath.Base(filepath.FromSlash(filepath.Dir(filepath.FromSlash(path))))
	return name == dir
}
