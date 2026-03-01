package cmd

import (
	"context"
	"testing"
)

func TestExecuteContext(t *testing.T) {
	// ExecuteContext just calls rootCmd.ExecuteContext
	// We can't easily test it without it running default commands, but we can set args to just "help"
	rootCmd.SetArgs([]string{"help"})
	if err := ExecuteContext(context.Background()); err != nil {
		t.Errorf("ExecuteContext failed: %v", err)
	}

	rootCmd.SetArgs([]string{"help"})
	if err := Execute(); err != nil {
		t.Errorf("Execute failed: %v", err)
	}
}

func TestGetCommandContext(t *testing.T) {
	cmd := newDoctorCmd()
	ctx := getCommandContext(cmd)
	if ctx == nil {
		t.Error("expected context")
	}
}
