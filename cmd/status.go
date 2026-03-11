package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

// StatusRemote defines the subset of Confluence API methods needed for sync inspection.
type StatusRemote interface {
	GetSpace(ctx context.Context, spaceKey string) (confluence.Space, error)
	ListPages(ctx context.Context, opts confluence.PageListOptions) (confluence.PageListResult, error)
	GetPage(ctx context.Context, pageID string) (confluence.Page, error)
	GetFolder(ctx context.Context, folderID string) (confluence.Folder, error)
	ListAttachments(ctx context.Context, pageID string) ([]confluence.Attachment, error)
}

// StatusReport contains the results of a sync drift inspection.
type StatusReport struct {
	LocalAdded              []string
	LocalModified           []string
	LocalDeleted            []string
	RemoteAdded             []string
	RemoteModified          []string
	RemoteDeleted           []string
	PlannedPathMoves        []syncflow.PlannedPagePathMove
	ConflictAhead           []string // pages that are both locally modified AND ahead on remote
	MaxVersionDrift         int
	LocalAttachmentAdded    []string
	LocalAttachmentDeleted  []string
	RemoteAttachmentAdded   []string
	RemoteAttachmentDeleted []string
	OrphanedLocalAssets     []string
}

const statusScopeNote = "Scope: markdown/page drift by default. Use `conf status --attachments` to inspect local and remote attachment drift from the same command."

var flagStatusAttachments bool

var newStatusRemote = func(cfg *config.Config) (StatusRemote, error) {
	return newConfluenceClientFromConfig(cfg)
}

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "status [TARGET]",

		Short: "Inspect local and remote sync drift",
		Long: `status prints a high-level sync summary without mutating local files or remote content.

Status scope defaults to markdown/page drift. Add ` + "`--attachments`" + ` to include local and remote attachment drift plus orphaned local asset files.

TARGET follows the standard rule:
- .md suffix => file mode (space inferred from file)
- otherwise => space mode (SPACE_KEY or space directory).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			return runStatus(cmd, config.ParseTarget(raw))
		},
	}
	cmd.Flags().BoolVar(&flagStatusAttachments, "attachments", false, "Include attachment drift and orphaned local asset inspection")

	return cmd
}

func runStatus(cmd *cobra.Command, target config.Target) error {
	if err := ensureWorkspaceSyncReady("status"); err != nil {
		return err
	}

	out := ensureSynchronizedCmdOutput(cmd)
	ctx := getCommandContext(cmd)

	initialCtx, err := resolveInitialPullContext(target)
	if err != nil {
		return err
	}
	if !dirExists(initialCtx.spaceDir) {
		return fmt.Errorf("space directory not found: %s", initialCtx.spaceDir)
	}

	state, err := fs.LoadState(initialCtx.spaceDir)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	spaceKey := strings.TrimSpace(initialCtx.spaceKey)
	if spaceKey == "" {
		spaceKey = strings.TrimSpace(state.SpaceKey)
	}
	if spaceKey == "" {
		return fmt.Errorf("unable to resolve space key for %s", initialCtx.spaceDir)
	}

	envPath := findEnvPath(initialCtx.spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if strings.TrimSpace(cfg.Domain) == "" {
		return fmt.Errorf("ATLASSIAN_DOMAIN is missing in %s", envPath)
	}

	remote, err := newStatusRemote(cfg)
	if err != nil {
		return fmt.Errorf("create Confluence client: %w", err)
	}
	defer closeRemoteIfPossible(remote)

	targetRelPath := ""
	if target.IsFile() {
		abs, absErr := filepath.Abs(target.Value)
		if absErr != nil {
			return absErr
		}
		rel, relErr := filepath.Rel(initialCtx.spaceDir, abs)
		if relErr != nil {
			return relErr
		}
		targetRelPath = normalizeRepoRelPath(rel)
	}

	report, err := buildStatusReport(ctx, remote, target, initialCtx, state, spaceKey, targetRelPath, flagStatusAttachments)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "Space: %s\n", spaceKey)
	_, _ = fmt.Fprintf(out, "Directory: %s\n", initialCtx.spaceDir)
	_, _ = fmt.Fprintf(out, "Note: %s\n", statusScopeNote)

	printStatusSection(out, "Local not pushed", report.LocalAdded, report.LocalModified, report.LocalDeleted)
	printStatusSection(out, "Remote not pulled", report.RemoteAdded, report.RemoteModified, report.RemoteDeleted)
	printPlannedPathMoves(out, report.PlannedPathMoves)

	if len(report.ConflictAhead) > 0 {
		_, _ = fmt.Fprintf(out, "\nConflict ahead (%d) — locally modified AND remote is ahead:\n", len(report.ConflictAhead))
		for _, p := range report.ConflictAhead {
			_, _ = fmt.Fprintf(out, "  ! %s\n", p)
		}
	}

	if report.MaxVersionDrift > 0 {
		_, _ = fmt.Fprintf(out, "\nVersion drift: local markdown is up to %d version(s) behind remote\n", report.MaxVersionDrift)
	} else {
		_, _ = fmt.Fprintln(out, "\nVersion drift: no remote-ahead tracked pages")
	}

	if flagStatusAttachments {
		printStatusSection(out, "Attachment drift", report.LocalAttachmentAdded, nil, report.LocalAttachmentDeleted)
		printStatusSection(out, "Remote attachment drift", report.RemoteAttachmentAdded, nil, report.RemoteAttachmentDeleted)
		printStatusList(out, "orphaned local assets", report.OrphanedLocalAssets)
	}

	return nil
}

func buildStatusReport(
	ctx context.Context,
	remote StatusRemote,
	target config.Target,
	initialCtx initialPullContext,
	state fs.SpaceState,
	spaceKey string,
	targetRelPath string,
	includeAttachments bool,
) (StatusReport, error) {
	localAdded, localModified, localDeleted, err := collectLocalStatusChanges(target, initialCtx.spaceDir, spaceKey)
	if err != nil {
		return StatusReport{}, err
	}

	space, err := remote.GetSpace(ctx, spaceKey)
	if err != nil {
		return StatusReport{}, fmt.Errorf("fetch space %s: %w", spaceKey, err)
	}

	remotePages, err := listAllPagesForStatus(ctx, remote, confluence.PageListOptions{SpaceID: space.ID, SpaceKey: space.Key, Status: "current", Limit: 100})
	if err != nil {
		return StatusReport{}, fmt.Errorf("list remote pages: %w", err)
	}
	remotePages, err = recoverMissingPagesForDiff(ctx, remote, space.ID, state.PagePathIndex, remotePages)
	if err != nil {
		return StatusReport{}, fmt.Errorf("recover tracked pages for status: %w", err)
	}

	pathByID := make(map[string]string, len(state.PagePathIndex))
	trackedPathByID := make(map[string]string, len(state.PagePathIndex))
	for path, pageID := range state.PagePathIndex {
		normalizedPath := normalizeRepoRelPath(path)
		normalizedID := strings.TrimSpace(pageID)
		if normalizedPath == "" || normalizedID == "" {
			continue
		}
		if targetRelPath != "" && normalizedPath != targetRelPath {
			continue
		}
		pathByID[normalizedID] = normalizedPath
		trackedPathByID[normalizedID] = normalizedPath
	}

	localVersionByID := map[string]int{}
	for pageID, relPath := range trackedPathByID {
		doc, readErr := fs.ReadMarkdownDocument(filepath.Join(initialCtx.spaceDir, filepath.FromSlash(relPath)))
		if readErr != nil {
			continue
		}
		localVersionByID[pageID] = doc.Frontmatter.Version
	}

	remoteByID := make(map[string]confluence.Page, len(remotePages))
	remoteAdded := make([]string, 0)
	remoteModified := make([]string, 0)
	maxVersionDrift := 0

	for _, page := range remotePages {
		pageID := strings.TrimSpace(page.ID)
		if pageID == "" {
			continue
		}
		remoteByID[pageID] = page

		trackedPath, tracked := pathByID[pageID]
		if !tracked {
			if targetRelPath == "" {
				remoteAdded = append(remoteAdded, fmt.Sprintf("%s (id=%s)", strings.TrimSpace(page.Title), pageID))
			}
			continue
		}

		localVersion := localVersionByID[pageID]
		if page.Version > localVersion {
			remoteModified = append(remoteModified, trackedPath)
			drift := page.Version - localVersion
			if drift > maxVersionDrift {
				maxVersionDrift = drift
			}
		}
	}

	remoteDeleted := make([]string, 0)
	for pageID, relPath := range trackedPathByID {
		if _, exists := remoteByID[pageID]; exists {
			continue
		}
		page, getErr := remote.GetPage(ctx, pageID)
		if getErr != nil {
			if isNotFoundError(getErr) {
				remoteDeleted = append(remoteDeleted, relPath)
			}
			continue
		}
		if strings.TrimSpace(page.SpaceID) != strings.TrimSpace(space.ID) || !syncflow.IsSyncableRemotePageStatus(page.Status) {
			remoteDeleted = append(remoteDeleted, relPath)
		}
	}

	sort.Strings(localAdded)
	sort.Strings(localModified)
	sort.Strings(localDeleted)
	sort.Strings(remoteAdded)
	sort.Strings(remoteModified)
	sort.Strings(remoteDeleted)

	folderByID, _, err := resolveDiffFolderHierarchyFromPages(ctx, remote, remotePages)
	if err != nil {
		return StatusReport{}, fmt.Errorf("resolve folder hierarchy: %w", err)
	}
	_, plannedPathByID := syncflow.PlanPagePaths(initialCtx.spaceDir, state.PagePathIndex, remotePages, folderByID)
	plannedPathMoves := syncflow.PlannedPagePathMoves(state.PagePathIndex, plannedPathByID)
	if targetRelPath != "" {
		filteredMoves := make([]syncflow.PlannedPagePathMove, 0, len(plannedPathMoves))
		for _, move := range plannedPathMoves {
			if move.PreviousPath == targetRelPath {
				filteredMoves = append(filteredMoves, move)
			}
		}
		plannedPathMoves = filteredMoves
	}

	// ConflictAhead = pages that are BOTH locally modified AND ahead on remote.
	conflictAhead := computeConflictAhead(localModified, remoteModified)
	sort.Strings(conflictAhead)

	localAttachmentAdded := []string{}
	localAttachmentDeleted := []string{}
	remoteAttachmentAdded := []string{}
	remoteAttachmentDeleted := []string{}
	orphanedLocalAssets := []string{}

	if includeAttachments {
		var attachmentErr error
		localAttachmentAdded, localAttachmentDeleted, remoteAttachmentAdded, remoteAttachmentDeleted, orphanedLocalAssets, attachmentErr = collectAttachmentStatus(
			ctx,
			remote,
			target,
			initialCtx.spaceDir,
			state,
			trackedPathByID,
			targetRelPath,
		)
		if attachmentErr != nil {
			return StatusReport{}, attachmentErr
		}
	}

	return StatusReport{
		LocalAdded:              localAdded,
		LocalModified:           localModified,
		LocalDeleted:            localDeleted,
		RemoteAdded:             remoteAdded,
		RemoteModified:          remoteModified,
		RemoteDeleted:           remoteDeleted,
		PlannedPathMoves:        plannedPathMoves,
		ConflictAhead:           conflictAhead,
		MaxVersionDrift:         maxVersionDrift,
		LocalAttachmentAdded:    localAttachmentAdded,
		LocalAttachmentDeleted:  localAttachmentDeleted,
		RemoteAttachmentAdded:   remoteAttachmentAdded,
		RemoteAttachmentDeleted: remoteAttachmentDeleted,
		OrphanedLocalAssets:     orphanedLocalAssets,
	}, nil
}

func collectAttachmentStatus(
	ctx context.Context,
	remote StatusRemote,
	target config.Target,
	spaceDir string,
	state fs.SpaceState,
	trackedPathByID map[string]string,
	targetRelPath string,
) ([]string, []string, []string, []string, []string, error) {
	targetCtx, err := resolveValidateTargetContext(target, spaceDir)
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("resolve attachment status target: %w", err)
	}

	trackedStateByPageID := map[string]map[string]string{}
	attachmentIDToPath := map[string]string{}
	for relPath, attachmentID := range state.AttachmentIndex {
		pageID := pageIDFromAttachmentPath(relPath)
		if pageID == "" {
			continue
		}
		if targetRelPath != "" {
			if trackedPath, ok := trackedPathByID[pageID]; !ok || trackedPath != targetRelPath {
				continue
			}
		}
		if trackedStateByPageID[pageID] == nil {
			trackedStateByPageID[pageID] = map[string]string{}
		}
		trackedStateByPageID[pageID][normalizeRepoRelPath(relPath)] = strings.TrimSpace(attachmentID)
		attachmentIDToPath[strings.TrimSpace(attachmentID)] = normalizeRepoRelPath(relPath)
	}

	localAdded := map[string]struct{}{}
	localDeleted := map[string]struct{}{}
	referencedByPage := map[string]map[string]struct{}{}
	for _, file := range targetCtx.files {
		relPath, relErr := filepath.Rel(spaceDir, file)
		if relErr != nil {
			continue
		}
		relPath = normalizeRepoRelPath(relPath)
		doc, readErr := fs.ReadMarkdownDocument(file)
		if readErr != nil {
			continue
		}
		pageID := strings.TrimSpace(doc.Frontmatter.ID)
		if pageID == "" {
			pageID = strings.TrimSpace(state.PagePathIndex[relPath])
		}
		if pageID == "" {
			continue
		}
		if referencedByPage[pageID] == nil {
			referencedByPage[pageID] = map[string]struct{}{}
		}
		referencedPaths, refErr := syncflow.CollectReferencedAssetPaths(spaceDir, file, doc.Body)
		if refErr != nil {
			continue
		}
		for _, assetPath := range referencedPaths {
			plannedKey := normalizeRepoRelPath(filepath.ToSlash(filepath.Join("assets", pageID, filepath.Base(assetPath))))
			referencedByPage[pageID][plannedKey] = struct{}{}
			if strings.TrimSpace(state.AttachmentIndex[plannedKey]) == "" {
				localAdded[plannedKey] = struct{}{}
				continue
			}
			if _, statErr := os.Stat(filepath.Join(spaceDir, filepath.FromSlash(plannedKey))); os.IsNotExist(statErr) {
				localDeleted[plannedKey] = struct{}{}
			}
		}
	}

	for pageID, statePaths := range trackedStateByPageID {
		referencedSet := referencedByPage[pageID]
		for relPath := range statePaths {
			if referencedSet == nil {
				localDeleted[relPath] = struct{}{}
				continue
			}
			if _, exists := referencedSet[relPath]; !exists {
				localDeleted[relPath] = struct{}{}
			}
		}
	}

	remoteAdded := map[string]struct{}{}
	remoteDeleted := map[string]struct{}{}
	for pageID, trackedState := range trackedStateByPageID {
		remoteAttachments, listErr := remote.ListAttachments(ctx, pageID)
		if listErr != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("list remote attachments for page %s: %w", pageID, listErr)
		}
		remoteIDs := map[string]confluence.Attachment{}
		for _, attachment := range remoteAttachments {
			attachmentID := strings.TrimSpace(attachment.ID)
			if attachmentID == "" {
				continue
			}
			remoteIDs[attachmentID] = attachment
			if _, tracked := attachmentIDToPath[attachmentID]; !tracked {
				remoteAdded[normalizeRepoRelPath(filepath.ToSlash(filepath.Join("assets", pageID, attachmentID+"-"+strings.TrimSpace(attachment.Filename))))] = struct{}{}
			}
		}
		for relPath, attachmentID := range trackedState {
			if _, exists := remoteIDs[attachmentID]; !exists {
				remoteDeleted[relPath] = struct{}{}
			}
		}
	}

	orphaned := []string{}
	assetsRoot := filepath.Join(spaceDir, "assets")
	if _, statErr := os.Stat(assetsRoot); statErr == nil {
		walkErr := filepath.WalkDir(assetsRoot, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return walkErr
			}
			relPath, relErr := filepath.Rel(spaceDir, path)
			if relErr != nil {
				return relErr
			}
			normalized := normalizeRepoRelPath(relPath)
			if strings.TrimSpace(state.AttachmentIndex[normalized]) == "" {
				orphaned = append(orphaned, normalized)
			}
			return nil
		})
		if walkErr != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("walk local assets: %w", walkErr)
		}
	}

	return sortedStatusSet(localAdded), sortedStatusSet(localDeleted), sortedStatusSet(remoteAdded), sortedStatusSet(remoteDeleted), dedupeSortedStatusPaths(orphaned), nil
}

func dedupeSortedStatusPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		if len(out) > 0 && out[len(out)-1] == path {
			continue
		}
		out = append(out, path)
	}
	return out
}

func sortedStatusSet(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func listAllPagesForStatus(ctx context.Context, remote StatusRemote, opts confluence.PageListOptions) ([]confluence.Page, error) {
	pages := make([]confluence.Page, 0)
	cursor := opts.Cursor
	iterations := 0
	for {
		if iterations >= maxPaginationIterations {
			return nil, fmt.Errorf("pagination loop exceeded %d iterations for space %s", maxPaginationIterations, opts.SpaceID)
		}
		iterations++
		opts.Cursor = cursor
		result, err := remote.ListPages(ctx, opts)
		if err != nil {
			return nil, err
		}
		pages = append(pages, result.Pages...)
		if strings.TrimSpace(result.NextCursor) == "" || result.NextCursor == cursor {
			break
		}
		cursor = result.NextCursor
	}
	return pages, nil
}

func collectLocalStatusChanges(target config.Target, spaceDir, spaceKey string) ([]string, []string, []string, error) {
	client, err := git.NewClient()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("init git client: %w", err)
	}

	baselineRef, err := gitPushBaselineRef(client, spaceKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve sync baseline: %w", err)
	}

	targetCtx, err := resolveValidateTargetContext(target, "")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve target context: %w", err)
	}
	spaceScopePath, err := gitScopePathFromPath(spaceDir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve git scope for %s: %w", spaceDir, err)
	}
	changeScopePath, err := resolvePushScopePath(client, spaceDir, target, targetCtx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve change scope: %w", err)
	}

	changes, err := collectPushChangesForTarget(client, baselineRef, target, spaceScopePath, changeScopePath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("collect local changes: %w", err)
	}

	added := make([]string, 0)
	modified := make([]string, 0)
	deleted := make([]string, 0)
	for _, change := range changes {
		switch change.Type {
		case syncflow.PushChangeAdd:
			added = append(added, change.Path)
		case syncflow.PushChangeModify:
			modified = append(modified, change.Path)
		case syncflow.PushChangeDelete:
			deleted = append(deleted, change.Path)
		}
	}

	return added, modified, deleted, nil
}

func printStatusSection(out io.Writer, title string, added, modified, deleted []string) {
	_, _ = fmt.Fprintf(out, "\n%s:\n", title)
	printStatusList(out, "added", added)
	printStatusList(out, "modified", modified)
	printStatusList(out, "deleted", deleted)
}

func printStatusList(out io.Writer, label string, items []string) {
	if len(items) == 0 {
		_, _ = fmt.Fprintf(out, "  %s (0)\n", label)
		return
	}
	_, _ = fmt.Fprintf(out, "  %s (%d):\n", label, len(items))
	for _, item := range items {
		_, _ = fmt.Fprintf(out, "    - %s\n", item)
	}
}

func printPlannedPathMoves(out io.Writer, moves []syncflow.PlannedPagePathMove) {
	if len(moves) == 0 {
		return
	}
	_, _ = fmt.Fprintf(out, "\nPlanned path moves (%d) — next pull would relocate tracked markdown:\n", len(moves))
	for _, move := range moves {
		_, _ = fmt.Fprintf(out, "  - %s -> %s\n", move.PreviousPath, move.PlannedPath)
	}
}

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, confluence.ErrNotFound) {
		return true
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "not found") {
		return true
	}
	return false
}

// computeConflictAhead returns paths that appear in both localModified and
// remoteModified — these pages have local uncommitted edits AND are behind
// on the remote, making them prime conflict candidates.
func computeConflictAhead(localModified, remoteModified []string) []string {
	if len(localModified) == 0 || len(remoteModified) == 0 {
		return nil
	}
	remoteSet := make(map[string]struct{}, len(remoteModified))
	for _, p := range remoteModified {
		remoteSet[p] = struct{}{}
	}
	var result []string
	for _, p := range localModified {
		if _, ok := remoteSet[p]; ok {
			result = append(result, p)
		}
	}
	return result
}
