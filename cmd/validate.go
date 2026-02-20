package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/rgonek/jira-adf-converter/mdconverter"
	"github.com/spf13/cobra"
)

type validateTargetContext struct {
	spaceDir string
	files    []string
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
			return runValidateTarget(cmd.OutOrStdout(), target)
		},
	}
}

func runValidateTarget(out io.Writer, target config.Target) error {
	ctx, err := resolveValidateTargetContext(target)
	if err != nil {
		return err
	}

	envPath := findEnvPath(ctx.spaceDir)
	cfg, err := config.Load(envPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	fmt.Fprintf(out, "Building index for space: %s\n", ctx.spaceDir)
	index, err := syncflow.BuildPageIndex(ctx.spaceDir)
	if err != nil {
		return fmt.Errorf("failed to build page index: %w", err)
	}

	state, err := fs.LoadState(ctx.spaceDir)
	if err != nil {
		return fmt.Errorf("failed to load state: %w", err)
	}

	linkHook := syncflow.NewReverseLinkHook(ctx.spaceDir, index, cfg.Domain)
	mediaHook := syncflow.NewReverseMediaHook(ctx.spaceDir, state.AttachmentIndex)

	hasErrors := false
	for _, file := range ctx.files {
		rel, _ := filepath.Rel(ctx.spaceDir, file)

		issues := validateFile(file, ctx.spaceDir, linkHook, mediaHook)
		if len(issues) == 0 {
			continue
		}

		hasErrors = true
		fmt.Fprintf(out, "Validation failed for %s:\n", filepath.ToSlash(rel))
		for _, issue := range issues {
			fmt.Fprintf(out, "  - [%s] %s: %s\n", issue.Code, issue.Field, issue.Message)
		}
	}

	if hasErrors {
		return fmt.Errorf("validation failed")
	}

	fmt.Fprintln(out, "Validation successful")
	return nil
}

func resolveValidateTargetContext(target config.Target) (validateTargetContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return validateTargetContext{}, err
	}

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
			files:    []string{abs},
		}, nil
	}

	spaceDir, err := resolveSpaceDirFromTarget(cwd, target)
	if err != nil {
		return validateTargetContext{}, err
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

	return validateTargetContext{spaceDir: spaceDir, files: files}, nil
}

func resolveSpaceDirFromTarget(cwd string, target config.Target) (string, error) {
	if target.Value == "" {
		return filepath.Abs(cwd)
	}

	if info, err := os.Stat(target.Value); err == nil && info.IsDir() {
		return filepath.Abs(target.Value)
	}

	return filepath.Abs(filepath.Join(cwd, target.Value))
}

func validateFile(path, spaceDir string, linkHook mdconverter.LinkParseHook, mediaHook mdconverter.MediaParseHook) []fs.ValidationIssue {
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

	// 2. Validate Space Key matches state if available
	state, err := fs.LoadState(spaceDir)
	if err == nil {
		// We could collect space key from many files, but state is source of truth for "scoped" changes
		// In fact, we should just check if doc.Frontmatter.ConfluenceSpaceKey is non-empty.
		// Detailed cross-file space key validation is complex if we have multiple spaces.
		// Let's assume if it is valid by schema, we are mostly OK,
		// but we can check it against state page_path_index if we want to be sure it belongs to this dir.
		if doc.Frontmatter.ConfluenceSpaceKey == "" {
			issues = append(issues, fs.ValidationIssue{
				Field:   "confluence_space_key",
				Code:    "required",
				Message: "confluence_space_key is required",
			})
		}

		// If we have state, we can verify this page ID belongs to this space dir
		if doc.Frontmatter.ConfluencePageID != "" {
			found := false
			for _, id := range state.PagePathIndex {
				if id == doc.Frontmatter.ConfluencePageID {
					found = true
					break
				}
			}
			if !found && len(state.PagePathIndex) > 0 {
				// Page is not in state index, might be a new file or wrong space.
				// For now let's just ensure space key is present.
			}
		}
	}

	// 3. Strict Conversion
	_, err = converter.Reverse(context.Background(), []byte(doc.Body), converter.ReverseConfig{
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
