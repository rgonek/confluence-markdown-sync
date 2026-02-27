package cmd

import (
	"os"
	"path/filepath"
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
	defer os.Chdir(oldWD)
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
