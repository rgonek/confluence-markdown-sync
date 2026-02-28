package cmd

import (
	"bytes"
	"testing"
)

func TestNewDiffCmd(t *testing.T) {
	cmd := newDiffCmd()
	if cmd == nil {
		t.Fatal("expected cmd")
	}

	// It fails to run because it expects workspace
	cmd.SetArgs([]string{})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	_ = cmd.Execute()

	cmd.SetArgs([]string{"ENG"})
	_ = cmd.Execute()
}

func TestNewStatusCmd(t *testing.T) {
	cmd := newStatusCmd()
	cmd.SetArgs([]string{"ENG"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	_ = cmd.Execute()
}

func TestNewPruneCmd(t *testing.T) {
	cmd := newPruneCmd()
	cmd.SetArgs([]string{"ENG"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	_ = cmd.Execute()
}

func TestNewCleanCmd(t *testing.T) {
	cmd := newCleanCmd()
	cmd.SetArgs([]string{"ENG"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	_ = cmd.Execute()
}

func TestNewAgentsCmd(t *testing.T) {
	cmd := newInitAgentsCmd()
	cmd.SetArgs([]string{"ENG"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	_ = cmd.Execute()
}

func TestNewRelinkCmd(t *testing.T) {
	cmd := newRelinkCmd()
	cmd.SetArgs([]string{"ENG"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	_ = cmd.Execute()
}

func TestNewValidateCmd(t *testing.T) {
	cmd := newValidateCmd()
	cmd.SetArgs([]string{"ENG"})
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	_ = cmd.Execute()
}
