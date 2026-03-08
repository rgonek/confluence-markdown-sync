package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
)

func TestNewInitCmd_RegistersAgentsSubcommand(t *testing.T) {
	runParallelCommandTest(t)
	initCmd := newInitCmd()

	foundCmd, _, err := initCmd.Find([]string{"agents"})
	if err != nil {
		t.Fatalf("find init agents command: %v", err)
	}
	if foundCmd == nil || foundCmd.Name() != "agents" {
		t.Fatalf("init agents command not registered")
	}
}

func TestRootCommand_DoesNotRegisterTopLevelAgents(t *testing.T) {
	runParallelCommandTest(t)
	for _, subcommand := range rootCmd.Commands() {
		if subcommand.Name() == "agents" {
			t.Fatalf("unexpected top-level agents command registration")
		}
	}
}

func TestRootCommand_RegistersInitAgentsSubcommand(t *testing.T) {
	runParallelCommandTest(t)
	var initCmdName string
	for _, subcommand := range rootCmd.Commands() {
		if subcommand.Name() == "init" {
			for _, initSubcommand := range subcommand.Commands() {
				if initSubcommand.Name() == "agents" {
					return
				}
			}
			initCmdName = subcommand.Name()
		}
	}

	if initCmdName == "" {
		t.Fatalf("init command not registered on root")
	}
	t.Fatalf("init agents subcommand not registered")
}

func TestRunAgentsInit(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()

	oldWD, _ := os.Getwd()
	defer func() {
		_ = os.Chdir(oldWD)
	}()
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}

	cmd := newInitAgentsCmd()
	target := config.Target{Value: "", Mode: config.TargetModeSpace}

	// Test default init (Tech)
	if err := runAgentsInit(cmd, target, "technical-documentation"); err != nil {
		t.Fatalf("runAgentsInit default failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md not created: %v", err)
	}
	// Clean up for next template test
	_ = os.Remove(filepath.Join(repo, "AGENTS.md"))

	// Test HR
	if err := runAgentsInit(cmd, target, "hr"); err != nil {
		t.Fatalf("runAgentsInit hr failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md not created for hr: %v", err)
	}
	_ = os.Remove(filepath.Join(repo, "AGENTS.md"))

	// Test PM
	if err := runAgentsInit(cmd, target, "pm"); err != nil {
		t.Fatalf("runAgentsInit pm failed: %v", err)
	}
	_ = os.Remove(filepath.Join(repo, "AGENTS.md"))

	// Test PRD
	if err := runAgentsInit(cmd, target, "prd"); err != nil {
		t.Fatalf("runAgentsInit prd failed: %v", err)
	}
	_ = os.Remove(filepath.Join(repo, "AGENTS.md"))

	// Test Support
	if err := runAgentsInit(cmd, target, "support"); err != nil {
		t.Fatalf("runAgentsInit support failed: %v", err)
	}
	_ = os.Remove(filepath.Join(repo, "AGENTS.md"))

	// Test General
	if err := runAgentsInit(cmd, target, "general"); err != nil {
		t.Fatalf("runAgentsInit general failed: %v", err)
	}
}

func TestAgentsMDTemplateAlignment(t *testing.T) {
	runParallelCommandTest(t)
	content := agentsMDTemplate

	// Must NOT contain old split workflow references.
	for _, banned := range []string{"Human-in-the-Loop", "Full Agentic Use"} {
		if strings.Contains(content, banned) {
			t.Errorf("workspace AGENTS.md still contains stale %q reference", banned)
		}
	}

	// Must contain unified workflow.
	for _, required := range []string{"Recommended Workflow", "validate", "diff", "push"} {
		if !strings.Contains(content, required) {
			t.Errorf("workspace AGENTS.md missing required %q", required)
		}
	}

	// Frontmatter: id immutable, version managed, space NOT listed as immutable frontmatter key.
	if strings.Contains(content, "`space`: Immutable") || strings.Contains(content, "`space`"+": Immutable") {
		t.Error("workspace AGENTS.md still lists space as immutable frontmatter")
	}
	if !strings.Contains(content, "`id`") {
		t.Error("workspace AGENTS.md missing id frontmatter reference")
	}
	if !strings.Contains(content, "`version`") {
		t.Error("workspace AGENTS.md missing version frontmatter reference")
	}
	if !strings.Contains(content, "remove `id` and `version` from the copy before pushing") {
		t.Error("workspace AGENTS.md missing copy-page safety guidance for id/version")
	}

	// Must contain Content Support Contract section.
	if !strings.Contains(content, "Content Support Contract") {
		t.Error("workspace AGENTS.md missing Content Support Contract section")
	}

	// Root workspace AGENTS.md should stay operational and self-contained.
	for _, banned := range []string{"Documentation Strategy", "Specs and PRDs", "Spec/PRD document"} {
		if strings.Contains(content, banned) {
			t.Errorf("workspace AGENTS.md should not reference repo docs/specs via %q", banned)
		}
	}

	// Must mention Mermaid preservation behavior.
	if !strings.Contains(content, "MERMAID_PRESERVED_AS_CODEBLOCK") {
		t.Error("workspace AGENTS.md missing MERMAID_PRESERVED_AS_CODEBLOCK diagnostic reference")
	}
}

func TestReadmeMDTemplateAlignment(t *testing.T) {
	runParallelCommandTest(t)
	content := readmeMDTemplate

	// space must NOT be listed as an immutable key.
	if strings.Contains(content, "`id`, `space`: Immutable") || strings.Contains(content, "`space`: Immutable") {
		t.Error("README.md template still lists space as an immutable frontmatter key")
	}

	// id must still be documented as immutable.
	if !strings.Contains(content, "`id`") {
		t.Error("README.md template missing id frontmatter reference")
	}
}

func TestSpaceTemplates_NoStaleSpaceImmutableReference(t *testing.T) {
	runParallelCommandTest(t)

	type templateCase struct {
		name    string
		content string
	}

	cases := []templateCase{
		{"tech", getTechAgentsTemplate("TEST")},
		{"hr", getHRAgentsTemplate("TEST")},
		{"pm", getPMAgentsTemplate("TEST")},
		{"prd", getPRDAgentsTemplate("TEST")},
		{"support", getSupportAgentsTemplate("TEST")},
		{"general", getGeneralAgentsTemplate("TEST")},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// No template should reference `space` as an immutable frontmatter key.
			if strings.Contains(tc.content, "`id` or `space`") ||
				strings.Contains(tc.content, "`space`: Immutable") {
				t.Errorf("%s template still references space as an immutable frontmatter key", tc.name)
			}

			// Each template must mention id and version in its frontmatter guidance.
			if !strings.Contains(tc.content, "`id`") {
				t.Errorf("%s template missing id reference", tc.name)
			}
			if !strings.Contains(tc.content, "`version`") {
				t.Errorf("%s template missing version reference", tc.name)
			}
		})
	}
}

func TestTechAgentsTemplate_ContentNotes(t *testing.T) {
	runParallelCommandTest(t)
	content := getTechAgentsTemplate("TEST")

	// Must mention Mermaid preservation behavior.
	if !strings.Contains(content, "codeBlock") {
		t.Error("tech template missing Mermaid/codeBlock note")
	}

	// Must mention cross-space link support.
	if !strings.Contains(content, "Cross-space") {
		t.Error("tech template missing cross-space links note")
	}
}

func TestGeneralAgentsTemplate_FrontmatterGuidance(t *testing.T) {
	runParallelCommandTest(t)
	content := getGeneralAgentsTemplate("TEST")

	// Must say "id or version", not "id or space".
	if strings.Contains(content, "`id` or `space`") {
		t.Error("general template still says 'id or space' instead of 'id or version'")
	}
	if !strings.Contains(content, "`id`") || !strings.Contains(content, "`version`") {
		t.Error("general template missing id or version reference")
	}
}
