package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

var (
	newPullRemote = func(cfg *config.Config) (syncflow.PullRemote, error) {
		return confluence.NewClient(confluence.ClientConfig{
			BaseURL:  cfg.Domain,
			Email:    cfg.Email,
			APIToken: cfg.APIToken,
		})
	}
	nowUTC = func() time.Time {
		return time.Now().UTC()
	}
)

type pullContext struct {
	spaceKey     string
	spaceDir     string
	targetPageID string
}

func newPullCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pull [TARGET]",
		Short: "Pull Confluence pages to local Markdown files",
		Long: `Pull fetches Confluence pages and converts them to local Markdown files.

TARGET can be a SPACE_KEY (e.g. "MYSPACE") or a path to a .md file.
If omitted, the space is inferred from the current directory name.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			return runPull(cmd, config.ParseTarget(raw))
		},
	}
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Auto-approve safety confirmations")
	cmd.Flags().BoolVar(&flagNonInteractive, "non-interactive", false, "Disable prompts; fail fast when a decision is required")
	return cmd
}

func runPull(cmd *cobra.Command, target config.Target) (runErr error) {
	ctx := context.Background()
	out := cmd.OutOrStdout()

	pullCtx, err := resolvePullContext(target)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(pullCtx.spaceDir, 0o755); err != nil {
		return fmt.Errorf("prepare space directory: %w", err)
	}

	envPath := findEnvPath(pullCtx.spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	remote, err := newPullRemote(cfg)
	if err != nil {
		return fmt.Errorf("create confluence client: %w", err)
	}

	state, err := fs.LoadState(pullCtx.spaceDir)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	impact, err := estimatePullImpact(ctx, remote, pullCtx.spaceKey, pullCtx.targetPageID, state, syncflow.DefaultPullOverlapWindow)
	if err != nil {
		return err
	}

	affectedCount := impact.changedMarkdown + impact.deletedMarkdown
	if err := requireSafetyConfirmation(cmd.InOrStdin(), out, "pull", affectedCount, impact.deletedMarkdown > 0); err != nil {
		return err
	}

	repoRoot, err := gitRepoRoot()
	if err != nil {
		return err
	}

	scopePath, err := gitScopePath(repoRoot, pullCtx.spaceDir)
	if err != nil {
		return err
	}

	pullStartedAt := nowUTC()
	stashRef, err := stashScopeIfDirty(repoRoot, scopePath, pullCtx.spaceKey, pullStartedAt)
	if err != nil {
		return err
	}
	if stashRef != "" {
		defer func() {
			restoreErr := applyAndDropStash(repoRoot, stashRef)
			if restoreErr != nil {
				runErr = errors.Join(runErr, restoreErr)
			}
		}()
	}

	result, err := syncflow.Pull(ctx, remote, syncflow.PullOptions{
		SpaceKey:      pullCtx.spaceKey,
		SpaceDir:      pullCtx.spaceDir,
		State:         state,
		PullStartedAt: pullStartedAt,
		OverlapWindow: syncflow.DefaultPullOverlapWindow,
		TargetPageID:  pullCtx.targetPageID,
	})
	if err != nil {
		return err
	}

	if err := fs.SaveState(pullCtx.spaceDir, result.State); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	for _, diag := range result.Diagnostics {
		fmt.Fprintf(out, "warning: %s [%s] %s\n", diag.Path, diag.Code, diag.Message)
	}

	if _, err := runGit(repoRoot, "add", "--", scopePath); err != nil {
		return err
	}

	hasChanges, err := gitHasScopedStagedChanges(repoRoot, scopePath)
	if err != nil {
		return err
	}

	if !hasChanges {
		fmt.Fprintln(out, "pull completed with no scoped changes (no-op)")
		return nil
	}

	commitMsg := fmt.Sprintf("Sync from Confluence: [%s] (v%d)", pullCtx.spaceKey, result.MaxVersion)
	if _, err := runGit(repoRoot, "commit", "-m", commitMsg); err != nil {
		return err
	}

	ts := pullStartedAt.UTC().Format("20060102T150405Z")
	tagName := fmt.Sprintf("confluence-sync/pull/%s/%s", pullCtx.spaceKey, ts)
	tagMsg := fmt.Sprintf("Confluence pull sync for %s at %s", pullCtx.spaceKey, ts)
	if _, err := runGit(repoRoot, "tag", "-a", tagName, "-m", tagMsg); err != nil {
		return err
	}

	fmt.Fprintf(out, "pull completed: committed and tagged %s\n", tagName)
	return nil
}

func resolvePullContext(target config.Target) (pullContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return pullContext{}, err
	}

	if target.IsFile() {
		absPath, err := filepath.Abs(target.Value)
		if err != nil {
			return pullContext{}, err
		}

		doc, err := fs.ReadMarkdownDocument(absPath)
		if err != nil {
			return pullContext{}, fmt.Errorf("read target file %s: %w", target.Value, err)
		}

		spaceKey := strings.TrimSpace(doc.Frontmatter.ConfluenceSpaceKey)
		if spaceKey == "" {
			return pullContext{}, fmt.Errorf("target file %s missing confluence_space_key", target.Value)
		}
		pageID := strings.TrimSpace(doc.Frontmatter.ConfluencePageID)
		if pageID == "" {
			return pullContext{}, fmt.Errorf("target file %s missing confluence_page_id", target.Value)
		}

		return pullContext{
			spaceKey:     spaceKey,
			spaceDir:     findSpaceDirFromFile(absPath, spaceKey),
			targetPageID: pageID,
		}, nil
	}

	if target.Value == "" {
		spaceDir, err := filepath.Abs(cwd)
		if err != nil {
			return pullContext{}, err
		}
		return pullContext{
			spaceKey: filepath.Base(spaceDir),
			spaceDir: spaceDir,
		}, nil
	}

	if info, statErr := os.Stat(target.Value); statErr == nil && info.IsDir() {
		spaceDir, err := filepath.Abs(target.Value)
		if err != nil {
			return pullContext{}, err
		}
		return pullContext{
			spaceKey: filepath.Base(spaceDir),
			spaceDir: spaceDir,
		}, nil
	}

	spaceDir := filepath.Join(cwd, target.Value)
	spaceDir, err = filepath.Abs(spaceDir)
	if err != nil {
		return pullContext{}, err
	}

	return pullContext{
		spaceKey: target.Value,
		spaceDir: spaceDir,
	}, nil
}

func findSpaceDirFromFile(filePath, spaceKey string) string {
	dir := filepath.Dir(filePath)
	for {
		if filepath.Base(dir) == spaceKey {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, fs.StateFileName)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Dir(filePath)
}

func findEnvPath(startDir string) string {
	dir := startDir
	for {
		candidate := filepath.Join(dir, ".env")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return filepath.Join(startDir, ".env")
}

func gitRepoRoot() (string, error) {
	root, err := runGit("", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("pull requires a git repository: %w", err)
	}
	return strings.TrimSpace(root), nil
}

func gitScopePath(repoRoot, scopeDir string) (string, error) {
	rel, err := filepath.Rel(repoRoot, scopeDir)
	if err != nil {
		return "", err
	}
	rel = filepath.Clean(rel)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("space directory %s is outside repository root %s", scopeDir, repoRoot)
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return ".", nil
	}
	return rel, nil
}

func stashScopeIfDirty(repoRoot, scopePath, spaceKey string, ts time.Time) (string, error) {
	status, err := runGit(repoRoot, "status", "--porcelain", "--", scopePath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(status) == "" {
		return "", nil
	}

	message := fmt.Sprintf("Auto-stash %s %s", spaceKey, ts.UTC().Format(time.RFC3339))
	if _, err := runGit(repoRoot, "stash", "push", "--include-untracked", "-m", message, "--", scopePath); err != nil {
		return "", err
	}

	ref, err := runGit(repoRoot, "stash", "list", "-1", "--format=%gd")
	if err != nil {
		return "", err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("failed to capture stash reference")
	}
	return ref, nil
}

func applyAndDropStash(repoRoot, stashRef string) error {
	if stashRef == "" {
		return nil
	}
	if _, err := runGit(repoRoot, "stash", "apply", "--index", stashRef); err != nil {
		return fmt.Errorf("failed to restore stashed changes (%s): %w", stashRef, err)
	}
	if _, err := runGit(repoRoot, "stash", "drop", stashRef); err != nil {
		return fmt.Errorf("restored stash but failed to drop %s: %w", stashRef, err)
	}
	return nil
}

func gitHasScopedStagedChanges(repoRoot, scopePath string) (bool, error) {
	cmd := exec.Command("git", "diff", "--cached", "--quiet", "--", scopePath)
	cmd.Dir = repoRoot
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("check staged changes: %w", err)
}

type pullImpact struct {
	changedMarkdown int
	deletedMarkdown int
}

func estimatePullImpact(
	ctx context.Context,
	remote syncflow.PullRemote,
	spaceKey string,
	targetPageID string,
	state fs.SpaceState,
	overlapWindow time.Duration,
) (pullImpact, error) {
	space, err := remote.GetSpace(ctx, spaceKey)
	if err != nil {
		return pullImpact{}, fmt.Errorf("resolve space %q: %w", spaceKey, err)
	}

	pages, err := listAllPullPagesForEstimate(ctx, remote, confluence.PageListOptions{
		SpaceID:  space.ID,
		SpaceKey: spaceKey,
		Status:   "current",
		Limit:    100,
	})
	if err != nil {
		return pullImpact{}, fmt.Errorf("list pages for safety check: %w", err)
	}

	pageByID := make(map[string]confluence.Page, len(pages))
	for _, page := range pages {
		pageByID[page.ID] = page
	}

	targetPageID = strings.TrimSpace(targetPageID)
	if targetPageID != "" {
		if _, exists := pageByID[targetPageID]; !exists {
			return pullImpact{}, nil
		}
		return pullImpact{changedMarkdown: 1}, nil
	}

	changedIDs := map[string]struct{}{}
	if strings.TrimSpace(state.LastPullHighWatermark) == "" {
		for _, page := range pages {
			changedIDs[page.ID] = struct{}{}
		}
	} else {
		watermark, err := time.Parse(time.RFC3339, strings.TrimSpace(state.LastPullHighWatermark))
		if err != nil {
			return pullImpact{}, fmt.Errorf("parse last_pull_high_watermark: %w", err)
		}

		since := watermark.Add(-overlapWindow)
		changes, err := listAllPullChangesForEstimate(ctx, remote, confluence.ChangeListOptions{
			SpaceKey: spaceKey,
			Since:    since,
			Limit:    100,
		})
		if err != nil {
			return pullImpact{}, fmt.Errorf("list incremental changes for safety check: %w", err)
		}

		for _, change := range changes {
			if _, exists := pageByID[change.PageID]; exists {
				changedIDs[change.PageID] = struct{}{}
			}
		}
	}

	deletedIDs := map[string]struct{}{}
	for _, pageID := range state.PagePathIndex {
		if _, exists := pageByID[pageID]; !exists {
			deletedIDs[pageID] = struct{}{}
		}
	}

	return pullImpact{
		changedMarkdown: len(changedIDs),
		deletedMarkdown: len(deletedIDs),
	}, nil
}

func listAllPullPagesForEstimate(
	ctx context.Context,
	remote syncflow.PullRemote,
	opts confluence.PageListOptions,
) ([]confluence.Page, error) {
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

func listAllPullChangesForEstimate(
	ctx context.Context,
	remote syncflow.PullRemote,
	opts confluence.ChangeListOptions,
) ([]confluence.Change, error) {
	result := []confluence.Change{}
	start := opts.Start
	for {
		opts.Start = start
		changeResult, err := remote.ListChanges(ctx, opts)
		if err != nil {
			return nil, err
		}
		result = append(result, changeResult.Changes...)
		if !changeResult.HasMore {
			break
		}
		next := changeResult.NextStart
		if next <= start {
			next = start + opts.Limit
		}
		if next <= start {
			break
		}
		start = next
	}
	return result, nil
}

func runGit(workdir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if strings.TrimSpace(workdir) != "" {
		cmd.Dir = workdir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return "", fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), msg)
	}
	return string(out), nil
}
