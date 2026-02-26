package cmd

import (
	"context"
	"fmt"
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

type checkSyncRemote interface {
	GetSpace(ctx context.Context, spaceKey string) (confluence.Space, error)
	ListPages(ctx context.Context, opts confluence.PageListOptions) (confluence.PageListResult, error)
	GetPage(ctx context.Context, pageID string) (confluence.Page, error)
}

type checkSyncReport struct {
	LocalAdded      []string
	LocalModified   []string
	LocalDeleted    []string
	RemoteAdded     []string
	RemoteModified  []string
	RemoteDeleted   []string
	MaxVersionDrift int
}

var newCheckSyncRemote = func(cfg *config.Config) (checkSyncRemote, error) {
	return newConfluenceClientFromConfig(cfg)
}

func newCheckSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "check-sync [TARGET]",
		Aliases: []string{"status"},
		Short:   "Inspect local and remote sync drift",
		Long: `check-sync prints a high-level sync summary without mutating local files or remote content.

TARGET follows the standard rule:
- .md suffix => file mode (space inferred from file)
- otherwise => space mode (SPACE_KEY or space directory).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			return runCheckSync(cmd, config.ParseTarget(raw))
		},
	}

	return cmd
}

func runCheckSync(cmd *cobra.Command, target config.Target) error {
	if err := ensureWorkspaceSyncReady("check-sync"); err != nil {
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

	remote, err := newCheckSyncRemote(cfg)
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

	report, err := buildCheckSyncReport(ctx, remote, target, initialCtx, state, spaceKey, targetRelPath)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "Space: %s\n", spaceKey)
	_, _ = fmt.Fprintf(out, "Directory: %s\n", initialCtx.spaceDir)

	printCheckSyncSection(out, "Local not pushed", report.LocalAdded, report.LocalModified, report.LocalDeleted)
	printCheckSyncSection(out, "Remote not pulled", report.RemoteAdded, report.RemoteModified, report.RemoteDeleted)

	if report.MaxVersionDrift > 0 {
		_, _ = fmt.Fprintf(out, "\nVersion drift: local markdown is up to %d version(s) behind remote\n", report.MaxVersionDrift)
	} else {
		_, _ = fmt.Fprintln(out, "\nVersion drift: no remote-ahead tracked pages")
	}

	return nil
}

func buildCheckSyncReport(
	ctx context.Context,
	remote checkSyncRemote,
	target config.Target,
	initialCtx initialPullContext,
	state fs.SpaceState,
	spaceKey string,
	targetRelPath string,
) (checkSyncReport, error) {
	localAdded, localModified, localDeleted, err := collectLocalCheckSyncChanges(target, initialCtx.spaceDir, spaceKey)
	if err != nil {
		return checkSyncReport{}, err
	}

	space, err := remote.GetSpace(ctx, spaceKey)
	if err != nil {
		return checkSyncReport{}, fmt.Errorf("fetch space %s: %w", spaceKey, err)
	}

	remotePages, err := listAllPagesForCheckSync(ctx, remote, confluence.PageListOptions{SpaceID: space.ID, SpaceKey: space.Key, Status: "current", Limit: 100})
	if err != nil {
		return checkSyncReport{}, fmt.Errorf("list remote pages: %w", err)
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

	return checkSyncReport{
		LocalAdded:      localAdded,
		LocalModified:   localModified,
		LocalDeleted:    localDeleted,
		RemoteAdded:     remoteAdded,
		RemoteModified:  remoteModified,
		RemoteDeleted:   remoteDeleted,
		MaxVersionDrift: maxVersionDrift,
	}, nil
}

func listAllPagesForCheckSync(ctx context.Context, remote checkSyncRemote, opts confluence.PageListOptions) ([]confluence.Page, error) {
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

func collectLocalCheckSyncChanges(target config.Target, spaceDir, spaceKey string) ([]string, []string, []string, error) {
	client, err := git.NewClient()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("init git client: %w", err)
	}

	baselineRef, err := gitPushBaselineRef(client, spaceKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve sync baseline: %w", err)
	}

	targetCtx, err := resolveValidateTargetContext(target)
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

func printCheckSyncSection(out interface{ Write([]byte) (int, error) }, title string, added, modified, deleted []string) {
	_, _ = fmt.Fprintf(out, "\n%s:\n", title)
	printCheckSyncList(out, "added", added)
	printCheckSyncList(out, "modified", modified)
	printCheckSyncList(out, "deleted", deleted)
}

func printCheckSyncList(out interface{ Write([]byte) (int, error) }, label string, items []string) {
	if len(items) == 0 {
		_, _ = fmt.Fprintf(out, "  %s (0)\n", label)
		return
	}
	_, _ = fmt.Fprintf(out, "  %s (%d):\n", label, len(items))
	for _, item := range items {
		_, _ = fmt.Fprintf(out, "    - %s\n", item)
	}
}

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "not found") {
		return true
	}
	return err == confluence.ErrNotFound
}
