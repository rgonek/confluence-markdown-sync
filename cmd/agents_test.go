package cmd

import "testing"

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
