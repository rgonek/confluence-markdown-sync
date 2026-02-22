package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

const pushPageBatchSize = 100

var markdownImageRefPattern = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)

// PushRemote defines remote operations required by push orchestration.
type PushRemote interface {
	GetSpace(ctx context.Context, spaceKey string) (confluence.Space, error)
	ListPages(ctx context.Context, opts confluence.PageListOptions) (confluence.PageListResult, error)
	GetPage(ctx context.Context, pageID string) (confluence.Page, error)
	CreatePage(ctx context.Context, input confluence.PageUpsertInput) (confluence.Page, error)
	UpdatePage(ctx context.Context, pageID string, input confluence.PageUpsertInput) (confluence.Page, error)
	ArchivePages(ctx context.Context, pageIDs []string) (confluence.ArchiveResult, error)
	DeletePage(ctx context.Context, pageID string, hardDelete bool) error
	UploadAttachment(ctx context.Context, input confluence.AttachmentUploadInput) (confluence.Attachment, error)
	DeleteAttachment(ctx context.Context, attachmentID string, pageID string) error
}

// PushConflictPolicy controls remote-ahead conflict behavior.
type PushConflictPolicy string

const (
	PushConflictPolicyPullMerge PushConflictPolicy = "pull-merge"
	PushConflictPolicyForce     PushConflictPolicy = "force"
	PushConflictPolicyCancel    PushConflictPolicy = "cancel"
)

// PushChangeType is the git-derived file change type for push planning.
type PushChangeType string

const (
	PushChangeAdd      PushChangeType = "A"
	PushChangeModify   PushChangeType = "M"
	PushChangeDelete   PushChangeType = "D"
	PushChangeTypeNone PushChangeType = ""
)

// PushFileChange captures one changed markdown path inside a space scope.
type PushFileChange struct {
	Type PushChangeType
	Path string
}

// PushOptions controls push orchestration.
type PushOptions struct {
	SpaceKey       string
	SpaceDir       string
	Domain         string
	State          fs.SpaceState
	Changes        []PushFileChange
	ConflictPolicy PushConflictPolicy
	HardDelete     bool
	Progress       Progress
}

// PushCommitPlan describes local paths and metadata for one push commit.
type PushCommitPlan struct {
	Path        string
	Deleted     bool
	PageID      string
	PageTitle   string
	Version     int
	SpaceKey    string
	URL         string
	StagedPaths []string
}

// PushResult captures outputs of push orchestration.
type PushResult struct {
	State   fs.SpaceState
	Commits []PushCommitPlan
}

// PushConflictError indicates a remote-ahead page conflict.
type PushConflictError struct {
	Path          string
	PageID        string
	LocalVersion  int
	RemoteVersion int
	Policy        PushConflictPolicy
}

func (e *PushConflictError) Error() string {
	return fmt.Sprintf(
		"remote version conflict for %s (page %s): local=%d remote=%d policy=%s",
		e.Path,
		e.PageID,
		e.LocalVersion,
		e.RemoteVersion,
		e.Policy,
	)
}

// Push executes the v1 push sync loop for in-scope markdown changes.
func Push(ctx context.Context, remote PushRemote, opts PushOptions) (PushResult, error) {
	if strings.TrimSpace(opts.SpaceKey) == "" {
		return PushResult{}, errors.New("space key is required")
	}
	if strings.TrimSpace(opts.SpaceDir) == "" {
		return PushResult{}, errors.New("space directory is required")
	}
	if len(opts.Changes) == 0 {
		state := opts.State
		state = normalizePushState(state)
		return PushResult{State: state}, nil
	}

	spaceDir, err := filepath.Abs(opts.SpaceDir)
	if err != nil {
		return PushResult{}, fmt.Errorf("resolve space directory: %w", err)
	}
	opts.SpaceDir = spaceDir

	state := normalizePushState(opts.State)
	policy := normalizeConflictPolicy(opts.ConflictPolicy)

	space, err := remote.GetSpace(ctx, opts.SpaceKey)
	if err != nil {
		return PushResult{}, fmt.Errorf("resolve space %q: %w", opts.SpaceKey, err)
	}

	pages, err := listAllPushPages(ctx, remote, confluence.PageListOptions{
		SpaceID:  space.ID,
		SpaceKey: opts.SpaceKey,
		Status:   "current",
		Limit:    pushPageBatchSize,
	})
	if err != nil {
		return PushResult{}, fmt.Errorf("list pages: %w", err)
	}
	remotePageByID := make(map[string]confluence.Page, len(pages))
	for _, page := range pages {
		remotePageByID[page.ID] = page
	}

	pageIDByPath, err := BuildPageIndex(spaceDir)
	if err != nil {
		return PushResult{}, fmt.Errorf("build page index: %w", err)
	}

	attachmentIDByPath := cloneStringMap(state.AttachmentIndex)
	changes := normalizePushChanges(opts.Changes)
	commits := make([]PushCommitPlan, 0, len(changes))

	if opts.Progress != nil {
		opts.Progress.SetDescription("Pushing changes")
		opts.Progress.SetTotal(len(changes))
	}

	for _, change := range changes {
		relPath := normalizeRelPath(change.Path)
		if relPath == "" {
			if opts.Progress != nil {
				opts.Progress.Add(1)
			}
			continue
		}

		switch change.Type {
		case PushChangeDelete:
			commit, err := pushDeletePage(ctx, remote, opts, state, remotePageByID, relPath)
			if err != nil {
				return PushResult{}, err
			}
			if commit.Path != "" {
				commits = append(commits, commit)
			}
		case PushChangeAdd, PushChangeModify:
			commit, err := pushUpsertPage(
				ctx,
				remote,
				space,
				opts,
				state,
				policy,
				pageIDByPath,
				attachmentIDByPath,
				remotePageByID,
				relPath,
			)
			if err != nil {
				return PushResult{}, err
			}
			if commit.Path != "" {
				commits = append(commits, commit)
			}
		default:
			if opts.Progress != nil {
				opts.Progress.Add(1)
			}
			continue
		}

		if opts.Progress != nil {
			opts.Progress.Add(1)
		}
	}

	if opts.Progress != nil {
		opts.Progress.Done()
	}

	state.AttachmentIndex = attachmentIDByPath

	return PushResult{
		State:   state,
		Commits: commits,
	}, nil
}

func pushDeletePage(
	ctx context.Context,
	remote PushRemote,
	opts PushOptions,
	state fs.SpaceState,
	remotePageByID map[string]confluence.Page,
	relPath string,
) (PushCommitPlan, error) {
	pageID := strings.TrimSpace(state.PagePathIndex[relPath])
	if pageID == "" {
		return PushCommitPlan{}, nil
	}

	page := remotePageByID[pageID]
	if opts.HardDelete {
		if err := remote.DeletePage(ctx, pageID, true); err != nil && !errors.Is(err, confluence.ErrNotFound) {
			return PushCommitPlan{}, fmt.Errorf("hard-delete page %s: %w", pageID, err)
		}
	} else {
		if _, err := remote.ArchivePages(ctx, []string{pageID}); err != nil && !errors.Is(err, confluence.ErrNotFound) {
			return PushCommitPlan{}, fmt.Errorf("archive page %s: %w", pageID, err)
		}
	}

	stalePaths := collectPageAttachmentPaths(state.AttachmentIndex, pageID)
	for _, assetPath := range stalePaths {
		attachmentID := state.AttachmentIndex[assetPath]
		if strings.TrimSpace(attachmentID) != "" {
			if err := remote.DeleteAttachment(ctx, attachmentID, pageID); err != nil && !errors.Is(err, confluence.ErrNotFound) {
				return PushCommitPlan{}, fmt.Errorf("delete attachment %s: %w", attachmentID, err)
			}
		}
		delete(state.AttachmentIndex, assetPath)
	}

	delete(state.PagePathIndex, relPath)

	stagedPaths := append([]string{relPath}, stalePaths...)
	stagedPaths = dedupeSortedPaths(stagedPaths)

	pageTitle := page.Title
	if strings.TrimSpace(pageTitle) == "" {
		pageTitle = strings.TrimSuffix(filepath.Base(relPath), filepath.Ext(relPath))
	}

	return PushCommitPlan{
		Path:        relPath,
		Deleted:     true,
		PageID:      pageID,
		PageTitle:   pageTitle,
		Version:     page.Version,
		SpaceKey:    opts.SpaceKey,
		URL:         page.WebURL,
		StagedPaths: stagedPaths,
	}, nil
}

func pushUpsertPage(
	ctx context.Context,
	remote PushRemote,
	space confluence.Space,
	opts PushOptions,
	state fs.SpaceState,
	policy PushConflictPolicy,
	pageIDByPath PageIndex,
	attachmentIDByPath map[string]string,
	remotePageByID map[string]confluence.Page,
	relPath string,
) (PushCommitPlan, error) {
	absPath := filepath.Join(opts.SpaceDir, filepath.FromSlash(relPath))
	doc, err := fs.ReadMarkdownDocument(absPath)
	if err != nil {
		return PushCommitPlan{}, fmt.Errorf("read markdown %s: %w", relPath, err)
	}

	pageID := strings.TrimSpace(doc.Frontmatter.ID)
	if !strings.EqualFold(strings.TrimSpace(doc.Frontmatter.Space), strings.TrimSpace(opts.SpaceKey)) {
		return PushCommitPlan{}, fmt.Errorf("%s belongs to space %q, expected %q", relPath, doc.Frontmatter.Space, opts.SpaceKey)
	}

	localVersion := doc.Frontmatter.Version
	fallbackParentID := strings.TrimSpace(doc.Frontmatter.ConfluenceParentPageID)
	var remotePage confluence.Page
	if pageID != "" {
		// Always fetch the latest version specifically for the page we're about to update
		// to avoid eventual consistency issues with space-wide listing.
		fetched, fetchErr := remote.GetPage(ctx, pageID)
		if fetchErr != nil {
			if errors.Is(fetchErr, confluence.ErrNotFound) {
				return PushCommitPlan{}, fmt.Errorf("remote page %s for %s was not found", pageID, relPath)
			}
			return PushCommitPlan{}, fmt.Errorf("fetch page %s: %w", pageID, fetchErr)
		}
		remotePage = fetched
		remotePageByID[pageID] = fetched

		fallbackParentID = strings.TrimSpace(remotePage.ParentPageID)

		if remotePage.Version > localVersion {
			switch policy {

			case PushConflictPolicyForce:
				// Continue and overwrite on top of remote head.
			case PushConflictPolicyPullMerge, PushConflictPolicyCancel:
				return PushCommitPlan{}, &PushConflictError{
					Path:          relPath,
					PageID:        pageID,
					LocalVersion:  localVersion,
					RemoteVersion: remotePage.Version,
					Policy:        policy,
				}
			default:
				return PushCommitPlan{}, &PushConflictError{
					Path:          relPath,
					PageID:        pageID,
					LocalVersion:  localVersion,
					RemoteVersion: remotePage.Version,
					Policy:        PushConflictPolicyCancel,
				}
			}
		}
	} else {
		// Create a placeholder page to get an ID for attachments
		title := resolveLocalTitle(doc, relPath)
		resolvedParentID := resolveParentIDFromHierarchy(relPath, "", fallbackParentID, pageIDByPath)
		created, err := remote.CreatePage(ctx, confluence.PageUpsertInput{
			SpaceID:      space.ID,
			ParentPageID: resolvedParentID,
			Title:        title,
			Status:       "current",
			BodyADF:      []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Initial sync..."}]}]}`),
		})
		if err != nil {
			return PushCommitPlan{}, fmt.Errorf("create placeholder page for %s: %w", relPath, err)
		}
		pageID = created.ID
		doc.Frontmatter.ID = pageID
		doc.Frontmatter.Space = opts.SpaceKey
		doc.Frontmatter.Version = created.Version
		localVersion = created.Version
		remotePage = created
		remotePageByID[pageID] = created
		pageIDByPath[normalizeRelPath(relPath)] = pageID
	}

	linkHook := NewReverseLinkHook(opts.SpaceDir, pageIDByPath, opts.Domain)
	mediaHook := NewReverseMediaHook(opts.SpaceDir, attachmentIDByPath)

	if _, err := converter.Reverse(ctx, []byte(doc.Body), converter.ReverseConfig{
		LinkHook:  linkHook,
		MediaHook: mediaHook,
		Strict:    true,
	}, absPath); err != nil {
		return PushCommitPlan{}, fmt.Errorf("strict conversion failed for %s: %w", relPath, err)
	}

	referencedAssetPaths, err := collectReferencedAssetPaths(opts.SpaceDir, absPath, doc.Body)
	if err != nil {
		return PushCommitPlan{}, fmt.Errorf("resolve assets for %s: %w", relPath, err)
	}

	touchedAssets := make([]string, 0)
	referencedIDs := map[string]struct{}{}
	for _, assetRelPath := range referencedAssetPaths {
		if existingID := strings.TrimSpace(attachmentIDByPath[assetRelPath]); existingID != "" {
			referencedIDs[existingID] = struct{}{}
			touchedAssets = append(touchedAssets, assetRelPath)
			continue
		}

		assetAbsPath := filepath.Join(opts.SpaceDir, filepath.FromSlash(assetRelPath))
		raw, err := os.ReadFile(assetAbsPath)
		if err != nil {
			return PushCommitPlan{}, fmt.Errorf("read asset %s: %w", assetRelPath, err)
		}

		uploaded, err := remote.UploadAttachment(ctx, confluence.AttachmentUploadInput{
			PageID:      pageID,
			Filename:    filepath.Base(assetAbsPath),
			ContentType: detectAssetContentType(assetAbsPath, raw),
			Data:        raw,
		})
		if err != nil {
			return PushCommitPlan{}, fmt.Errorf("upload asset %s: %w", assetRelPath, err)
		}

		uploadedID := strings.TrimSpace(uploaded.ID)
		if uploadedID == "" {
			return PushCommitPlan{}, fmt.Errorf("upload asset %s returned empty attachment ID", assetRelPath)
		}

		attachmentIDByPath[assetRelPath] = uploadedID
		state.AttachmentIndex[assetRelPath] = uploadedID
		referencedIDs[uploadedID] = struct{}{}
		touchedAssets = append(touchedAssets, assetRelPath)
	}

	stalePaths := collectPageAttachmentPaths(state.AttachmentIndex, pageID)
	for _, stalePath := range stalePaths {
		attachmentID := strings.TrimSpace(state.AttachmentIndex[stalePath])
		if attachmentID == "" {
			delete(state.AttachmentIndex, stalePath)
			delete(attachmentIDByPath, stalePath)
			continue
		}
		if _, keep := referencedIDs[attachmentID]; keep {
			continue
		}
		if err := remote.DeleteAttachment(ctx, attachmentID, pageID); err != nil && !errors.Is(err, confluence.ErrNotFound) {
			return PushCommitPlan{}, fmt.Errorf("delete stale attachment %s: %w", attachmentID, err)
		}
		delete(state.AttachmentIndex, stalePath)
		delete(attachmentIDByPath, stalePath)
		touchedAssets = append(touchedAssets, stalePath)
	}

	mediaHook = NewReverseMediaHook(opts.SpaceDir, attachmentIDByPath)
	reverse, err := converter.Reverse(ctx, []byte(doc.Body), converter.ReverseConfig{
		LinkHook:  linkHook,
		MediaHook: mediaHook,
		Strict:    true,
	}, absPath)
	if err != nil {
		return PushCommitPlan{}, fmt.Errorf("strict conversion failed for %s after attachment mapping: %w", relPath, err)
	}

	title := resolveLocalTitle(doc, relPath)
	resolvedParentID := resolveParentIDFromHierarchy(relPath, pageID, fallbackParentID, pageIDByPath)
	nextVersion := localVersion + 1
	if policy == PushConflictPolicyForce && remotePage.Version >= nextVersion {
		nextVersion = remotePage.Version + 1
	}

	// Post-process ADF to ensure required attributes for Confluence v2 API
	finalADF, err := ensureADFMediaCollection(reverse.ADF, pageID)
	if err != nil {
		return PushCommitPlan{}, fmt.Errorf("post-process ADF for %s: %w", relPath, err)
	}

	updatedPage, err := remote.UpdatePage(ctx, pageID, confluence.PageUpsertInput{
		SpaceID:      space.ID,
		ParentPageID: resolvedParentID,
		Title:        title,
		Status:       "current",
		Version:      nextVersion,
		BodyADF:      finalADF,
	})
	if err != nil {
		return PushCommitPlan{}, fmt.Errorf("update page %s: %w", pageID, err)
	}

	doc.Frontmatter.Title = title
	doc.Frontmatter.Version = updatedPage.Version
	if err := fs.WriteMarkdownDocument(absPath, doc); err != nil {
		return PushCommitPlan{}, fmt.Errorf("write markdown %s: %w", relPath, err)
	}

	state.PagePathIndex[relPath] = pageID
	stagedPaths := append([]string{relPath}, touchedAssets...)
	stagedPaths = dedupeSortedPaths(stagedPaths)

	return PushCommitPlan{
		Path:        relPath,
		Deleted:     false,
		PageID:      pageID,
		PageTitle:   updatedPage.Title,
		Version:     updatedPage.Version,
		SpaceKey:    opts.SpaceKey,
		URL:         updatedPage.WebURL,
		StagedPaths: stagedPaths,
	}, nil
}

func resolveParentIDFromHierarchy(relPath, pageID, fallbackParentID string, pageIDByPath PageIndex) string {
	resolvedFallback := strings.TrimSpace(fallbackParentID)
	resolvedPageID := strings.TrimSpace(pageID)

	dirPath := normalizeRelPath(filepath.ToSlash(filepath.Dir(filepath.FromSlash(relPath))))
	if dirPath == "" || dirPath == "." {
		return resolvedFallback
	}

	currentDir := dirPath
	for currentDir != "" && currentDir != "." {
		dirBase := filepath.Base(filepath.FromSlash(currentDir))
		if strings.TrimSpace(dirBase) != "" && dirBase != "." {
			candidatePath := normalizeRelPath(filepath.ToSlash(filepath.Join(currentDir, dirBase+".md")))
			candidateID := strings.TrimSpace(pageIDByPath[candidatePath])
			if candidateID != "" && candidateID != resolvedPageID {
				return candidateID
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
		if out[i].Path == out[j].Path {
			return out[i].Type < out[j].Type
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func collectReferencedAssetPaths(spaceDir, sourcePath, body string) ([]string, error) {
	matches := markdownImageRefPattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil, nil
	}

	paths := map[string]struct{}{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		destination := normalizeMarkdownDestination(match[1])
		if destination == "" || isExternalDestination(destination) {
			continue
		}

		if idx := strings.Index(destination, "#"); idx >= 0 {
			destination = destination[:idx]
		}
		if idx := strings.Index(destination, "?"); idx >= 0 {
			destination = destination[:idx]
		}
		if destination == "" {
			continue
		}

		assetAbsPath := filepath.Clean(filepath.Join(filepath.Dir(sourcePath), filepath.FromSlash(destination)))
		if !isSubpathOrSame(spaceDir, assetAbsPath) {
			return nil, fmt.Errorf("asset path escapes space root: %s", destination)
		}
		if _, err := os.Stat(assetAbsPath); err != nil {
			return nil, fmt.Errorf("asset %s not found", destination)
		}

		relPath, err := filepath.Rel(spaceDir, assetAbsPath)
		if err != nil {
			return nil, err
		}
		relPath = normalizeRelPath(relPath)
		if !strings.HasPrefix(relPath, "assets/") {
			continue
		}
		paths[relPath] = struct{}{}
	}

	return sortedStringKeys(paths), nil
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
		if nodeType, ok := n["type"].(string); ok && nodeType == "media" {
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
