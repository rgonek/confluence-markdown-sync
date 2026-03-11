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
	"strings"
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
	cmd := &cobra.Command{
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
	addReportJSONFlag(cmd)
	return cmd
}

func runDiff(cmd *cobra.Command, target config.Target) (runErr error) {
	actualOut := cmd.OutOrStdout()
	out := reportWriter(cmd, actualOut)
	runID, restoreLogger := beginCommandRun("diff")
	defer restoreLogger()

	startedAt := time.Now()
	report := newCommandRunReport(runID, "diff", target, startedAt)
	defer func() {
		if !commandRequestsJSONReport(cmd) {
			return
		}
		report.finalize(runErr, time.Now())
		_ = writeCommandRunReport(actualOut, report)
	}()
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
	initialCtx, err := resolveInitialDiffContext(target)
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
	report.Target.SpaceKey = diffCtx.spaceKey
	report.Target.SpaceDir = diffCtx.spaceDir
	if target.IsFile() {
		absPath, err := filepath.Abs(target.Value)
		if err == nil {
			diffCtx.targetFile = absPath
		}
	}
	report.Target.File = diffCtx.targetFile

	state, err := fs.LoadState(diffCtx.spaceDir)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	if target.IsFile() {
		absPath, err := filepath.Abs(target.Value)
		if err != nil {
			return err
		}
		doc, err := fs.ReadMarkdownDocument(absPath)
		if err != nil {
			return fmt.Errorf("read target file %s: %w", target.Value, err)
		}
		if strings.TrimSpace(doc.Frontmatter.ID) == "" {
			globalPageIndex, buildErr := buildWorkspaceGlobalPageIndex(diffCtx.spaceDir)
			if buildErr != nil {
				return fmt.Errorf("build global page index: %w", buildErr)
			}
			preview, previewErr := buildLocalCreatePreview(ctx, diffCtx.spaceDir, diffDisplayRelPath(diffCtx.spaceDir, absPath), cfg.Domain, state.AttachmentIndex, globalPageIndex)
			if previewErr != nil {
				return fmt.Errorf("build create preview for %s: %w", target.Value, previewErr)
			}
			printDiffCreatePreview(out, preview)
			report.MutatedFiles = append(report.MutatedFiles, preview.RelPath)
			report.MutatedPages = append(report.MutatedPages, commandRunReportPage{
				Operation: "create-preview",
				Path:      preview.RelPath,
				Title:     preview.Title,
			})
			return nil
		}
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
	report.Diagnostics = append(report.Diagnostics, reportDiagnosticsFromPull(folderDiags, diffCtx.spaceDir)...)
	report.FallbackModes = append(report.FallbackModes, fallbackModesFromPullDiagnostics(folderDiags)...)
	for _, diag := range folderDiags {
		if err := writeSyncDiagnostic(out, diag); err != nil {
			return fmt.Errorf("write diagnostic output: %w", err)
		}
	}

	pagePathByIDAbs, pagePathByIDRel := syncflow.PlanPagePaths(diffCtx.spaceDir, state.PagePathIndex, pages, folderByID)
	pathMoves := syncflow.PlannedPagePathMoves(state.PagePathIndex, pagePathByIDRel)
	attachmentPathByID := buildDiffAttachmentPathByID(diffCtx.spaceDir, state.AttachmentIndex)
	globalPageIndex, err := buildWorkspaceGlobalPageIndex(diffCtx.spaceDir)
	if err != nil {
		return fmt.Errorf("build global page index: %w", err)
	}

	tmpRoot, err := os.MkdirTemp("", "conf-diff-*")
	if err != nil {
		return fmt.Errorf("create diff workspace: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpRoot)
	}()

	if target.IsFile() {
		result, err := runDiffFileMode(ctx, out, remote, diffCtx, pagePathByIDAbs, pathMoves, attachmentPathByID, globalPageIndex, tmpRoot)
		report.Diagnostics = append(report.Diagnostics, reportDiagnosticsFromPull(result.Diagnostics, diffCtx.spaceDir)...)
		report.MutatedFiles = append(report.MutatedFiles, result.ChangedFiles...)
		return err
	}

	result, err := runDiffSpaceMode(
		ctx,
		out,
		remote,
		diffCtx,
		pages,
		pagePathByIDAbs,
		pagePathByIDRel,
		pathMoves,
		attachmentPathByID,
		globalPageIndex,
		tmpRoot,
	)
	report.Diagnostics = append(report.Diagnostics, reportDiagnosticsFromPull(result.Diagnostics, diffCtx.spaceDir)...)
	report.MutatedFiles = append(report.MutatedFiles, result.ChangedFiles...)
	return err
}

func resolveInitialDiffContext(target config.Target) (initialPullContext, error) {
	if !target.IsFile() {
		return resolveInitialPullContext(target)
	}

	absPath, err := filepath.Abs(target.Value)
	if err != nil {
		return initialPullContext{}, err
	}
	doc, err := fs.ReadMarkdownDocument(absPath)
	if err != nil {
		return initialPullContext{}, fmt.Errorf("read target file %s: %w", target.Value, err)
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
		spaceKey:     spaceKey,
		spaceDir:     spaceDir,
		targetPageID: strings.TrimSpace(doc.Frontmatter.ID),
		fixedDir:     true,
	}, nil
}

func runDiffFileMode(
	ctx context.Context,
	out io.Writer,
	remote syncflow.PullRemote,
	diffCtx diffContext,
	pagePathByIDAbs map[string]string,
	pathMoves []syncflow.PlannedPagePathMove,
	attachmentPathByID map[string]string,
	globalPageIndex syncflow.GlobalPageIndex,
	tmpRoot string,
) (diffCommandResult, error) {
	result := diffCommandResult{
		SpaceKey:     diffCtx.spaceKey,
		SpaceDir:     diffCtx.spaceDir,
		TargetFile:   diffCtx.targetFile,
		Diagnostics:  []syncflow.PullDiagnostic{},
		ChangedFiles: []string{},
	}
	relPath := diffDisplayRelPath(diffCtx.spaceDir, diffCtx.targetFile)
	localFile := filepath.Join(tmpRoot, "local", filepath.FromSlash(relPath))
	remoteFile := filepath.Join(tmpRoot, "remote", filepath.FromSlash(relPath))

	if err := os.MkdirAll(filepath.Dir(localFile), 0o750); err != nil {
		return result, fmt.Errorf("prepare local diff file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(remoteFile), 0o750); err != nil {
		return result, fmt.Errorf("prepare diff file: %w", err)
	}

	localRaw, err := os.ReadFile(diffCtx.targetFile) //nolint:gosec // target file path is user-selected markdown inside workspace
	if err != nil {
		return result, fmt.Errorf("read local file for diff: %w", err)
	}
	localRaw, err = normalizeDiffMarkdown(localRaw)
	if err != nil {
		return result, fmt.Errorf("normalize local file for diff: %w", err)
	}
	if err := os.WriteFile(localFile, localRaw, 0o600); err != nil {
		return result, fmt.Errorf("write local diff file: %w", err)
	}

	page, err := remote.GetPage(ctx, diffCtx.targetPageID)
	if err != nil {
		if errors.Is(err, confluence.ErrNotFound) {
			result.Diagnostics = append(result.Diagnostics, syncflow.PullDiagnostic{
				Path:    relPath,
				Code:    "missing_remote_page",
				Message: fmt.Sprintf("remote page %s not found", diffCtx.targetPageID),
			})
			if _, err := fmt.Fprintf(out, "warning: %s [missing_remote_page] remote page %s not found\n", relPath, diffCtx.targetPageID); err != nil {
				return result, fmt.Errorf("write diagnostic output: %w", err)
			}
			if err := os.WriteFile(remoteFile, []byte{}, 0o600); err != nil {
				return result, fmt.Errorf("write diff file: %w", err)
			}
			changed, err := renderNoIndexDiff(out, localFile, remoteFile)
			if changed {
				result.ChangedFiles = append(result.ChangedFiles, relPath)
			}
			return result, err
		}
		return result, fmt.Errorf("fetch page %s: %w", diffCtx.targetPageID, err)
	}

	page, metadataDiags := hydrateDiffPageMetadata(ctx, remote, page, relPath)
	renderSourcePath := diffCtx.targetFile
	if plannedSourcePath, ok := pagePathByIDAbs[page.ID]; ok && strings.TrimSpace(plannedSourcePath) != "" {
		renderSourcePath = plannedSourcePath
	}
	rendered, diagnostics, err := renderDiffMarkdown(
		ctx,
		page,
		diffCtx.spaceKey,
		diffCtx.spaceDir,
		renderSourcePath,
		relPath,
		pagePathByIDAbs,
		attachmentPathByID,
		globalPageIndex,
	)
	if err != nil {
		return result, err
	}
	diagnostics = append(metadataDiags, diagnostics...)
	for _, move := range pathMoves {
		if move.PageID == diffCtx.targetPageID {
			diagnostics = append([]syncflow.PullDiagnostic{syncflow.PagePathMoveDiagnostic(move)}, diagnostics...)
			break
		}
	}
	result.Diagnostics = append(result.Diagnostics, diagnostics...)

	for _, diag := range diagnostics {
		if err := writeSyncDiagnostic(out, diag); err != nil {
			return result, fmt.Errorf("write diagnostic output: %w", err)
		}
	}

	if err := os.WriteFile(remoteFile, rendered, 0o600); err != nil {
		return result, fmt.Errorf("write diff file: %w", err)
	}
	if err := writeDiffMetadataSummary(out, []diffMetadataSummary{summarizeMetadataDrift(relPath, localRaw, rendered)}); err != nil {
		return result, err
	}

	changed, err := renderNoIndexDiff(out, localFile, remoteFile)
	if changed {
		result.ChangedFiles = append(result.ChangedFiles, relPath)
	}
	return result, err
}

func printDiffCreatePreview(out io.Writer, preview localCreatePreview) {
	_, _ = fmt.Fprintln(out, "new page preview:")
	_, _ = fmt.Fprintf(out, "  operation: create page %q\n", preview.Title)
	_, _ = fmt.Fprintf(out, "  source file: %s\n", preview.RelPath)
	_, _ = fmt.Fprintf(out, "  resolved parent: %s\n", preview.ResolvedParent)
	_, _ = fmt.Fprintf(out, "  canonical target path: %s\n", preview.CanonicalTargetPath)
	if len(preview.AttachmentUploads) == 0 {
		_, _ = fmt.Fprintln(out, "  attachment operations: none")
	} else {
		_, _ = fmt.Fprintf(out, "  attachment operations: upload %d asset(s)\n", len(preview.AttachmentUploads))
		for _, asset := range preview.AttachmentUploads {
			_, _ = fmt.Fprintf(out, "    - %s\n", asset)
		}
	}
	_, _ = fmt.Fprintf(out, "  ADF summary: %d byte(s), %d top-level node(s)\n", preview.ADFBytes, preview.ADFTopLevelNodes)
}

func runDiffSpaceMode(
	ctx context.Context,
	out io.Writer,
	remote syncflow.PullRemote,
	diffCtx diffContext,
	pages []confluence.Page,
	pagePathByIDAbs map[string]string,
	pagePathByIDRel map[string]string,
	pathMoves []syncflow.PlannedPagePathMove,
	attachmentPathByID map[string]string,
	globalPageIndex syncflow.GlobalPageIndex,
	tmpRoot string,
) (diffCommandResult, error) {
	result := diffCommandResult{
		SpaceKey:     diffCtx.spaceKey,
		SpaceDir:     diffCtx.spaceDir,
		TargetFile:   diffCtx.targetFile,
		Diagnostics:  []syncflow.PullDiagnostic{},
		ChangedFiles: []string{},
	}
	localSnapshot := filepath.Join(tmpRoot, "local")
	remoteSnapshot := filepath.Join(tmpRoot, "remote")
	if err := os.MkdirAll(localSnapshot, 0o750); err != nil {
		return result, fmt.Errorf("prepare local snapshot: %w", err)
	}
	if err := os.MkdirAll(remoteSnapshot, 0o750); err != nil {
		return result, fmt.Errorf("prepare remote snapshot: %w", err)
	}

	if err := copyLocalMarkdownSnapshot(diffCtx.spaceDir, localSnapshot); err != nil {
		return result, err
	}

	pageIDs := make([]string, 0, len(pages))
	for _, page := range pages {
		pageIDs = append(pageIDs, page.ID)
	}
	sort.Strings(pageIDs)

	diagnostics := make([]syncflow.PullDiagnostic, 0)
	for _, move := range pathMoves {
		diagnostics = append(diagnostics, syncflow.PagePathMoveDiagnostic(move))
	}
	metadataSummaries := make([]diffMetadataSummary, 0, len(pageIDs))
	for _, pageID := range pageIDs {
		page, err := remote.GetPage(ctx, pageID)
		if err != nil {
			if errors.Is(err, confluence.ErrNotFound) || errors.Is(err, confluence.ErrArchived) {
				continue
			}
			return result, fmt.Errorf("fetch page %s: %w", pageID, err)
		}

		sourcePath, ok := pagePathByIDAbs[page.ID]
		if !ok {
			return result, fmt.Errorf("planned path missing for page %s", page.ID)
		}

		relPath, ok := pagePathByIDRel[page.ID]
		if !ok {
			return result, fmt.Errorf("planned relative path missing for page %s", page.ID)
		}

		page, metadataDiags := hydrateDiffPageMetadata(ctx, remote, page, relPath)
		rendered, pageDiags, err := renderDiffMarkdown(
			ctx,
			page,
			diffCtx.spaceKey,
			diffCtx.spaceDir,
			sourcePath,
			relPath,
			pagePathByIDAbs,
			attachmentPathByID,
			globalPageIndex,
		)
		if err != nil {
			return result, err
		}
		pageDiags = append(metadataDiags, pageDiags...)
		diagnostics = append(diagnostics, pageDiags...)

		dstPath := filepath.Join(remoteSnapshot, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o750); err != nil {
			return result, fmt.Errorf("prepare remote snapshot path: %w", err)
		}
		if err := os.WriteFile(dstPath, rendered, 0o600); err != nil {
			return result, fmt.Errorf("write remote snapshot file: %w", err)
		}

		localRaw, err := os.ReadFile(sourcePath) //nolint:gosec // planned path is scoped under the current workspace
		if err == nil {
			localRaw, err = normalizeDiffMarkdown(localRaw)
		}
		if err == nil {
			metadataSummaries = append(metadataSummaries, summarizeMetadataDrift(relPath, localRaw, rendered))
		}
	}

	for _, diag := range diagnostics {
		if err := writeSyncDiagnostic(out, diag); err != nil {
			return result, fmt.Errorf("write diagnostic output: %w", err)
		}
	}
	if err := writeDiffMetadataSummary(out, metadataSummaries); err != nil {
		return result, err
	}
	result.Diagnostics = append(result.Diagnostics, diagnostics...)
	changed, err := renderNoIndexDiff(out, localSnapshot, remoteSnapshot)
	if changed {
		changedFiles, changedFilesErr := collectChangedSnapshotFiles(localSnapshot, remoteSnapshot)
		if changedFilesErr != nil {
			return result, changedFilesErr
		}
		result.ChangedFiles = append(result.ChangedFiles, changedFiles...)
	}
	return result, err
}
