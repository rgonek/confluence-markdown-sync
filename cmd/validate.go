package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/rgonek/jira-adf-converter/mdconverter"
	"github.com/spf13/cobra"
)

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

			cwd, err := os.Getwd()
			if err != nil {
				return err
			}

			// Resolve target to list of files and space directory
			var spaceDir string
			var files []string

			if target.IsFile() {
				files = []string{target.Value}
				abs, err := filepath.Abs(target.Value)
				if err != nil {
					return err
				}
				// Attempt to find space root by looking up for .confluence-state.json
				dir := filepath.Dir(abs)
				found := false
				for {
					if _, err := os.Stat(filepath.Join(dir, ".confluence-state.json")); err == nil {
						spaceDir = dir
						found = true
						break
					}
					parent := filepath.Dir(dir)
					if parent == dir {
						break
					}
					dir = parent
				}
				if !found {
					// Fallback: assume parent of file is space dir
					spaceDir = filepath.Dir(abs)
				}
			} else {
				// Space mode
				if target.Value == "" {
					// Infer from CWD
					spaceDir = cwd
				} else {
					// Check if directory exists
					if info, err := os.Stat(target.Value); err == nil && info.IsDir() {
						spaceDir = target.Value
					} else {
						// Assume relative to CWD
						spaceDir = filepath.Join(cwd, target.Value)
					}
				}

				// Walk space to find all .md files
				err = filepath.WalkDir(spaceDir, func(path string, d os.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if !d.IsDir() && filepath.Ext(path) == ".md" {
						files = append(files, path)
					}
					return nil
				})
				if err != nil {
					return fmt.Errorf("failed to walk space directory: %w", err)
				}
			}

			// Find .env
			envPath := ".env"
			if _, err := os.Stat(filepath.Join(spaceDir, "..", ".env")); err == nil {
				envPath = filepath.Join(spaceDir, "..", ".env")
			} else if _, err := os.Stat(filepath.Join(spaceDir, ".env")); err == nil {
				envPath = filepath.Join(spaceDir, ".env")
			}

			// Load config
			cfg, err := config.Load(envPath)
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Build Page Index
			fmt.Fprintf(cmd.OutOrStdout(), "Building index for space: %s\n", spaceDir)
			index, err := sync.BuildPageIndex(spaceDir)
			if err != nil {
				return fmt.Errorf("failed to build page index: %w", err)
			}

			// Load State for Attachment Index
			state, err := fs.LoadState(spaceDir)
			if err != nil {
				return fmt.Errorf("failed to load state: %w", err)
			}

			// Create Hooks
			linkHook := sync.NewReverseLinkHook(spaceDir, index, cfg.Domain)
			mediaHook := sync.NewReverseMediaHook(spaceDir, state.AttachmentIndex)

			// Validate each file
			hasErrors := false
			for _, file := range files {
				rel, _ := filepath.Rel(spaceDir, file)

				issues := validateFile(file, spaceDir, linkHook, mediaHook)
				if len(issues) > 0 {
					hasErrors = true
					fmt.Fprintf(cmd.OutOrStdout(), "Validation failed for %s:\n", rel)
					for _, issue := range issues {
						fmt.Fprintf(cmd.OutOrStdout(), "  - [%s] %s: %s\n", issue.Code, issue.Field, issue.Message)
					}
				}
			}

			if hasErrors {
				return fmt.Errorf("validation failed")
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Validation successful")
			return nil
		},
	}
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

	// 2. Validate Space Key matches directory
	spaceKey := filepath.Base(spaceDir)
	if doc.Frontmatter.ConfluenceSpaceKey != spaceKey {
		issues = append(issues, fs.ValidationIssue{
			Field:   "confluence_space_key",
			Code:    "mismatch",
			Message: fmt.Sprintf("space key %q does not match directory name %q", doc.Frontmatter.ConfluenceSpaceKey, spaceKey),
		})
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
