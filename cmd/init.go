package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const gitignoreContent = `# Confluence Markdown Sync
.confluence-state.json
.env

# OS artifacts
.DS_Store
Thumbs.db

# Temporary files
*.tmp
*.bak

# Binary
cms
cms.exe
`

const agentsMDTemplate = `# AGENTS

## Purpose
This repository uses ` + "`cms`" + ` (confluence-sync) to sync Confluence pages with local Markdown files.

## Core Invariants
- ` + "`push`" + ` must always run ` + "`validate`" + ` before any remote write.
- Immutable frontmatter keys (do not edit manually):
  - ` + "`confluence_page_id`" + `
  - ` + "`confluence_space_key`" + `
- Mutable-by-sync frontmatter keys (managed by ` + "`cms`" + ` only):
  - ` + "`confluence_version`" + `
  - ` + "`confluence_last_modified`" + `
  - ` + "`confluence_parent_page_id`" + `

## Workflow
- ` + "`cms pull [SPACE_KEY]`" + ` — fetch remote changes and update local Markdown files.
- ` + "`cms push [SPACE_KEY]`" + ` — validate and publish local changes to Confluence.
- ` + "`cms validate [SPACE_KEY]`" + ` — check frontmatter and conversion before push.
- ` + "`cms diff [SPACE_KEY]`" + ` — compare local files with remote Confluence content.

## AI-Safe Rules
- Do not modify ` + "`confluence_page_id`" + ` or ` + "`confluence_space_key`" + ` frontmatter fields.
- Do not delete or rename ` + "`.confluence-state.json`" + ` (it is gitignored; managed by ` + "`cms`" + `).
- Do not commit ` + "`.env`" + ` files (they contain API credentials).
`

const readmeMDTemplate = `# Confluence Markdown Sync

This workspace is managed by [cms](https://github.com/rgonek/confluence-markdown-sync).

## Quick Start

` + "```sh" + `
# Pull latest from Confluence
cms pull <SPACE_KEY>

# Edit .md files, then push
cms push <SPACE_KEY>

# Validate before pushing
cms validate <SPACE_KEY>

# See what changed remotely
cms diff <SPACE_KEY>
` + "```" + `

## Authentication

Set the following environment variables (or add them to ` + "`.env`" + `):

` + "```" + `
ATLASSIAN_DOMAIN=https://your-domain.atlassian.net
ATLASSIAN_EMAIL=you@example.com
ATLASSIAN_API_TOKEN=<your-api-token>
` + "```" + `

## Notes
- Frontmatter fields ` + "`confluence_page_id`" + ` and ` + "`confluence_space_key`" + ` are immutable — do not edit them.
- ` + "`.confluence-state.json`" + ` is local state and is gitignored.
- Recovery from a failed push is CLI-guided — no manual Git commands required.
`

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a cms workspace",
		Long: `Init sets up the current directory as a cms workspace.

It will:
  - Verify git is installed (and initialize a repo on branch 'main' if needed)
  - Create or update .gitignore
  - Prompt for Atlassian credentials and create a .env file if missing
  - Create AGENTS.md and README.md if they do not exist`,
		Args: cobra.NoArgs,
		RunE: runInit,
	}
}

func runInit(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()

	// 1. Verify git is installed.
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is required but was not found in PATH: %w", err)
	}
	fmt.Fprintln(out, "✓ git found")

	// 2. Initialize git repo if not already inside one.
	if !isInsideGitRepo() {
		fmt.Fprintln(out, "Initializing git repository on branch 'main'...")
		if out, err := exec.Command("git", "init", "-b", "main").CombinedOutput(); err != nil {
			return fmt.Errorf("git init failed: %s: %w", strings.TrimSpace(string(out)), err)
		}
		fmt.Fprintln(cmd.OutOrStdout(), "✓ git repository initialized")
	} else {
		fmt.Fprintln(out, "✓ existing git repository detected")
	}

	// 3. Create or update .gitignore.
	if err := ensureGitignore(); err != nil {
		return fmt.Errorf("failed to update .gitignore: %w", err)
	}
	fmt.Fprintln(out, "✓ .gitignore updated")

	// 4. Ensure .env exists (prompt if missing).
	envCreated, err := ensureDotEnv(cmd)
	if err != nil {
		return fmt.Errorf("failed to create .env: %w", err)
	}
	if envCreated {
		fmt.Fprintln(out, "✓ .env created")
	} else {
		fmt.Fprintln(out, "✓ .env already exists")
	}

	// 5. Create AGENTS.md if missing.
	if err := createIfMissing("AGENTS.md", agentsMDTemplate); err != nil {
		return fmt.Errorf("failed to create AGENTS.md: %w", err)
	}
	fmt.Fprintln(out, "✓ AGENTS.md ready")

	// 6. Create README.md if missing.
	if err := createIfMissing("README.md", readmeMDTemplate); err != nil {
		return fmt.Errorf("failed to create README.md: %w", err)
	}
	fmt.Fprintln(out, "✓ README.md ready")

	fmt.Fprintln(out, "\ncms workspace initialized successfully.")
	return nil
}

// isInsideGitRepo returns true if the current directory is inside a git repo.
func isInsideGitRepo() bool {
	err := exec.Command("git", "rev-parse", "--git-dir").Run()
	return err == nil
}

// ensureGitignore appends required cms entries to .gitignore, creating it if necessary.
func ensureGitignore() error {
	const path = ".gitignore"

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	content := string(existing)
	var missing []string
	for _, entry := range []string{".confluence-state.json", ".env"} {
		if !containsLine(content, entry) {
			missing = append(missing, entry)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Ensure we start on a new line.
	if len(existing) > 0 && !strings.HasSuffix(content, "\n") {
		fmt.Fprintln(f)
	}
	if len(existing) == 0 {
		// Write full template for a new file.
		_, err = f.WriteString(gitignoreContent)
		return err
	}
	// Append only the missing entries.
	for _, e := range missing {
		fmt.Fprintln(f, e)
	}
	return nil
}

// ensureDotEnv creates .env with prompted credentials; returns true if file was created.
func ensureDotEnv(cmd *cobra.Command) (bool, error) {
	if _, err := os.Stat(".env"); err == nil {
		return false, nil
	}

	in := cmd.InOrStdin()
	out := cmd.OutOrStdout()

	fmt.Fprintln(out, "\nNo .env file found. Please enter your Atlassian credentials.")
	scanner := bufio.NewScanner(in)

	domain := promptField(scanner, out, "ATLASSIAN_DOMAIN (e.g. https://your-domain.atlassian.net)")
	email := promptField(scanner, out, "ATLASSIAN_EMAIL")
	token := promptField(scanner, out, "ATLASSIAN_API_TOKEN")

	lines := []string{
		"# Atlassian / Confluence credentials",
		fmt.Sprintf("ATLASSIAN_DOMAIN=%s", strings.TrimRight(domain, "/")),
		fmt.Sprintf("ATLASSIAN_EMAIL=%s", email),
		fmt.Sprintf("ATLASSIAN_API_TOKEN=%s", token),
	}

	return true, os.WriteFile(".env", []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func promptField(scanner *bufio.Scanner, out interface{ Write([]byte) (int, error) }, label string) string {
	fmt.Fprintf(out, "  %s: ", label)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}
	return ""
}

// createIfMissing creates path with content only if the file does not exist.
func createIfMissing(path, content string) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// containsLine reports whether s contains the given line.
func containsLine(s, line string) bool {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}
