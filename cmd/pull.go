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
	flagPullForce        = false
	flagPullDiscardLocal = false

	newPullRemote = func(cfg *config.Config) (syncflow.PullRemote, error) {
		return confluence.NewClient(confluence.ClientConfig{
			BaseURL:  cfg.Domain,
			Email:    cfg.Email,
			APIToken: cfg.APIToken,
			Verbose:  flagVerbose,
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
	cmd.Flags().BoolVarP(&flagSkipMissingAssets, "skip-missing-assets", "s", false, "Continue if an attachment is missing (not found)")
	cmd.Flags().BoolVarP(&flagPullForce, "force", "f", false, "Force full space pull and refresh all tracked pages")
	cmd.Flags().BoolVar(&flagPullDiscardLocal, "discard-local", false, "Discard local uncommitted changes if they conflict with remote updates")
	return cmd
}

func runPull(cmd *cobra.Command, target config.Target) (runErr error) {
	ctx := context.Background()
	out := cmd.OutOrStdout()

	// 1. Initial resolution of key/dir
	initialCtx, err := resolveInitialPullContext(target)
	if err != nil {
		return err
	}

	// 2. Load config to talk to Confluence
	envPath := findEnvPath(initialCtx.spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	remote, err := newPullRemote(cfg)
	if err != nil {
		return fmt.Errorf("create confluence client: %w", err)
	}

	// 3. Resolve actual space metadata and final directory
	space, err := remote.GetSpace(ctx, initialCtx.spaceKey)
	if err != nil {
		return fmt.Errorf("resolve space %q: %w", initialCtx.spaceKey, err)
	}

	// Finalize space directory based on Space Name if we are creating it,
	// or if we found it via state file.
	spaceDir := initialCtx.spaceDir
	if !initialCtx.fixedDir {
		// If not already in a tracked directory, use sanitized "Name (KEY)"
		spaceDir = filepath.Join(filepath.Dir(initialCtx.spaceDir), fs.SanitizeSpaceDirName(space.Name, space.Key))
	}
	pullCtx := pullContext{
		spaceKey:     space.Key,
		spaceDir:     spaceDir,
		targetPageID: initialCtx.targetPageID,
	}

	if flagPullForce && strings.TrimSpace(pullCtx.targetPageID) != "" {
		return errors.New("--force is only supported for space targets")
	}
	scopeDirExisted := dirExists(pullCtx.spaceDir)

	if err := os.MkdirAll(pullCtx.spaceDir, 0o755); err != nil {
		return fmt.Errorf("prepare space directory: %w", err)
	}

	state, err := fs.LoadState(pullCtx.spaceDir)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	impact, err := estimatePullImpactWithSpace(ctx, remote, space, pullCtx.targetPageID, state, syncflow.DefaultPullOverlapWindow, flagPullForce)
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
	stashRef := ""
	if scopeDirExisted {
		stashRef, err = stashScopeIfDirty(repoRoot, scopePath, pullCtx.spaceKey, pullStartedAt)
		if err != nil {
			return err
		}
		if stashRef != "" {
			defer func() {
				if flagPullDiscardLocal {
					fmt.Fprintf(out, "Discarding local changes (dropped stash %s)\n", stashRef)
					_, _ = runGit(repoRoot, "stash", "drop", stashRef)
					return
				}
				restoreErr := applyAndDropStash(repoRoot, stashRef)
				if restoreErr != nil {
					runErr = errors.Join(runErr, restoreErr)
				}
			}()
		}
	}

	var progress syncflow.Progress
	if !flagVerbose {
		progress = newConsoleProgress(out, "Syncing from Confluence")
	}

	result, err := syncflow.Pull(ctx, remote, syncflow.PullOptions{
		SpaceKey:          pullCtx.spaceKey,
		SpaceDir:          pullCtx.spaceDir,
		State:             state,
		PullStartedAt:     pullStartedAt,
		OverlapWindow:     syncflow.DefaultPullOverlapWindow,
		TargetPageID:      pullCtx.targetPageID,
		ForceFull:         flagPullForce,
		SkipMissingAssets: flagSkipMissingAssets,
		OnDownloadError: func(attachmentID string, pageID string, err error) bool {
			return askToContinueOnDownloadError(cmd.InOrStdin(), out, attachmentID, pageID, err)
		},
		Progress: progress,
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
	// Use actual SpaceKey for tags, sanitized for safety
	refKey := fs.SanitizePathSegment(pullCtx.spaceKey)
	tagName := fmt.Sprintf("confluence-sync/pull/%s/%s", refKey, ts)
	tagMsg := fmt.Sprintf("Confluence pull sync for %s at %s", pullCtx.spaceKey, ts)
	if _, err := runGit(repoRoot, "tag", "-a", tagName, "-m", tagMsg); err != nil {
		return err
	}

	fmt.Fprintf(out, "pull completed: committed and tagged %s\n", tagName)
	return nil
}

type initialPullContext struct {
	spaceKey     string
	spaceDir     string
	targetPageID string
	fixedDir     bool
}

func resolveInitialPullContext(target config.Target) (initialPullContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return initialPullContext{}, err
	}

	if target.IsFile() {
		absPath, err := filepath.Abs(target.Value)
		if err != nil {
			return initialPullContext{}, err
		}

		doc, err := fs.ReadMarkdownDocument(absPath)
		if err != nil {
			return initialPullContext{}, fmt.Errorf("read target file %s: %w", target.Value, err)
		}

		spaceKey := strings.TrimSpace(doc.Frontmatter.Space)
		if spaceKey == "" {
			return initialPullContext{}, fmt.Errorf("target file %s missing space", target.Value)
		}
		pageID := strings.TrimSpace(doc.Frontmatter.ID)
		if pageID == "" {
			return initialPullContext{}, fmt.Errorf("target file %s missing id", target.Value)
		}

		return initialPullContext{
			spaceKey:     spaceKey,
			spaceDir:     findSpaceDirFromFile(absPath, spaceKey),
			targetPageID: pageID,
			fixedDir:     true,
		}, nil
	}

	if target.Value == "" {
		// If we are in a tracked directory, use it.
		if _, err := os.Stat(filepath.Join(cwd, fs.StateFileName)); err == nil {
			state, err := fs.LoadState(cwd)
			if err == nil && len(state.PagePathIndex) > 0 {
				// We need space key. Usually we can find it in one of the files frontmatter
				// or we assume current dir name might be the key if not found.
				// But best is to check if we can find ANY .md file and its space key.
				for relPath := range state.PagePathIndex {
					doc, err := fs.ReadMarkdownDocument(filepath.Join(cwd, filepath.FromSlash(relPath)))
					if err == nil && doc.Frontmatter.Space != "" {
						return initialPullContext{
							spaceKey: doc.Frontmatter.Space,
							spaceDir: cwd,
							fixedDir: true,
						}, nil
					}
				}
			}
		}

		spaceDir, err := filepath.Abs(cwd)
		if err != nil {
			return initialPullContext{}, err
		}
		return initialPullContext{
			spaceKey: filepath.Base(spaceDir),
			spaceDir: spaceDir,
			fixedDir: false,
		}, nil
	}

	if info, statErr := os.Stat(target.Value); statErr == nil && info.IsDir() {
		spaceDir, err := filepath.Abs(target.Value)
		if err != nil {
			return initialPullContext{}, err
		}

		// Check if it is a tracked directory
		if _, err := os.Stat(filepath.Join(spaceDir, fs.StateFileName)); err == nil {
			state, err := fs.LoadState(spaceDir)
			if err == nil {
				for relPath := range state.PagePathIndex {
					doc, err := fs.ReadMarkdownDocument(filepath.Join(spaceDir, filepath.FromSlash(relPath)))
					if err == nil && doc.Frontmatter.Space != "" {
						return initialPullContext{
							spaceKey: doc.Frontmatter.Space,
							spaceDir: spaceDir,
							fixedDir: true,
						}, nil
					}
				}
			}
		}

		return initialPullContext{
			spaceKey: filepath.Base(spaceDir),
			spaceDir: spaceDir,
			fixedDir: true, // User explicitly provided a directory
		}, nil
	}

	spaceDir := filepath.Join(cwd, target.Value)
	if _, err := os.Stat(spaceDir); err != nil {
		// Try to find a directory that looks like "Name (KEY)"
		if items, err := os.ReadDir(cwd); err == nil {
			suffix := fmt.Sprintf("(%s)", target.Value)
			for _, item := range items {
				if item.IsDir() && strings.HasSuffix(item.Name(), suffix) {
					spaceDir = filepath.Join(cwd, item.Name())
					return initialPullContext{
						spaceKey: target.Value,
						spaceDir: spaceDir,
						fixedDir: true,
					}, nil
				}
			}
		}
	}

	spaceDir, err = filepath.Abs(spaceDir)
	if err != nil {
		return initialPullContext{}, err
	}

	return initialPullContext{
		spaceKey: target.Value,
		spaceDir: spaceDir,
		fixedDir: false,
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
	normalizedRepoRoot, err := normalizeRepoPath(repoRoot)
	if err != nil {
		return "", err
	}
	normalizedScopeDir, err := normalizeRepoPath(scopeDir)
	if err != nil {
		return "", err
	}

	// Case-insensitive comparison for Windows
	isOutside := false
	rel, err := filepath.Rel(normalizedRepoRoot, normalizedScopeDir)
	if err != nil {
		isOutside = true
	} else {
		rel = filepath.Clean(rel)
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			isOutside = true
		}
	}

	if isOutside {
		// Final check: if they are actually the same path or one is prefix of other (case-insensitive)
		lowerRoot := strings.ToLower(filepath.ToSlash(normalizedRepoRoot))
		lowerScope := strings.ToLower(filepath.ToSlash(normalizedScopeDir))
		if !strings.HasPrefix(lowerScope, lowerRoot) {
			return "", fmt.Errorf("space directory %s is outside repository root %s", scopeDir, repoRoot)
		}
		// If it IS a subpath but filepath.Rel failed or returned .., recalculate rel
		rel = strings.TrimPrefix(lowerScope, lowerRoot)
		rel = strings.TrimPrefix(rel, "/")
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return ".", nil
	}
	return rel, nil
}

func normalizeRepoPath(p string) (string, error) {
	absPath, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err == nil && strings.TrimSpace(resolvedPath) != "" {
		absPath = resolvedPath
	}

	// On Windows, handle case sensitivity and short paths for comparison
	if strings.TrimSpace(absPath) != "" {
		if longPath, err := filepath.Abs(absPath); err == nil {
			absPath = longPath
		}
	}

	absPath = filepath.Clean(absPath)

	return absPath, nil
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
	out, err := runGit(repoRoot, "stash", "apply", "--index", stashRef)
	if err != nil {
		// Check if it's a conflict
		if strings.Contains(err.Error(), "conflict") || strings.Contains(out, "CONFLICT") {
			return handlePullConflict(repoRoot, stashRef)
		}
		return fmt.Errorf("local changes could not be automatically merged with remote updates. Please resolve the conflicts in the affected files and then run 'git stash drop %s' to clean up. Error: %w", stashRef, err)
	}
	if _, err := runGit(repoRoot, "stash", "drop", stashRef); err != nil {
		return fmt.Errorf("restored stash but failed to drop %s: %w", stashRef, err)
	}
	return nil
}

func handlePullConflict(repoRoot, stashRef string) error {
	if flagNonInteractive || flagYes {
		return fmt.Errorf("local changes could not be automatically merged with remote updates (CONFLICT). Please resolve the conflicts in the affected files and then run 'git stash drop %s' to clean up.", stashRef)
	}

	fmt.Println("\n⚠️  CONFLICT DETECTED")
	fmt.Println("Local changes could not be automatically merged with remote updates.")
	fmt.Println("How would you like to proceed?")
	fmt.Println(" [1] Keep both (add conflict markers to files) - RECOMMENDED")
	fmt.Println(" [2] Use Remote version (discard my local changes for these files)")
	fmt.Println(" [3] Use Local version (overwrite remote updates with my local changes)")
	fmt.Print("\nChoice [1/2/3]: ")

	var choice string
	fmt.Scanln(&choice)

	switch strings.TrimSpace(choice) {
	case "2":
		fmt.Println("Discarding local changes...")
		// We already pulled remote, so we just need to reset the conflicted files or drop the stash.
		// Actually, stash apply already modified the files with markers.
		// We should checkout from HEAD.
		_, err := runGit(repoRoot, "checkout", "HEAD", "--", ".")
		if err != nil {
			return fmt.Errorf("failed to discard local changes: %w", err)
		}
		_, _ = runGit(repoRoot, "stash", "drop", stashRef)
		fmt.Println("Local changes discarded. Remote version kept.")
		return nil
	case "3":
		fmt.Println("Keeping local version...")
		// Checkout from stash
		_, err := runGit(repoRoot, "checkout", stashRef, "--", ".")
		if err != nil {
			return fmt.Errorf("failed to keep local version: %w", err)
		}
		_, _ = runGit(repoRoot, "stash", "drop", stashRef)
		fmt.Println("Remote updates overwritten by local version.")
		return nil
	default:
		fmt.Printf("Conflict markers kept. Please resolve them manually and then run 'git stash drop %s'\n", stashRef)
		return nil // Return nil because the user "handled" it by choosing to keep markers
	}
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

func estimatePullImpactWithSpace(
	ctx context.Context,
	remote syncflow.PullRemote,
	space confluence.Space,
	targetPageID string,
	state fs.SpaceState,
	overlapWindow time.Duration,
	forceFull bool,
) (pullImpact, error) {
	pages, err := listAllPullPagesForEstimate(ctx, remote, confluence.PageListOptions{
		SpaceID:  space.ID,
		SpaceKey: space.Key,
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

	deletedIDs := map[string]struct{}{}
	for _, pageID := range state.PagePathIndex {
		if _, exists := pageByID[pageID]; !exists {
			deletedIDs[pageID] = struct{}{}
		}
	}

	if forceFull {
		return pullImpact{
			changedMarkdown: len(pageByID),
			deletedMarkdown: len(deletedIDs),
		}, nil
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
			SpaceKey: space.Key,
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

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func ensureSpaceAgentsMD(spaceDir, spaceKey string) {
	path := filepath.Join(spaceDir, "AGENTS.md")
	if _, err := os.Stat(path); err == nil {
		return // Already exists
	}

	content := fmt.Sprintf(`# AGENTS (%s)

This space directory contains Markdown files synced from Confluence space [%s].

## Space Purpose
[Describe what this space is for, e.g., Technical Documentation, HR Policies, etc.]

## Space-Specific Rules
- [e.g., Use Mermaid for all diagrams]
- [e.g., Every page must have a 'Last Reviewed' date at the bottom]
- [e.g., Do not include customer names in these docs]

## Sync Workflow
- Use `+"`cms pull`"+` to get latest updates.
- Use `+"`cms push`"+` to publish your changes.
`, spaceKey, spaceKey)

	_ = os.WriteFile(path, []byte(content), 0644)
}
