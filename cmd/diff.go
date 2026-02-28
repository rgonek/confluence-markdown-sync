package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

var newDiffRemote = func(cfg *config.Config) (syncflow.PullRemote, error) {
	return newConfluenceClientFromConfig(cfg)
}

type diffContext struct {
	spaceKey     string
	spaceDir     string
	targetPageID string
	targetFile   string
}

func newDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff [TARGET]",
		Short: "Show diff between local Markdown and remote Confluence content",
		Long: `Diff fetches remote Confluence content, converts it to Markdown,
and shows a diff against local files using git diff --no-index.

TARGET can be a SPACE_KEY (e.g. "MYSPACE") or a path to a .md file.
If omitted, the space is inferred from the current directory name.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			return runDiff(cmd, config.ParseTarget(raw))
		},
	}
}

func runDiff(cmd *cobra.Command, target config.Target) (runErr error) {
	_, restoreLogger := beginCommandRun("diff")
	defer restoreLogger()

	startedAt := time.Now()
	telemetrySpaceKey := "unknown"
	slog.Info("diff_started", "target_mode", target.Mode, "target", target.Value)
	defer func() {
		duration := time.Since(startedAt)
		if runErr != nil {
			slog.Warn("diff_finished",
				"space_key", telemetrySpaceKey,
				"duration_ms", duration.Milliseconds(),
				"error", runErr.Error(),
			)
			return
		}
		slog.Info("diff_finished",
			"space_key", telemetrySpaceKey,
			"duration_ms", duration.Milliseconds(),
		)
	}()

	ctx := getCommandContext(cmd)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ensureWorkspaceSyncReady("diff"); err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	initialCtx, err := resolveInitialPullContext(target)
	if err != nil {
		return err
	}

	envPath := findEnvPath(initialCtx.spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	remote, err := newDiffRemote(cfg)
	if err != nil {
		return fmt.Errorf("create confluence client: %w", err)
	}
	defer closeRemoteIfPossible(remote)

	space, err := remote.GetSpace(ctx, initialCtx.spaceKey)
	if err != nil {
		return fmt.Errorf("resolve space %q: %w", initialCtx.spaceKey, err)
	}

	spaceDir := initialCtx.spaceDir
	if !initialCtx.fixedDir {
		spaceDir = filepath.Join(filepath.Dir(initialCtx.spaceDir), fs.SanitizeSpaceDirName(space.Name, space.Key))
	}

	diffCtx := diffContext{
		spaceKey:     space.Key,
		spaceDir:     spaceDir,
		targetPageID: initialCtx.targetPageID,
	}
	telemetrySpaceKey = diffCtx.spaceKey
	if target.IsFile() {
		absPath, err := filepath.Abs(target.Value)
		if err == nil {
			diffCtx.targetFile = absPath
		}
	}

	state, err := fs.LoadState(diffCtx.spaceDir)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	pages, err := listAllDiffPages(ctx, remote, confluence.PageListOptions{
		SpaceID:  space.ID,
		SpaceKey: space.Key,
		Status:   "current",
		Limit:    100,
	})
	if err != nil {
		return fmt.Errorf("list pages: %w", err)
	}

	pages, err = recoverMissingPagesForDiff(ctx, remote, space.ID, state.PagePathIndex, pages)
	if err != nil {
		return fmt.Errorf("recover missing pages: %w", err)
	}

	folderByID, folderDiags, err := resolveDiffFolderHierarchyFromPages(ctx, remote, pages)

	if err != nil {
		return err
	}
	for _, diag := range folderDiags {
		if _, err := fmt.Fprintf(out, "warning: %s [%s] %s\n", diag.Path, diag.Code, diag.Message); err != nil {
			return fmt.Errorf("write diagnostic output: %w", err)
		}
	}

	pagePathByIDAbs, pagePathByIDRel := syncflow.PlanPagePaths(diffCtx.spaceDir, state.PagePathIndex, pages, folderByID)
	attachmentPathByID := buildDiffAttachmentPathByID(diffCtx.spaceDir, state.AttachmentIndex)

	tmpRoot, err := os.MkdirTemp("", "conf-diff-*")
	if err != nil {
		return fmt.Errorf("create diff workspace: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpRoot)
	}()

	if target.IsFile() {
		return runDiffFileMode(ctx, out, remote, diffCtx, pagePathByIDAbs, attachmentPathByID, tmpRoot)
	}

	return runDiffSpaceMode(
		ctx,
		out,
		remote,
		diffCtx,
		pages,
		pagePathByIDAbs,
		pagePathByIDRel,
		attachmentPathByID,
		tmpRoot,
	)
}

func runDiffFileMode(
	ctx context.Context,
	out io.Writer,
	remote syncflow.PullRemote,
	diffCtx diffContext,
	pagePathByIDAbs map[string]string,
	attachmentPathByID map[string]string,
	tmpRoot string,
) error {
	relPath := diffDisplayRelPath(diffCtx.spaceDir, diffCtx.targetFile)
	localFile := filepath.Join(tmpRoot, "local", filepath.FromSlash(relPath))
	remoteFile := filepath.Join(tmpRoot, "remote", filepath.FromSlash(relPath))

	if err := os.MkdirAll(filepath.Dir(localFile), 0o750); err != nil {
		return fmt.Errorf("prepare local diff file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(remoteFile), 0o750); err != nil {
		return fmt.Errorf("prepare diff file: %w", err)
	}

	localRaw, err := os.ReadFile(diffCtx.targetFile) //nolint:gosec // target file path is user-selected markdown inside workspace
	if err != nil {
		return fmt.Errorf("read local file for diff: %w", err)
	}
	localRaw, err = normalizeDiffMarkdown(localRaw)
	if err != nil {
		return fmt.Errorf("normalize local file for diff: %w", err)
	}
	if err := os.WriteFile(localFile, localRaw, 0o600); err != nil {
		return fmt.Errorf("write local diff file: %w", err)
	}

	page, err := remote.GetPage(ctx, diffCtx.targetPageID)
	if err != nil {
		if errors.Is(err, confluence.ErrNotFound) {
			if _, err := fmt.Fprintf(out, "warning: %s [missing_remote_page] remote page %s not found\n", relPath, diffCtx.targetPageID); err != nil {
				return fmt.Errorf("write diagnostic output: %w", err)
			}
			if err := os.WriteFile(remoteFile, []byte{}, 0o600); err != nil {
				return fmt.Errorf("write diff file: %w", err)
			}
			return printNoIndexDiff(out, localFile, remoteFile)
		}
		return fmt.Errorf("fetch page %s: %w", diffCtx.targetPageID, err)
	}

	rendered, diagnostics, err := renderDiffMarkdown(
		ctx,
		page,
		diffCtx.spaceKey,
		diffCtx.targetFile,
		relPath,
		pagePathByIDAbs,
		attachmentPathByID,
	)
	if err != nil {
		return err
	}

	for _, diag := range diagnostics {
		if _, err := fmt.Fprintf(out, "warning: %s [%s] %s\n", diag.Path, diag.Code, diag.Message); err != nil {
			return fmt.Errorf("write diagnostic output: %w", err)
		}
	}

	if err := os.WriteFile(remoteFile, rendered, 0o600); err != nil {
		return fmt.Errorf("write diff file: %w", err)
	}

	return printNoIndexDiff(out, localFile, remoteFile)
}

func runDiffSpaceMode(
	ctx context.Context,
	out io.Writer,
	remote syncflow.PullRemote,
	diffCtx diffContext,
	pages []confluence.Page,
	pagePathByIDAbs map[string]string,
	pagePathByIDRel map[string]string,
	attachmentPathByID map[string]string,
	tmpRoot string,
) error {
	localSnapshot := filepath.Join(tmpRoot, "local")
	remoteSnapshot := filepath.Join(tmpRoot, "remote")
	if err := os.MkdirAll(localSnapshot, 0o750); err != nil {
		return fmt.Errorf("prepare local snapshot: %w", err)
	}
	if err := os.MkdirAll(remoteSnapshot, 0o750); err != nil {
		return fmt.Errorf("prepare remote snapshot: %w", err)
	}

	if err := copyLocalMarkdownSnapshot(diffCtx.spaceDir, localSnapshot); err != nil {
		return err
	}

	pageIDs := make([]string, 0, len(pages))
	for _, page := range pages {
		pageIDs = append(pageIDs, page.ID)
	}
	sort.Strings(pageIDs)

	diagnostics := make([]syncflow.PullDiagnostic, 0)
	for _, pageID := range pageIDs {
		page, err := remote.GetPage(ctx, pageID)
		if err != nil {
			if errors.Is(err, confluence.ErrNotFound) || errors.Is(err, confluence.ErrArchived) {
				continue
			}
			return fmt.Errorf("fetch page %s: %w", pageID, err)
		}

		sourcePath, ok := pagePathByIDAbs[page.ID]
		if !ok {
			return fmt.Errorf("planned path missing for page %s", page.ID)
		}

		relPath, ok := pagePathByIDRel[page.ID]
		if !ok {
			return fmt.Errorf("planned relative path missing for page %s", page.ID)
		}

		rendered, pageDiags, err := renderDiffMarkdown(
			ctx,
			page,
			diffCtx.spaceKey,
			sourcePath,
			relPath,
			pagePathByIDAbs,
			attachmentPathByID,
		)
		if err != nil {
			return err
		}
		diagnostics = append(diagnostics, pageDiags...)

		dstPath := filepath.Join(remoteSnapshot, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o750); err != nil {
			return fmt.Errorf("prepare remote snapshot path: %w", err)
		}
		if err := os.WriteFile(dstPath, rendered, 0o600); err != nil {
			return fmt.Errorf("write remote snapshot file: %w", err)
		}
	}

	for _, diag := range diagnostics {
		if _, err := fmt.Fprintf(out, "warning: %s [%s] %s\n", diag.Path, diag.Code, diag.Message); err != nil {
			return fmt.Errorf("write diagnostic output: %w", err)
		}
	}

	return printNoIndexDiff(out, localSnapshot, remoteSnapshot)
}
