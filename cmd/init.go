package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/spf13/cobra"
)

const gitignoreContent = `# Confluence Markdown Sync
.confluence-state.json
.confluence-search-index/
.conf.yaml
.env

# OS artifacts
.DS_Store
Thumbs.db

# Temporary files
*.tmp
*.bak

# Binary
conf
conf.exe
`

const agentsMDTemplate = `# AGENTS

This repository uses ` + "`conf`" + ` (confluence-sync) to manage Confluence documentation as Markdown.

## Recommended Workflow

` + "`conf pull [TARGET]`" + ` → edit Markdown → ` + "`conf validate [TARGET]`" + ` → ` + "`conf diff [TARGET]`" + ` → ` + "`conf push [TARGET]`" + `

Humans may review or approve specific steps, but the workflow is the same regardless of who runs each command.

## Search (Read-Only, Zero API Calls)
Use ` + "`conf search`" + ` to find content without reading entire files.
- **Workflow**: ` + "`conf search \"term\" --format json | <process>`" + ` for structured reads.
- **Filters**: ` + "`--space KEY`" + `, ` + "`--label LABEL`" + `, ` + "`--heading TEXT`" + `, ` + "`--created-by USER`" + `, ` + "`--updated-by USER`" + `, ` + "`--created-after DATE`" + `, ` + "`--created-before DATE`" + `, ` + "`--updated-after DATE`" + `, ` + "`--updated-before DATE`" + `.
- **Facets**: ` + "`conf search --list-labels`" + `, ` + "`conf search --list-spaces`" + `.
- **Index**: built automatically on first use; updated after each ` + "`conf pull`" + `.

## Core Invariants
- **Source of Truth**: Confluence is the primary source of truth for IDs and versions. Local Markdown is the source of truth for content between syncs.
- **Validation**: ` + "`push`" + ` will fail if ` + "`validate`" + ` fails.
- **Frontmatter**:
  - ` + "`id`" + `: Immutable — do not edit.
  - ` + "`version`" + `: Managed by ` + "`conf`" + ` — do not edit.
  - ` + "`state`" + `: Lifecycle state (` + "`draft`" + ` or ` + "`current`" + `). Omitted means ` + "`current`" + `. Cannot revert to ` + "`draft`" + ` once published.
  - ` + "`status`" + `: Confluence visual lozenge (e.g., "Ready to review").
  - ` + "`labels`" + `: Confluence page labels (array of strings).
- **State**: ` + "`.confluence-state.json`" + ` tracks sync state. Do not delete.

## Directory Hierarchy Convention

To create a Confluence page that has subpages, use a directory named after the parent page and place the parent page file inside it with the **same name as the directory**:

` + "```" + `
Space/
  ParentPage/
    ParentPage.md       ← parent page content
    SubpageOne.md       ← child of ParentPage
    SubpageTwo.md       ← child of ParentPage
    NestedGroup/
      NestedGroup.md    ← child of ParentPage, parent of its own children
      NestedChild.md
` + "```" + `

- The parent page file **must** be ` + "`DirectoryName/DirectoryName.md`" + ` (filename matches directory name).
- Sibling ` + "`.md`" + ` files inside the directory become subpages of that parent.
- This mirrors the Confluence page tree hierarchy in the local filesystem.

## Content Support Contract
- **Same-space links**: Relative Markdown links between pages in the same space are fully supported.
- **Cross-space links**: Relative links to pages in sibling space directories are resolved at push time.
- **Attachments**: Images and files stored in ` + "`assets/`" + ` are uploaded as Confluence page attachments.
- **PlantUML**: Rendered round-trip support via the ` + "`plantumlcloud`" + ` Confluence macro.
- **Mermaid**: Preserved as fenced code blocks; pushed as ADF ` + "`codeBlock`" + ` (not rendered as a Confluence diagram). ` + "`validate`" + ` warns with ` + "`MERMAID_PRESERVED_AS_CODEBLOCK`" + `.
- **Hierarchy**: Pages with children use the ` + "`ParentPage/ParentPage.md`" + ` convention; moves are surfaced as ` + "`PAGE_PATH_MOVED`" + ` diagnostics.

## Documentation Strategy
Specs and PRDs generated in this workspace should be maintained as the working source of truth for feature behavior and product intent. When behavior or requirements are unclear, refer to the primary plan (if one exists) or to the relevant Spec/PRD document.

## Space-Specific Rules
Each space directory (e.g., ` + "`Technical documentation (TD)/`" + `) may contain its own ` + "`AGENTS.md`" + ` with space-specific content rules (e.g., required templates, PII guidelines). Check those if they exist.
`

const readmeMDTemplate = `# Confluence Markdown Sync

This workspace is managed by [conf](https://github.com/rgonek/confluence-markdown-sync).


## Quick Start

` + "```sh" + `
# Pull latest from Confluence
conf pull <SPACE_KEY>

# Edit .md files, then push
conf push <SPACE_KEY>

# Validate before pushing
conf validate <SPACE_KEY>

# See what changed remotely
conf diff <SPACE_KEY>
` + "```" + `

## Authentication

Set the following environment variables (or add them to ` + "`.env`" + `):

` + "```" + `
ATLASSIAN_DOMAIN=https://your-domain.atlassian.net
ATLASSIAN_EMAIL=you@example.com
ATLASSIAN_API_TOKEN=<your-api-token>
` + "```" + `

## Notes
- Frontmatter fields:
  - ` + "`id`" + `: Immutable — do not edit.
  - ` + "`version`" + `: Managed by ` + "`conf`" + `.
  - ` + "`state`" + `: Lifecycle state (` + "`draft`" + ` or ` + "`current`" + `).
  - ` + "`status`" + `: Confluence visual lozenge (e.g., "Ready to review").
  - ` + "`labels`" + `: Confluence page labels (list).
- ` + "`.confluence-state.json`" + ` is local state and is gitignored.
- Recovery from a failed push is CLI-guided — no manual Git commands required.
`

func newInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a conf workspace",
		Long: `Init sets up the current directory as a conf workspace.

It will:
  - Verify git is installed (and initialize a repo on branch 'main' if needed)
  - Create or update .gitignore
  - Prompt for Atlassian credentials and create a .env file if missing
  - Create AGENTS.md and README.md if they do not exist`,
		Args: cobra.NoArgs,
		RunE: runInit,
	}

	cmd.AddCommand(newInitAgentsCmd())

	return cmd
}

func runInit(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	repoCreated := false

	ok := func(msg string) { _, _ = fmt.Fprintln(out, successStyle.Render("✓ "+msg)) }

	// 1. Verify git is installed.
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is required but was not found in PATH: %w", err)
	}
	ok("git found")

	// 2. Initialize git repo if not already inside one.
	if !isInsideGitRepo() {
		_, _ = fmt.Fprintln(out, "Initializing git repository on branch 'main'...")
		if out, err := exec.Command("git", "init", "-b", "main").CombinedOutput(); err != nil {
			return fmt.Errorf("git init failed: %s: %w", strings.TrimSpace(string(out)), err)
		}
		ok("git repository initialized")
		repoCreated = true
	} else {
		ok("existing git repository detected")
	}

	// 3. Create or update .gitignore.
	if err := ensureGitignore(); err != nil {
		return fmt.Errorf("failed to update .gitignore: %w", err)
	}
	ok(".gitignore updated")

	// 4. Ensure .env exists (prompt if missing).
	envCreated, err := ensureDotEnv(cmd)
	if err != nil {
		return fmt.Errorf("failed to create .env: %w", err)
	}
	if envCreated {
		ok(".env created")
	} else {
		ok(".env already exists")
	}

	// 5. Create AGENTS.md if missing.
	if err := createIfMissing("AGENTS.md", agentsMDTemplate); err != nil {
		return fmt.Errorf("failed to create AGENTS.md: %w", err)
	}
	ok("AGENTS.md ready")

	// 6. Create README.md if missing.
	if err := createIfMissing("README.md", readmeMDTemplate); err != nil {
		return fmt.Errorf("failed to create README.md: %w", err)
	}
	ok("README.md ready")

	if repoCreated {
		committed, err := createInitCommit()
		if err != nil {
			return err
		}
		if committed {
			ok("initial commit created")
		} else {
			ok("initial commit skipped (no staged changes)")
		}
	}

	_, _ = fmt.Fprintln(out, "\n"+headingStyle.Render("conf workspace initialized successfully."))
	return nil
}

// isInsideGitRepo returns true if the current directory is inside a git repo.
func isInsideGitRepo() bool {
	err := exec.Command("git", "rev-parse", "--git-dir").Run()
	return err == nil
}

// ensureGitignore appends required conf entries to .gitignore, creating it if necessary.
func ensureGitignore() error {
	const path = ".gitignore"

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	content := string(existing)
	var missing []string
	for _, entry := range []string{".confluence-state.json", ".confluence-search-index/", ".conf.yaml", ".env"} {
		if !containsLine(content, entry) {
			missing = append(missing, entry)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	// Ensure we start on a new line.
	if len(existing) > 0 && !strings.HasSuffix(content, "\n") {
		if _, err := fmt.Fprintln(f); err != nil {
			return err
		}
	}
	if len(existing) == 0 {
		// Write full template for a new file.
		_, err = f.WriteString(gitignoreContent)
		return err
	}
	// Append only the missing entries.
	for _, e := range missing {
		if _, err := fmt.Fprintln(f, e); err != nil {
			return err
		}
	}
	return nil
}

// ensureDotEnv creates .env with prompted credentials; returns true if file was created.
// When running in a TTY it uses a huh.Form with password masking for the API token.
// In non-TTY environments (pipes, tests) it falls back to plain-text prompts.
func ensureDotEnv(cmd *cobra.Command) (bool, error) {
	if _, err := os.Stat(".env"); err == nil {
		return false, nil
	}

	out := cmd.OutOrStdout()
	if cfg, ok, err := loadInitEnvScaffoldConfig(); err != nil {
		return false, err
	} else if ok {
		_, _ = fmt.Fprintln(out, "\nNo .env file found. Scaffolding it from existing Atlassian environment variables.")
		return true, writeDotEnvFile(*cfg)
	}

	_, _ = fmt.Fprintln(out, "\nNo .env file found. Please enter your Atlassian credentials.")

	var domain, email, token string

	if outputSupportsProgress(out) {
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("ATLASSIAN_DOMAIN").
					Placeholder("https://your-domain.atlassian.net").
					Value(&domain),
				huh.NewInput().
					Title("ATLASSIAN_EMAIL").
					Value(&email),
				huh.NewInput().
					Title("ATLASSIAN_API_TOKEN").
					EchoMode(huh.EchoModePassword).
					Value(&token),
			),
		).WithOutput(out)
		if err := form.Run(); err != nil {
			return false, err
		}
	} else {
		// Plain-text fallback for non-TTY environments.
		in := cmd.InOrStdin()
		scanner := bufio.NewScanner(in)
		domain = promptField(scanner, out, "ATLASSIAN_DOMAIN (e.g. https://your-domain.atlassian.net)")
		email = promptField(scanner, out, "ATLASSIAN_EMAIL")
		token = promptField(scanner, out, "ATLASSIAN_API_TOKEN")
	}

	return true, writeDotEnvFile(config.Config{
		Domain:   domain,
		Email:    email,
		APIToken: token,
	})
}

func loadInitEnvScaffoldConfig() (*config.Config, bool, error) {
	cfg, err := config.Load("")
	if err == nil {
		return cfg, true, nil
	}
	if errors.Is(err, config.ErrMissingConfig) {
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("resolve environment-backed auth for .env scaffolding: %w", err)
}

func writeDotEnvFile(cfg config.Config) error {
	lines := []string{
		"# Atlassian / Confluence credentials",
		fmt.Sprintf("ATLASSIAN_DOMAIN=%s", strings.TrimRight(cfg.Domain, "/")),
		fmt.Sprintf("ATLASSIAN_EMAIL=%s", strings.TrimSpace(cfg.Email)),
		fmt.Sprintf("ATLASSIAN_API_TOKEN=%s", strings.TrimSpace(cfg.APIToken)),
	}

	return os.WriteFile(".env", []byte(strings.Join(lines, "\n")+"\n"), 0o600) //nolint:gosec // Writing static filename
}

func promptField(scanner *bufio.Scanner, out interface{ Write([]byte) (int, error) }, label string) string {
	_, _ = fmt.Fprintf(out, "  %s: ", label)
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
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(content), 0o600)
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

func createInitCommit() (bool, error) {
	paths := []string{".gitignore", "AGENTS.md", "README.md"}
	toStage := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			ignored, err := isGitIgnored(p)
			if err != nil {
				return false, err
			}
			if ignored {
				continue
			}
			toStage = append(toStage, p)
		}
	}
	if len(toStage) == 0 {
		return false, nil
	}

	addArgs := append([]string{"add", "--"}, toStage...)
	gitAdd := exec.Command("git", addArgs...) //nolint:gosec // arguments are fixed git flags and local workspace paths
	if out, err := gitAdd.CombinedOutput(); err != nil {
		return false, fmt.Errorf("git add failed: %s", strings.TrimSpace(string(out)))
	}

	hasStaged, err := hasStagedChanges()
	if err != nil {
		return false, err
	}
	if !hasStaged {
		return false, nil
	}

	commitOut, err := exec.Command("git", "commit", "-m", "chore: initialize conf workspace").CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(commitOut))
		if strings.Contains(msg, "Please tell me who you are") || strings.Contains(msg, "unable to auto-detect email address") {
			return false, fmt.Errorf("git commit failed: missing git identity; set user.name and user.email, then rerun init")
		}
		return false, fmt.Errorf("git commit failed: %s", msg)
	}

	return true, nil
}

func hasStagedChanges() (bool, error) {
	err := exec.Command("git", "diff", "--cached", "--quiet").Run()
	if err == nil {
		return false, nil
	}
	exitErr, ok := err.(*exec.ExitError)
	if ok && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("check staged changes: %w", err)
}

func isGitIgnored(path string) (bool, error) {
	err := exec.Command("git", "check-ignore", "--quiet", "--", path).Run() //nolint:gosec // Intended use of git command
	if err == nil {
		return true, nil
	}
	exitErr, ok := err.(*exec.ExitError)
	if ok && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("check ignore status for %s: %w", path, err)
}
