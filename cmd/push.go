package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

// conflict policy values for --on-conflict.
const (
	OnConflictPullMerge = "pull-merge"
	OnConflictForce     = "force"
	OnConflictCancel    = "cancel"
)

var newPushRemote = func(cfg *config.Config) (syncflow.PushRemote, error) {
	return confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})
}

func newPushCmd() *cobra.Command {
	var onConflict string

	cmd := &cobra.Command{
		Use:   "push [TARGET]",
		Short: "Push local Markdown changes to Confluence",
		Long: `Push converts local Markdown files to ADF and updates Confluence pages.

TARGET can be a SPACE_KEY (e.g. "MYSPACE") or a path to a .md file.
If omitted, the space is inferred from the current directory name.

push always runs validate before any remote write.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			return runPush(cmd, config.ParseTarget(raw), onConflict)
		},
	}
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Auto-approve safety confirmations")
	cmd.Flags().BoolVar(&flagNonInteractive, "non-interactive", false, "Disable prompts; fail fast when a decision is required")
	cmd.Flags().StringVar(&onConflict, "on-conflict", "", "Non-interactive conflict policy: pull-merge|force|cancel")
	return cmd
}

func validateOnConflict(v string) error {
	if v == "" {
		return nil
	}
	switch v {
	case OnConflictPullMerge, OnConflictForce, OnConflictCancel:
		return nil
	default:
		return fmt.Errorf("invalid --on-conflict value %q: must be pull-merge, force, or cancel", v)
	}
}

type pushFileChange struct {
	Status string
	Path   string
}

func runPush(cmd *cobra.Command, target config.Target, onConflict string) error {
	ctx := context.Background()
	out := cmd.OutOrStdout()

	if err := validateOnConflict(onConflict); err != nil {
		return err
	}

	if err := runValidateTarget(out, target); err != nil {
		return fmt.Errorf("pre-push validate failed: %w", err)
	}

	targetCtx, err := resolveValidateTargetContext(target)
	if err != nil {
		return err
	}
	spaceDir := targetCtx.spaceDir
	spaceKey := filepath.Base(spaceDir)

	repoRoot, err := gitRepoRoot()
	if err != nil {
		return err
	}

	spaceScopePath, err := gitScopePath(repoRoot, spaceDir)
	if err != nil {
		return err
	}

	changeScopePath, err := resolvePushScopePath(repoRoot, spaceDir, target, targetCtx)
	if err != nil {
		return err
	}

	baselineRef, err := gitPushBaselineRef(repoRoot, spaceKey)
	if err != nil {
		return err
	}

	changes, err := gitChangedMarkdownSince(repoRoot, changeScopePath, baselineRef)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		fmt.Fprintln(out, "push completed with no in-scope markdown changes (no-op)")
		return nil
	}

	envPath := findEnvPath(spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	remote, err := newPushRemote(cfg)
	if err != nil {
		return fmt.Errorf("create confluence client: %w", err)
	}

	state, err := fs.LoadState(spaceDir)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	syncChanges, err := toSyncPushChanges(changes, repoRoot, spaceDir)
	if err != nil {
		return err
	}

	result, err := syncflow.Push(ctx, remote, syncflow.PushOptions{
		SpaceKey:       spaceKey,
		SpaceDir:       spaceDir,
		Domain:         cfg.Domain,
		State:          state,
		Changes:        syncChanges,
		ConflictPolicy: toSyncConflictPolicy(onConflict),
	})
	if err != nil {
		var conflictErr *syncflow.PushConflictError
		if errors.As(err, &conflictErr) {
			return formatPushConflictError(conflictErr)
		}
		return err
	}

	if len(result.Commits) == 0 {
		fmt.Fprintln(out, "push completed with no pushable markdown changes (no-op)")
		return nil
	}

	if err := fs.SaveState(spaceDir, result.State); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	for i, commitPlan := range result.Commits {
		repoPaths := make([]string, 0, len(commitPlan.StagedPaths)+1)
		for _, relPath := range commitPlan.StagedPaths {
			repoPaths = append(repoPaths, joinRepoScopePath(spaceScopePath, relPath))
		}
		if i == len(result.Commits)-1 {
			repoPaths = append(repoPaths, joinRepoScopePath(spaceScopePath, fs.StateFileName))
		}
		repoPaths = dedupeRepoPaths(repoPaths)

		addArgs := append([]string{"add", "--"}, repoPaths...)
		if _, err := runGit(repoRoot, addArgs...); err != nil {
			return err
		}

		hasChanges, err := gitHasScopedStagedChanges(repoRoot, spaceScopePath)
		if err != nil {
			return err
		}
		if !hasChanges {
			continue
		}

		subject := fmt.Sprintf("Sync %q to Confluence (v%d)", commitPlan.PageTitle, commitPlan.Version)
		body := fmt.Sprintf(
			"Page ID: %s\nURL: %s\n\nConfluence-Page-ID: %s\nConfluence-Version: %d\nConfluence-Space-Key: %s\nConfluence-URL: %s",
			commitPlan.PageID,
			commitPlan.URL,
			commitPlan.PageID,
			commitPlan.Version,
			commitPlan.SpaceKey,
			commitPlan.URL,
		)
		if _, err := runGit(repoRoot, "commit", "-m", subject, "-m", body); err != nil {
			return err
		}

		fmt.Fprintf(out, "pushed %s (page %s, v%d)\n", commitPlan.Path, commitPlan.PageID, commitPlan.Version)
	}

	fmt.Fprintf(out, "push completed: %d page change(s) synced\n", len(result.Commits))
	return nil
}

func resolvePushScopePath(repoRoot, spaceDir string, target config.Target, targetCtx validateTargetContext) (string, error) {
	if target.IsFile() {
		if len(targetCtx.files) != 1 {
			return "", fmt.Errorf("expected one file target, got %d", len(targetCtx.files))
		}
		return gitScopePath(repoRoot, targetCtx.files[0])
	}
	return gitScopePath(repoRoot, spaceDir)
}

func toSyncPushChanges(changes []pushFileChange, repoRoot, spaceDir string) ([]syncflow.PushFileChange, error) {
	out := make([]syncflow.PushFileChange, 0, len(changes))
	for _, change := range changes {
		absPath := filepath.Join(repoRoot, filepath.FromSlash(change.Path))
		relPath, err := filepath.Rel(spaceDir, absPath)
		if err != nil {
			return nil, err
		}
		relPath = filepath.ToSlash(relPath)
		if relPath == "." || strings.HasPrefix(relPath, "../") {
			continue
		}

		var changeType syncflow.PushChangeType
		switch change.Status {
		case "A":
			changeType = syncflow.PushChangeAdd
		case "M", "T":
			changeType = syncflow.PushChangeModify
		case "D":
			changeType = syncflow.PushChangeDelete
		default:
			continue
		}

		out = append(out, syncflow.PushFileChange{Type: changeType, Path: relPath})
	}
	return out, nil
}

func toSyncConflictPolicy(policy string) syncflow.PushConflictPolicy {
	switch policy {
	case OnConflictPullMerge:
		return syncflow.PushConflictPolicyPullMerge
	case OnConflictForce:
		return syncflow.PushConflictPolicyForce
	case OnConflictCancel:
		return syncflow.PushConflictPolicyCancel
	default:
		return syncflow.PushConflictPolicyCancel
	}
}

func formatPushConflictError(conflictErr *syncflow.PushConflictError) error {
	switch conflictErr.Policy {
	case syncflow.PushConflictPolicyPullMerge:
		return fmt.Errorf(
			"conflict for %s (remote v%d > local v%d): pull-merge policy selected; run cms pull and merge local changes before retrying push",
			conflictErr.Path,
			conflictErr.RemoteVersion,
			conflictErr.LocalVersion,
		)
	case syncflow.PushConflictPolicyForce:
		return conflictErr
	default:
		return fmt.Errorf(
			"conflict for %s (remote v%d > local v%d): rerun with --on-conflict=force to overwrite or --on-conflict=pull-merge to reconcile",
			conflictErr.Path,
			conflictErr.RemoteVersion,
			conflictErr.LocalVersion,
		)
	}
}

func joinRepoScopePath(scopePath, relPath string) string {
	relPath = filepath.ToSlash(filepath.Clean(filepath.FromSlash(relPath)))
	if scopePath == "." {
		return relPath
	}
	return filepath.ToSlash(filepath.Join(filepath.FromSlash(scopePath), filepath.FromSlash(relPath)))
}

func dedupeRepoPaths(paths []string) []string {
	set := map[string]struct{}{}
	for _, path := range paths {
		path = filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
		if path == "." || path == "" {
			continue
		}
		set[path] = struct{}{}
	}
	ordered := make([]string, 0, len(set))
	for path := range set {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	return ordered
}

func gitPushBaselineRef(repoRoot, spaceKey string) (string, error) {
	spaceKey = strings.TrimSpace(spaceKey)
	if spaceKey == "" {
		return "", fmt.Errorf("space key is required")
	}

	tagsRaw, err := runGit(
		repoRoot,
		"tag",
		"--list",
		fmt.Sprintf("confluence-sync/pull/%s/*", spaceKey),
		fmt.Sprintf("confluence-sync/push/%s/*", spaceKey),
	)
	if err != nil {
		return "", err
	}

	bestTag := ""
	bestStamp := ""
	for _, line := range strings.Split(strings.ReplaceAll(tagsRaw, "\r\n", "\n"), "\n") {
		tag := strings.TrimSpace(line)
		if tag == "" {
			continue
		}
		parts := strings.Split(tag, "/")
		if len(parts) < 4 {
			continue
		}
		timestamp := parts[len(parts)-1]
		if timestamp > bestStamp {
			bestStamp = timestamp
			bestTag = tag
		}
	}
	if bestTag != "" {
		return bestTag, nil
	}

	rootCommitRaw, err := runGit(repoRoot, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return "", err
	}
	lines := strings.Fields(rootCommitRaw)
	if len(lines) == 0 {
		return "", fmt.Errorf("unable to determine baseline commit")
	}
	return lines[0], nil
}

func gitChangedMarkdownSince(repoRoot, scopePath, baselineRef string) ([]pushFileChange, error) {
	rangeExpr := fmt.Sprintf("%s..HEAD", strings.TrimSpace(baselineRef))
	out, err := runGit(repoRoot, "diff", "--name-status", rangeExpr, "--", scopePath)
	if err != nil {
		return nil, err
	}

	changes := make([]pushFileChange, 0)
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		status := parts[0]
		switch {
		case strings.HasPrefix(status, "R"):
			if len(parts) < 3 {
				continue
			}
			oldPath := filepath.ToSlash(parts[1])
			newPath := filepath.ToSlash(parts[2])
			if strings.HasSuffix(strings.ToLower(oldPath), ".md") {
				changes = append(changes, pushFileChange{Status: "D", Path: oldPath})
			}
			if strings.HasSuffix(strings.ToLower(newPath), ".md") {
				changes = append(changes, pushFileChange{Status: "A", Path: newPath})
			}
		default:
			path := filepath.ToSlash(parts[1])
			if !strings.HasSuffix(strings.ToLower(path), ".md") {
				continue
			}
			changes = append(changes, pushFileChange{Status: string(status[0]), Path: path})
		}
	}

	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path == changes[j].Path {
			return changes[i].Status < changes[j].Status
		}
		return changes[i].Path < changes[j].Path
	})

	return changes, nil
}
