package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/rgonek/jira-adf-converter/mdconverter"
	"github.com/spf13/cobra"
)

type validateTargetContext struct {
	spaceDir string
	spaceKey string
	files    []string
}

type validateImmutableFrontmatterResolver struct {
	spaceDir string
	spaceKey string
	state    fs.SpaceState

	gitClient   *git.Client
	baselineRef string

	baselineCache map[string]baselineFrontmatterCacheEntry
}

type baselineFrontmatterCacheEntry struct {
	loaded bool
	found  bool
	fm     fs.Frontmatter
}

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [TARGET]",
		Short: "Validate local Markdown files against sync invariants",
		Long: `Validate checks frontmatter schema, immutable key integrity,
link/asset resolution, and Markdown-to-ADF conversion.

TARGET can be a SPACE_KEY (e.g. "MYSPACE") or a path to a .md file.
If omitted, the space is inferred from the current directory name.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			target := config.ParseTarget(raw)
			return runValidateCommand(cmd, target)
		},
	}
}

func runValidateTarget(out io.Writer, target config.Target) error {
	return runValidateTargetWithContext(context.Background(), out, target)
}

func runValidateCommand(cmd *cobra.Command, target config.Target) (runErr error) {
	_, restoreLogger := beginCommandRun("validate")
	defer restoreLogger()

	startedAt := time.Now()
	slog.Info("validate_started", "target_mode", target.Mode, "target", target.Value)
	defer func() {
		duration := time.Since(startedAt)
		if runErr != nil {
			slog.Warn("validate_finished",
				"duration_ms", duration.Milliseconds(),
				"error", runErr.Error(),
			)
			return
		}
		slog.Info("validate_finished", "duration_ms", duration.Milliseconds())
	}()

	return runValidateTargetWithContext(getCommandContext(cmd), cmd.OutOrStdout(), target)
}

func runValidateTargetWithContext(ctx context.Context, out io.Writer, target config.Target) error {
	targetCtx, err := resolveValidateTargetContext(target)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	envPath := findEnvPath(targetCtx.spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	_, _ = fmt.Fprintf(out, "Building index for space: %s\n", targetCtx.spaceDir)
	index, err := syncflow.BuildPageIndexWithPending(targetCtx.spaceDir, targetCtx.files)
	if err != nil {
		return fmt.Errorf("failed to build page index: %w", err)
	}

	var globalIndex syncflow.GlobalPageIndex
	globalIndexRoot := filepath.Dir(targetCtx.spaceDir)
	if repoRoot, rootErr := gitRepoRoot(); rootErr == nil {
		globalIndexRoot = repoRoot
	}
	globalIndex, err = syncflow.BuildGlobalPageIndex(globalIndexRoot)
	if err != nil {
		return fmt.Errorf("failed to build global page index: %w", err)
	}

	state, err := fs.LoadState(targetCtx.spaceDir)
	if err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}
	if strings.TrimSpace(targetCtx.spaceKey) == "" {
		targetCtx.spaceKey = strings.TrimSpace(state.SpaceKey)
	}

	immutableResolver := newValidateImmutableFrontmatterResolver(targetCtx.spaceDir, targetCtx.spaceKey, state)

	linkHook := syncflow.NewReverseLinkHookWithGlobalIndex(targetCtx.spaceDir, index, globalIndex, cfg.Domain)

	hasErrors := false
	for _, file := range targetCtx.files {
		if err := ctx.Err(); err != nil {
			return err
		}

		rel, _ := filepath.Rel(targetCtx.spaceDir, file)

		issues := validateFile(ctx, file, targetCtx.spaceDir, linkHook, state.AttachmentIndex)
		issues = append(issues, immutableResolver.validate(file)...)
		if len(issues) == 0 {
			continue
		}

		hasErrors = true
		_, _ = fmt.Fprintf(out, "Validation failed for %s:\n", filepath.ToSlash(rel))
		for _, issue := range issues {
			_, _ = fmt.Fprintf(out, "  - [%s] %s: %s\n", issue.Code, issue.Field, issue.Message)
		}
	}

	if hasErrors {
		return fmt.Errorf("validation failed: please fix the issues listed above before retrying")
	}

	_, _ = fmt.Fprintln(out, "Validation successful")
	return nil
}

func resolveValidateTargetContext(target config.Target) (validateTargetContext, error) {
	if target.IsFile() {
		abs, err := filepath.Abs(target.Value)
		if err != nil {
			return validateTargetContext{}, err
		}
		if _, err := os.Stat(abs); err != nil {
			return validateTargetContext{}, fmt.Errorf("target file %s: %w", target.Value, err)
		}

		return validateTargetContext{
			spaceDir: findSpaceDirFromFile(abs, ""),
			spaceKey: resolveValidateFileSpaceKey(abs),
			files:    []string{abs},
		}, nil
	}

	initialCtx, err := resolveInitialPullContext(target)
	if err != nil {
		return validateTargetContext{}, err
	}
	spaceDir := initialCtx.spaceDir
	info, err := os.Stat(spaceDir)
	if err != nil {
		return validateTargetContext{}, fmt.Errorf("resolve space directory %s: %w", spaceDir, err)
	}
	if !info.IsDir() {
		return validateTargetContext{}, fmt.Errorf("resolved space path is not a directory: %s", spaceDir)
	}

	files := make([]string, 0)
	err = filepath.WalkDir(spaceDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == "assets" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".md" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return validateTargetContext{}, fmt.Errorf("failed to walk space directory: %w", err)
	}
	sort.Strings(files)

	return validateTargetContext{spaceDir: spaceDir, spaceKey: initialCtx.spaceKey, files: files}, nil
}

func resolveValidateFileSpaceKey(filePath string) string {
	spaceDir := findSpaceDirFromFile(filePath, "")
	state, err := fs.LoadState(spaceDir)
	if err == nil {
		if key := strings.TrimSpace(state.SpaceKey); key != "" {
			return key
		}
	}

	fm, err := fs.ReadFrontmatter(filePath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(fm.Space)
}

func newValidateImmutableFrontmatterResolver(spaceDir, spaceKey string, state fs.SpaceState) *validateImmutableFrontmatterResolver {
	resolver := &validateImmutableFrontmatterResolver{
		spaceDir:      spaceDir,
		spaceKey:      strings.TrimSpace(spaceKey),
		state:         state,
		baselineCache: map[string]baselineFrontmatterCacheEntry{},
	}

	if resolver.spaceKey == "" {
		resolver.spaceKey = strings.TrimSpace(state.SpaceKey)
	}
	if resolver.spaceKey == "" {
		return resolver
	}

	gitClient, err := git.NewClient()
	if err != nil {
		return resolver
	}
	resolver.gitClient = gitClient

	baselineRef, err := gitPushBaselineRef(gitClient, resolver.spaceKey)
	if err != nil {
		return resolver
	}
	resolver.baselineRef = strings.TrimSpace(baselineRef)
	return resolver
}

func (r *validateImmutableFrontmatterResolver) validate(absPath string) []fs.ValidationIssue {
	current, err := fs.ReadFrontmatter(absPath)
	if err != nil {
		return nil
	}

	relPath, err := filepath.Rel(r.spaceDir, absPath)
	if err != nil {
		return nil
	}
	relPath = filepath.ToSlash(relPath)

	previous := fs.Frontmatter{}
	if id := strings.TrimSpace(r.state.PagePathIndex[relPath]); id != "" {
		previous.ID = id
	}

	baselineFrontmatter, baselineFound := r.readBaselineFrontmatter(absPath)
	if baselineFound {
		if id := strings.TrimSpace(baselineFrontmatter.ID); id != "" {
			previous.ID = id
		}
		if space := strings.TrimSpace(baselineFrontmatter.Space); space != "" {
			previous.Space = space
		}
		previous.State = baselineFrontmatter.State
	}

	if strings.TrimSpace(previous.ID) == "" {
		return nil
	}

	if strings.TrimSpace(previous.Space) == "" {
		if key := strings.TrimSpace(r.state.SpaceKey); key != "" {
			previous.Space = key
		} else {
			previous.Space = r.spaceKey
		}
	}

	if !baselineFound {
		// Without reliable prior lifecycle state, enforce ID/space immutability only.
		previous.State = current.State
	}

	result := fs.ValidateImmutableFrontmatter(previous, current)
	return result.Issues
}

func (r *validateImmutableFrontmatterResolver) readBaselineFrontmatter(absPath string) (fs.Frontmatter, bool) {
	if r.gitClient == nil || strings.TrimSpace(r.baselineRef) == "" {
		return fs.Frontmatter{}, false
	}

	repoPath, err := r.gitClient.ScopePath(absPath)
	if err != nil {
		return fs.Frontmatter{}, false
	}
	repoPath = filepath.ToSlash(filepath.Clean(repoPath))

	if cached, ok := r.baselineCache[repoPath]; ok && cached.loaded {
		return cached.fm, cached.found
	}

	raw, err := r.gitClient.Run("show", fmt.Sprintf("%s:%s", r.baselineRef, repoPath))
	if err != nil {
		r.baselineCache[repoPath] = baselineFrontmatterCacheEntry{loaded: true, found: false}
		return fs.Frontmatter{}, false
	}

	doc, err := fs.ParseMarkdownDocument([]byte(raw))
	if err != nil {
		r.baselineCache[repoPath] = baselineFrontmatterCacheEntry{loaded: true, found: false}
		return fs.Frontmatter{}, false
	}

	r.baselineCache[repoPath] = baselineFrontmatterCacheEntry{loaded: true, found: true, fm: doc.Frontmatter}
	return doc.Frontmatter, true
}

func validateFile(ctx context.Context, path, spaceDir string, linkHook mdconverter.LinkParseHook, attachmentIndex map[string]string) []fs.ValidationIssue {
	var issues []fs.ValidationIssue

	// Read full document
	doc, err := fs.ReadMarkdownDocument(path)
	if err != nil {
		return append(issues, fs.ValidationIssue{
			Code:    "read_error",
			Message: err.Error(),
		})
	}

	// 1. Validate Schema
	res := fs.ValidateFrontmatterSchema(doc.Frontmatter)
	issues = append(issues, res.Issues...)

	strictAttachmentIndex, _, err := syncflow.BuildStrictAttachmentIndex(spaceDir, path, doc.Body, attachmentIndex)
	if err != nil {
		issues = append(issues, fs.ValidationIssue{
			Code:    "conversion_error",
			Message: err.Error(),
		})
		return issues
	}
	mediaHook := syncflow.NewReverseMediaHook(spaceDir, strictAttachmentIndex)

	// 2. Strict Conversion
	_, err = converter.Reverse(ctx, []byte(doc.Body), converter.ReverseConfig{
		LinkHook:  linkHook,
		MediaHook: mediaHook,
		Strict:    true,
	}, path)

	if err != nil {
		issues = append(issues, fs.ValidationIssue{
			Code:    "conversion_error",
			Message: err.Error(),
		})
	}

	return issues
}
