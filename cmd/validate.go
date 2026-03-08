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
	"github.com/rgonek/confluence-markdown-sync/internal/search"
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

type validateWarning struct {
	Code    string
	Message string
}

type validateFileResult struct {
	Issues   []fs.ValidationIssue
	Warnings []validateWarning
}

func newValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
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
	addReportJSONFlag(cmd)
	return cmd
}

func runValidateCommand(cmd *cobra.Command, target config.Target) (runErr error) {
	actualOut := cmd.OutOrStdout()
	out := reportWriter(cmd, actualOut)
	runID, restoreLogger := beginCommandRun("validate")
	defer restoreLogger()

	startedAt := time.Now()
	report := newCommandRunReport(runID, "validate", target, startedAt)
	defer func() {
		if !commandRequestsJSONReport(cmd) {
			return
		}
		report.finalize(runErr, time.Now())
		_ = writeCommandRunReport(actualOut, report)
	}()
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

	result, err := runValidateTargetWithContextReport(getCommandContext(cmd), out, target)
	report.Target.SpaceKey = result.SpaceKey
	report.Target.SpaceDir = result.SpaceDir
	report.Target.File = result.TargetFile
	report.Diagnostics = append(report.Diagnostics, result.Diagnostics...)
	return err
}

func runValidateTargetWithContext(ctx context.Context, out io.Writer, target config.Target) error {
	_, err := runValidateTargetWithContextReport(ctx, out, target)
	return err
}

func runValidateTargetWithContextReport(ctx context.Context, out io.Writer, target config.Target) (validateCommandResult, error) {
	if err := ensureWorkspaceSyncReady("validate"); err != nil {
		return validateCommandResult{}, err
	}

	targetCtx, err := resolveValidateTargetContext(target)
	if err != nil {
		return validateCommandResult{}, err
	}
	result := validateCommandResult{
		SpaceKey:    targetCtx.spaceKey,
		SpaceDir:    targetCtx.spaceDir,
		Diagnostics: []commandRunReportDiagnostic{},
	}
	if target.IsFile() && len(targetCtx.files) == 1 {
		result.TargetFile = targetCtx.files[0]
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}

	envPath := findEnvPath(targetCtx.spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return result, fmt.Errorf("failed to load config: %w", err)
	}

	_, _ = fmt.Fprintf(out, "Building index for space: %s\n", targetCtx.spaceDir)
	index, err := syncflow.BuildPageIndexWithPending(targetCtx.spaceDir, targetCtx.files)
	if err != nil {
		return result, fmt.Errorf("failed to build page index: %w", err)
	}

	if dupErrs := detectDuplicatePageIDs(index); len(dupErrs) > 0 {
		for _, msg := range dupErrs {
			_, _ = fmt.Fprintf(out, "Validation failed: %s\n", msg)
			result.Diagnostics = append(result.Diagnostics, commandRunReportDiagnostic{
				Code:    "duplicate_page_id",
				Message: msg,
			})
		}
		return result, fmt.Errorf("validation failed: duplicate page IDs detected - rename each file to have a unique id or remove the duplicate id")
	}

	globalIndex, err := buildWorkspaceGlobalPageIndex(targetCtx.spaceDir)
	if err != nil {
		return result, fmt.Errorf("failed to build global page index: %w", err)
	}

	state, err := fs.LoadState(targetCtx.spaceDir)
	if err != nil {
		return result, fmt.Errorf("failed to load state: %w", err)
	}
	if strings.TrimSpace(targetCtx.spaceKey) == "" {
		targetCtx.spaceKey = strings.TrimSpace(state.SpaceKey)
		result.SpaceKey = targetCtx.spaceKey
	}

	immutableResolver := newValidateImmutableFrontmatterResolver(targetCtx.spaceDir, targetCtx.spaceKey, state)

	linkHook := syncflow.NewReverseLinkHookWithGlobalIndex(targetCtx.spaceDir, index, globalIndex, cfg.Domain)

	hasErrors := false
	for _, file := range targetCtx.files {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		rel, _ := filepath.Rel(targetCtx.spaceDir, file)
		rel = filepath.ToSlash(rel)

		fileResult := validateFile(ctx, file, targetCtx.spaceDir, linkHook, state.AttachmentIndex)
		issues := append(fileResult.Issues, immutableResolver.validate(file)...)
		printValidateWarnings(out, rel, fileResult.Warnings)
		for _, warning := range fileResult.Warnings {
			result.Diagnostics = append(result.Diagnostics, commandRunReportDiagnostic{
				Path:    rel,
				Code:    warning.Code,
				Message: warning.Message,
			})
		}
		if len(issues) == 0 {
			continue
		}

		hasErrors = true
		_, _ = fmt.Fprintf(out, "Validation failed for %s:\n", rel)
		for _, issue := range issues {
			_, _ = fmt.Fprintf(out, "  - [%s] %s: %s\n", issue.Code, issue.Field, issue.Message)
			result.Diagnostics = append(result.Diagnostics, commandRunReportDiagnostic{
				Path:    rel,
				Code:    issue.Code,
				Field:   issue.Field,
				Message: issue.Message,
			})
		}
	}

	if hasErrors {
		return result, fmt.Errorf("validation failed: please fix the issues listed above before retrying")
	}

	_, _ = fmt.Fprintln(out, "Validation successful")
	return result, nil
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
	return inferSpaceKeyFromDirName(spaceDir)
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
		previous.State = baselineFrontmatter.State
	}

	if strings.TrimSpace(previous.ID) == "" {
		return nil
	}

	if !baselineFound {
		// Without reliable prior lifecycle state, enforce ID immutability only.
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

func validateFile(ctx context.Context, path, spaceDir string, linkHook mdconverter.LinkParseHook, attachmentIndex map[string]string) validateFileResult {
	result := validateFileResult{}

	// Read full document
	doc, err := fs.ReadMarkdownDocument(path)
	if err != nil {
		result.Issues = append(result.Issues, fs.ValidationIssue{
			Code:    "read_error",
			Message: err.Error(),
		})
		return result
	}
	result.Warnings = append(result.Warnings, mermaidValidationWarnings(doc.Body)...)

	// 1. Validate Schema
	res := fs.ValidateFrontmatterSchema(doc.Frontmatter)
	result.Issues = append(result.Issues, res.Issues...)

	strictAttachmentIndex, _, err := syncflow.BuildStrictAttachmentIndex(spaceDir, path, doc.Body, attachmentIndex)
	if err != nil {
		result.Issues = append(result.Issues, fs.ValidationIssue{
			Code:    "conversion_error",
			Message: err.Error(),
		})
		return result
	}
	preparedBody, err := syncflow.PrepareMarkdownForAttachmentConversion(spaceDir, path, doc.Body, strictAttachmentIndex)
	if err != nil {
		result.Issues = append(result.Issues, fs.ValidationIssue{
			Code:    "conversion_error",
			Message: err.Error(),
		})
		return result
	}
	mediaHook := syncflow.NewReverseMediaHook(spaceDir, strictAttachmentIndex)

	// 2. Strict Conversion
	_, err = converter.Reverse(ctx, []byte(preparedBody), converter.ReverseConfig{
		LinkHook:  linkHook,
		MediaHook: mediaHook,
		Strict:    true,
	}, path)

	if err != nil {
		result.Issues = append(result.Issues, fs.ValidationIssue{
			Code:    "conversion_error",
			Message: err.Error(),
		})
	}

	return result
}

// runValidateChangedPushFiles validates only the files in changedAbsPaths but builds the full
// space context (index, global index) so cross-page links resolve correctly.
func runValidateChangedPushFiles(ctx context.Context, out io.Writer, spaceDir string, changedAbsPaths []string) error {
	if len(changedAbsPaths) == 0 {
		return nil
	}

	spaceTarget := config.Target{Mode: config.TargetModeSpace, Value: spaceDir}
	targetCtx, err := resolveValidateTargetContext(spaceTarget)
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

	if dupErrs := detectDuplicatePageIDs(index); len(dupErrs) > 0 {
		for _, msg := range dupErrs {
			_, _ = fmt.Fprintf(out, "Validation failed: %s\n", msg)
		}
		return fmt.Errorf("validation failed: duplicate page IDs detected - rename each file to have a unique id or remove the duplicate id")
	}

	globalIndex, err := buildWorkspaceGlobalPageIndex(targetCtx.spaceDir)
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
	for _, file := range changedAbsPaths {
		if err := ctx.Err(); err != nil {
			return err
		}

		rel, _ := filepath.Rel(targetCtx.spaceDir, file)

		fileResult := validateFile(ctx, file, targetCtx.spaceDir, linkHook, state.AttachmentIndex)
		issues := append(fileResult.Issues, immutableResolver.validate(file)...)
		printValidateWarnings(out, rel, fileResult.Warnings)
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

// pushChangedAbsPaths returns absolute paths for push changes that are not deletions.
func pushChangedAbsPaths(spaceDir string, changes []syncflow.PushFileChange) []string {
	out := make([]string, 0, len(changes))
	for _, change := range changes {
		if change.Type == syncflow.PushChangeDelete {
			continue
		}
		out = append(out, filepath.Join(spaceDir, filepath.FromSlash(change.Path)))
	}
	return out
}

func buildWorkspaceGlobalPageIndex(spaceDir string) (syncflow.GlobalPageIndex, error) {
	globalIndexRoot, err := syncflow.ResolveGlobalIndexRoot(spaceDir)
	if err != nil {
		return nil, err
	}
	return syncflow.BuildGlobalPageIndex(globalIndexRoot)
}

// detectDuplicatePageIDs returns an error message for each Confluence page ID
// that appears in more than one file within the index.
// A duplicate ID typically means a file was copy-pasted (rename trap) and
// both copies now claim the same remote page.
func detectDuplicatePageIDs(index syncflow.PageIndex) []string {
	pathsByID := make(map[string][]string)
	for relPath, pageID := range index {
		if strings.TrimSpace(pageID) == "" {
			continue
		}
		pathsByID[pageID] = append(pathsByID[pageID], relPath)
	}

	// Collect sorted keys so output is deterministic
	ids := make([]string, 0, len(pathsByID))
	for id := range pathsByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var errs []string
	for _, id := range ids {
		paths := pathsByID[id]
		if len(paths) <= 1 {
			continue
		}
		sort.Strings(paths)
		errs = append(errs, fmt.Sprintf(
			"page id %q is used by multiple files: %s — remove the duplicate id from all but one file",
			id,
			strings.Join(paths, ", "),
		))
	}
	return errs
}

func printValidateWarnings(out io.Writer, relPath string, warnings []validateWarning) {
	if len(warnings) == 0 {
		return
	}

	_, _ = fmt.Fprintf(out, "Validation warning for %s:\n", filepath.ToSlash(relPath))
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(out, "  - [%s] %s\n", warning.Code, warning.Message)
	}
}

func mermaidValidationWarnings(body string) []validateWarning {
	structure := search.ParseMarkdownStructure([]byte(body))
	warnings := make([]validateWarning, 0)
	for _, block := range structure.CodeBlocks {
		if !strings.EqualFold(strings.TrimSpace(block.Language), "mermaid") {
			continue
		}
		warnings = append(warnings, validateWarning{
			Code: "MERMAID_PRESERVED_AS_CODEBLOCK",
			Message: fmt.Sprintf(
				"Mermaid fenced code at line %d will be pushed as a Confluence code block with language mermaid; it will not render as a Mermaid diagram macro",
				block.Line,
			),
		})
	}
	return warnings
}
