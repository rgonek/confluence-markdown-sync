package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

func runPushDryRun(
	ctx context.Context,
	cmd *cobra.Command,
	out io.Writer,
	target config.Target,
	spaceKey, spaceDir, onConflict string,
	gitClient *git.Client,
	spaceScopePath, changeScopePath string,
) error {
	_, _ = fmt.Fprintln(out, "[DRY-RUN] Simulating push (no git or confluence state will be modified)")

	baselineRef, err := gitPushBaselineRef(gitClient, spaceKey)
	if err != nil {
		return err
	}

	syncChanges, err := collectPushChangesForTarget(gitClient, baselineRef, target, spaceScopePath, changeScopePath)
	if err != nil {
		return err
	}

	if len(syncChanges) == 0 {
		_, _ = fmt.Fprintln(out, "push completed: no local markdown changes detected since last sync (no-op)")
		return nil
	}

	if err := runPushValidation(ctx, out, target, spaceDir, "pre-push validate failed"); err != nil {
		return err
	}

	envPath := findEnvPath(spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	realRemote, err := newPushRemote(cfg)
	if err != nil {
		return fmt.Errorf("create confluence client: %w", err)
	}
	defer closeRemoteIfPossible(realRemote)

	remote := &dryRunPushRemote{inner: realRemote, out: out, domain: cfg.Domain, emitOperations: true}

	state, err := fs.LoadState(spaceDir)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	globalPageIndex, err := buildWorkspaceGlobalPageIndex(spaceDir)
	if err != nil {
		return fmt.Errorf("build global page index: %w", err)
	}

	var progress syncflow.Progress
	if !flagVerbose && outputSupportsProgress(out) {
		progress = newConsoleProgress(out, "[DRY-RUN] Syncing to Confluence")
	}

	result, err := syncflow.Push(ctx, remote, syncflow.PushOptions{
		SpaceKey:            spaceKey,
		SpaceDir:            spaceDir,
		Domain:              cfg.Domain,
		State:               state,
		GlobalPageIndex:     globalPageIndex,
		Changes:             syncChanges,
		ConflictPolicy:      toSyncConflictPolicy(onConflict),
		KeepOrphanAssets:    flagPushKeepOrphanAssets,
		DryRun:              true,
		ArchiveTimeout:      normalizedArchiveTaskTimeout(),
		ArchivePollInterval: normalizedArchiveTaskPollInterval(),
		Progress:            progress,
	})
	if err != nil {
		var conflictErr *syncflow.PushConflictError
		if errors.As(err, &conflictErr) {
			return formatPushConflictError(conflictErr)
		}
		printPushDiagnostics(out, result.Diagnostics)
		return err
	}

	_, _ = fmt.Fprintf(out, "\n[DRY-RUN] push completed: %d page change(s) would be synced\n", len(result.Commits))
	printPushDiagnostics(out, result.Diagnostics)
	printPushSyncSummary(out, result.Commits, result.Diagnostics)
	return nil
}

func resolveInitialPushContext(target config.Target) (initialPullContext, error) {
	if !target.IsFile() {
		return resolveInitialPullContext(target)
	}

	absPath, err := filepath.Abs(target.Value)
	if err != nil {
		return initialPullContext{}, err
	}

	if _, err := os.Stat(absPath); err != nil {
		return initialPullContext{}, fmt.Errorf("target file %s: %w", target.Value, err)
	}

	spaceDir := findSpaceDirFromFile(absPath, "")
	spaceKey := ""
	if state, stateErr := fs.LoadState(spaceDir); stateErr == nil {
		spaceKey = strings.TrimSpace(state.SpaceKey)
	}
	if spaceKey == "" {
		spaceKey = inferSpaceKeyFromDirName(spaceDir)
	}
	if spaceKey == "" {
		return initialPullContext{}, fmt.Errorf("target file %s missing tracked space context; run pull with a space target first", target.Value)
	}

	return initialPullContext{
		spaceKey: spaceKey,
		spaceDir: spaceDir,
		fixedDir: true,
	}, nil
}
