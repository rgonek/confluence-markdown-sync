package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

var newDiffRemote = func(cfg *config.Config) (syncflow.PullRemote, error) {
	return confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})
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

func runDiff(cmd *cobra.Command, target config.Target) error {
	ctx := context.Background()
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

	folderByID, folderDiags, err := resolveDiffFolderHierarchyFromPages(ctx, remote, pages)
	if err != nil {
		return err
	}
	for _, diag := range folderDiags {
		fmt.Fprintf(out, "warning: %s [%s] %s\n", diag.Path, diag.Code, diag.Message)
	}

	pagePathByIDAbs, pagePathByIDRel := syncflow.PlanPagePaths(diffCtx.spaceDir, state.PagePathIndex, pages, folderByID)
	attachmentPathByID := buildDiffAttachmentPathByID(diffCtx.spaceDir, state.AttachmentIndex)

	tmpRoot, err := os.MkdirTemp("", "cms-diff-*")
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
	remoteFile := filepath.Join(tmpRoot, "remote.md")
	if err := os.MkdirAll(filepath.Dir(remoteFile), 0o755); err != nil {
		return fmt.Errorf("prepare diff file: %w", err)
	}

	relPath := diffDisplayRelPath(diffCtx.spaceDir, diffCtx.targetFile)

	page, err := remote.GetPage(ctx, diffCtx.targetPageID)
	if err != nil {
		if errors.Is(err, confluence.ErrNotFound) {
			fmt.Fprintf(out, "warning: %s [missing_remote_page] remote page %s not found\n", relPath, diffCtx.targetPageID)
			if err := os.WriteFile(remoteFile, []byte{}, 0o644); err != nil {
				return fmt.Errorf("write diff file: %w", err)
			}
			return printNoIndexDiff(out, diffCtx.targetFile, remoteFile)
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
		fmt.Fprintf(out, "warning: %s [%s] %s\n", diag.Path, diag.Code, diag.Message)
	}

	if err := os.WriteFile(remoteFile, rendered, 0o644); err != nil {
		return fmt.Errorf("write diff file: %w", err)
	}

	return printNoIndexDiff(out, diffCtx.targetFile, remoteFile)
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
	if err := os.MkdirAll(localSnapshot, 0o755); err != nil {
		return fmt.Errorf("prepare local snapshot: %w", err)
	}
	if err := os.MkdirAll(remoteSnapshot, 0o755); err != nil {
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
			if errors.Is(err, confluence.ErrNotFound) {
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
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return fmt.Errorf("prepare remote snapshot path: %w", err)
		}
		if err := os.WriteFile(dstPath, rendered, 0o644); err != nil {
			return fmt.Errorf("write remote snapshot file: %w", err)
		}
	}

	for _, diag := range diagnostics {
		fmt.Fprintf(out, "warning: %s [%s] %s\n", diag.Path, diag.Code, diag.Message)
	}

	return printNoIndexDiff(out, localSnapshot, remoteSnapshot)
}

func renderDiffMarkdown(
	ctx context.Context,
	page confluence.Page,
	spaceKey string,
	sourcePath string,
	relPath string,
	pagePathByIDAbs map[string]string,
	attachmentPathByID map[string]string,
) ([]byte, []syncflow.PullDiagnostic, error) {
	forward, err := converter.Forward(ctx, page.BodyADF, converter.ForwardConfig{
		LinkHook:  syncflow.NewForwardLinkHook(sourcePath, pagePathByIDAbs, spaceKey),
		MediaHook: syncflow.NewForwardMediaHook(sourcePath, attachmentPathByID),
	}, sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("convert page %s: %w", page.ID, err)
	}

	doc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  page.Title,
			ConfluencePageID:       page.ID,
			ConfluenceSpaceKey:     spaceKey,
			ConfluenceVersion:      page.Version,
			ConfluenceLastModified: page.LastModified.UTC().Format("2006-01-02T15:04:05Z07:00"),
			ConfluenceParentPageID: page.ParentPageID,
		},
		Body: forward.Markdown,
	}

	rendered, err := fs.FormatMarkdownDocument(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("format page %s markdown: %w", page.ID, err)
	}

	diagnostics := make([]syncflow.PullDiagnostic, 0, len(forward.Warnings))
	for _, warning := range forward.Warnings {
		diagnostics = append(diagnostics, syncflow.PullDiagnostic{
			Path:    filepath.ToSlash(relPath),
			Code:    string(warning.Type),
			Message: warning.Message,
		})
	}

	return rendered, diagnostics, nil
}

func copyLocalMarkdownSnapshot(spaceDir, snapshotDir string) error {
	err := filepath.WalkDir(spaceDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == "assets" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(spaceDir, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(snapshotDir, relPath)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, raw, 0o644); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("prepare local markdown snapshot: %w", err)
	}
	return nil
}

func buildDiffAttachmentPathByID(spaceDir string, attachmentIndex map[string]string) map[string]string {
	out := map[string]string{}
	relPaths := make([]string, 0, len(attachmentIndex))
	for relPath := range attachmentIndex {
		relPaths = append(relPaths, relPath)
	}
	sort.Strings(relPaths)

	for _, relPath := range relPaths {
		attachmentID := strings.TrimSpace(attachmentIndex[relPath])
		if attachmentID == "" {
			continue
		}
		if _, exists := out[attachmentID]; exists {
			continue
		}

		normalized := filepath.ToSlash(filepath.Clean(relPath))
		normalized = strings.TrimPrefix(normalized, "./")
		out[attachmentID] = filepath.Join(spaceDir, filepath.FromSlash(normalized))
	}

	return out
}

func printNoIndexDiff(out io.Writer, leftPath, rightPath string) error {
	cmd := exec.Command(
		"git",
		"diff",
		"--no-index",
		"--src-prefix=local/",
		"--dst-prefix=remote/",
		"--",
		leftPath,
		rightPath,
	)

	raw, err := cmd.CombinedOutput()
	text := string(raw)
	if text != "" {
		_, _ = io.WriteString(out, text)
	}

	if err == nil {
		fmt.Fprintln(out, "diff completed with no differences")
		return nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil
	}

	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("git diff --no-index failed: %w", err)
	}
	return fmt.Errorf("git diff --no-index failed: %s", strings.TrimSpace(text))
}

func listAllDiffPages(ctx context.Context, remote syncflow.PullRemote, opts confluence.PageListOptions) ([]confluence.Page, error) {
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

func resolveDiffFolderHierarchyFromPages(ctx context.Context, remote syncflow.PullRemote, pages []confluence.Page) (map[string]confluence.Folder, []syncflow.PullDiagnostic, error) {
	folderByID := map[string]confluence.Folder{}
	diagnostics := []syncflow.PullDiagnostic{}

	queue := []string{}
	enqueued := map[string]struct{}{}
	for _, page := range pages {
		if !strings.EqualFold(strings.TrimSpace(page.ParentType), "folder") {
			continue
		}
		folderID := strings.TrimSpace(page.ParentPageID)
		if folderID == "" {
			continue
		}
		if _, exists := enqueued[folderID]; exists {
			continue
		}
		queue = append(queue, folderID)
		enqueued[folderID] = struct{}{}
	}

	visited := map[string]struct{}{}
	for len(queue) > 0 {
		folderID := queue[0]
		queue = queue[1:]

		if _, seen := visited[folderID]; seen {
			continue
		}
		visited[folderID] = struct{}{}

		folder, err := remote.GetFolder(ctx, folderID)
		if err != nil {
			if !shouldIgnoreFolderHierarchyError(err) {
				return nil, nil, fmt.Errorf("get folder %s: %w", folderID, err)
			}
			diagnostics = append(diagnostics, syncflow.PullDiagnostic{
				Path:    folderID,
				Code:    "FOLDER_LOOKUP_UNAVAILABLE",
				Message: fmt.Sprintf("folder %s unavailable, falling back to page-only hierarchy: %v", folderID, err),
			})
			continue
		}

		folderByID[folder.ID] = folder

		if !strings.EqualFold(strings.TrimSpace(folder.ParentType), "folder") {
			continue
		}
		parentID := strings.TrimSpace(folder.ParentID)
		if parentID == "" {
			continue
		}
		if _, seen := visited[parentID]; seen {
			continue
		}
		if _, exists := enqueued[parentID]; exists {
			continue
		}
		queue = append(queue, parentID)
		enqueued[parentID] = struct{}{}
	}

	return folderByID, diagnostics, nil
}

func shouldIgnoreFolderHierarchyError(err error) bool {
	if errors.Is(err, confluence.ErrNotFound) {
		return true
	}
	var apiErr *confluence.APIError
	return errors.As(err, &apiErr)
}

func diffDisplayRelPath(spaceDir, path string) string {
	relPath, err := filepath.Rel(spaceDir, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relPath)
}
