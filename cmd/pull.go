package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

const maxPaginationIterations = 500

var (
	flagPullForce        = false
	flagPullDiscardLocal = false
	flagPullRelink       = false

	newPullRemote = func(cfg *config.Config) (syncflow.PullRemote, error) {
		return newConfluenceClientFromConfig(cfg)
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
	cmd.Flags().BoolVarP(&flagPullRelink, "relink", "r", false, "Automatically relink references to this space from other spaces after pull")
	return cmd
}

func runPull(cmd *cobra.Command, target config.Target) (runErr error) {
	ctx := getCommandContext(cmd)
	out := ensureSynchronizedCmdOutput(cmd)
	_, restoreLogger := beginCommandRun("pull")
	defer restoreLogger()
	if err := ensureWorkspaceSyncReady("pull"); err != nil {
		return err
	}

	startedAt := time.Now()
	telemetrySpaceKey := ""
	telemetryUpdated := 0
	telemetryDeleted := 0
	telemetryAssetsDownloaded := 0
	telemetryDiagnostics := 0
	slog.Info("pull_started", "target_mode", target.Mode, "target", target.Value)

	defer func() {
		if telemetrySpaceKey == "" {
			telemetrySpaceKey = "unknown"
		}
		duration := time.Since(startedAt)
		if runErr != nil {
			slog.Warn("pull_finished",
				"space_key", telemetrySpaceKey,
				"duration_ms", duration.Milliseconds(),
				"error", runErr.Error(),
			)
			return
		}
		slog.Info("pull_finished",
			"space_key", telemetrySpaceKey,
			"duration_ms", duration.Milliseconds(),
			"updated_markdown", telemetryUpdated,
			"deleted_markdown", telemetryDeleted,
			"downloaded_assets", telemetryAssetsDownloaded,
			"diagnostics", telemetryDiagnostics,
		)
	}()

	// 1. Initial resolution of key/dir
	initialCtx, err := resolveInitialPullContext(target)
	if err != nil {
		return err
	}
	if flagPullForce && strings.TrimSpace(initialCtx.targetPageID) != "" {
		return errors.New("--force is only supported for space targets")
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
	defer closeRemoteIfPossible(remote)

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
	telemetrySpaceKey = pullCtx.spaceKey

	scopeDirExisted := dirExists(pullCtx.spaceDir)

	if err := os.MkdirAll(pullCtx.spaceDir, 0o750); err != nil {
		return fmt.Errorf("prepare space directory: %w", err)
	}

	state, err := loadPullStateWithHealing(ctx, out, remote, space, pullCtx.spaceDir)
	if err != nil {
		return err
	}

	var progress syncflow.Progress
	if !flagVerbose && outputSupportsProgress(out) {
		progress = newConsoleProgress(out, "Syncing from Confluence")
	}

	impact, err := estimatePullImpactWithSpace(ctx, remote, space, pullCtx.targetPageID, state, syncflow.DefaultPullOverlapWindow, flagPullForce, progress)
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

	dirtyMarkdownBeforePull := map[string]struct{}{}
	if !flagPullDiscardLocal {
		dirtyMarkdownBeforePull, err = listDirtyMarkdownPathsForScope(repoRoot, scopePath)
		if err != nil {
			return fmt.Errorf("inspect local markdown changes: %w", err)
		}
	}

	pullStartedAt := nowUTC()
	stashRef := ""
	var result syncflow.PullResult
	if scopeDirExisted {
		stashRef, err = stashScopeIfDirty(repoRoot, scopePath, pullCtx.spaceKey, pullStartedAt)
		if err != nil {
			return translateWorkspaceGitError(err, "pull")
		}
		if stashRef != "" {
			defer func() {
				if flagPullDiscardLocal && runErr == nil {
					_, _ = fmt.Fprintf(out, "Discarding local changes (dropped stash %s)\n", stashRef)
					_, _ = runGit(repoRoot, "stash", "drop", stashRef)
					return
				}
				if flagPullDiscardLocal && runErr != nil {
					_, _ = fmt.Fprintf(out, "Pull failed; preserving local changes from stash %s\n", stashRef)
				}

				if runErr != nil {
					// CLEANUP: If pull failed and we have a stash, we must clean up
					// the mess Pull made before we can pop the stash.
					// Otherwise git stash apply --include-untracked will fail if it
					// tries to restore files that Pull newly created.
					_, _ = fmt.Fprintf(out, "Cleaning up failed pull before restoring local changes...\n")
					cleanupFailedPullScope(repoRoot, scopePath)
				}

				restoreLocalChanges := func() error {
					if err := applyAndDropStash(repoRoot, stashRef, scopePath, cmd.InOrStdin(), out); err != nil {
						return err
					}
					// After a successful stash restore, ensure the version field in
					// any pulled file reflects the remote version rather than the
					// pre-pull local version that the stash may have reintroduced.
					if runErr == nil {
						fixPulledVersionsAfterStashRestore(repoRoot, pullCtx.spaceDir, result.UpdatedMarkdown, out)
					}
					return nil
				}

				var restoreErr error
				if progress != nil {
					restoreErr = runWithIndeterminateStatus(out, "Restoring local workspace", restoreLocalChanges)
				} else {
					restoreErr = restoreLocalChanges()
				}
				if restoreErr != nil {
					runErr = errors.Join(runErr, restoreErr)
				}
			}()
		}
	} else {
		// If the directory didn't exist before, we should delete it on error
		defer func() {
			if runErr != nil {
				_, _ = fmt.Fprintf(out, "Cleaning up failed pull...\n")
				_ = os.RemoveAll(pullCtx.spaceDir)
			}
		}()
	}

	globalPageIndex, err := syncflow.BuildGlobalPageIndex(repoRoot)
	if err != nil {
		return fmt.Errorf("build global page index: %w", err)
	}

	result, err = syncflow.Pull(ctx, remote, syncflow.PullOptions{
		SpaceKey:          pullCtx.spaceKey,
		SpaceDir:          pullCtx.spaceDir,
		State:             state,
		GlobalPageIndex:   globalPageIndex,
		PullStartedAt:     pullStartedAt,
		OverlapWindow:     syncflow.DefaultPullOverlapWindow,
		TargetPageID:      pullCtx.targetPageID,
		ForceFull:         flagPullForce,
		SkipMissingAssets: flagSkipMissingAssets,
		PrefetchedPages:   impact.prefetchedPages,
		OnDownloadError: func(attachmentID string, pageID string, err error) bool {
			return askToContinueOnDownloadError(cmd.InOrStdin(), out, attachmentID, pageID, err)
		},
		Progress: progress,
	})

	if err != nil {
		return err
	}
	telemetryUpdated = len(result.UpdatedMarkdown)
	telemetryDeleted = len(result.DeletedMarkdown)
	telemetryAssetsDownloaded = len(result.DownloadedAssets)
	telemetryDiagnostics = len(result.Diagnostics)

	if !flagPullDiscardLocal {
		warnSkippedDirtyDeletions(out, result.DeletedMarkdown, dirtyMarkdownBeforePull)
	}

	if err := fs.SaveState(pullCtx.spaceDir, result.State); err != nil {
		return fmt.Errorf("save state: %w", err)
	}

	for _, diag := range result.Diagnostics {
		if err := writeSyncDiagnostic(out, diag); err != nil {
			return fmt.Errorf("write diagnostic output: %w", err)
		}
	}

	hasChanges := false
	tagName := ""
	finalizePullGit := func() error {
		if _, err := runGit(repoRoot, "add", "--", scopePath); err != nil {
			return err
		}

		var err error
		hasChanges, err = gitHasScopedStagedChanges(repoRoot, scopePath)
		if err != nil {
			return err
		}
		if !hasChanges {
			return nil
		}

		commitMsg := fmt.Sprintf("Sync from Confluence: [%s] (v%d)", pullCtx.spaceKey, result.MaxVersion)
		if _, err := runGit(repoRoot, "commit", "-m", commitMsg); err != nil {
			return err
		}

		ts := pullStartedAt.UTC().Format("20060102T150405Z")
		refKey := fs.SanitizePathSegment(pullCtx.spaceKey)
		tagName = fmt.Sprintf("confluence-sync/pull/%s/%s", refKey, ts)
		tagMsg := fmt.Sprintf("Confluence pull sync for %s at %s", pullCtx.spaceKey, ts)
		if _, err := runGit(repoRoot, "tag", "-a", tagName, "-m", tagMsg); err != nil {
			return err
		}

		return nil
	}

	if progress != nil {
		if err := runWithIndeterminateStatus(out, "Finalizing pull", finalizePullGit); err != nil {
			return err
		}
	} else {
		if err := finalizePullGit(); err != nil {
			return err
		}
	}

	if !hasChanges {
		_, _ = fmt.Fprintln(out, "pull completed with no scoped changes (no-op)")
		return nil
	}

	_, _ = fmt.Fprintf(out, "pull completed: committed and tagged %s\n", tagName)

	if err := updateSearchIndexForSpace(repoRoot, pullCtx.spaceDir, pullCtx.spaceKey, out); err != nil {
		_, _ = fmt.Fprintf(out, "warning: search index update failed: %v\n", err)
	}

	if flagPullRelink {
		index, err := syncflow.BuildGlobalPageIndex(repoRoot)
		if err != nil {
			return fmt.Errorf("build global index for relink: %w", err)
		}

		states, err := fs.FindAllStateFiles(repoRoot)
		if err != nil {
			return fmt.Errorf("discover spaces for relink: %w", err)
		}

		if err := runTargetedRelink(cmd, repoRoot, pullCtx.spaceDir, index, states); err != nil {
			return fmt.Errorf("auto-relink: %w", err)
		}
	}

	return nil
}
