package cmd

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestVersionCommand_PrintsVersion(t *testing.T) {
	runParallelCommandTest(t)
	previousVersion := Version
	Version = "v1.2.3"
	t.Cleanup(func() { Version = previousVersion })

	cmd := newVersionCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != "v1.2.3" {
		t.Fatalf("version output = %q, want %q", got, "v1.2.3")
	}
}

func TestRootVersionFlag_PrintsVersion(t *testing.T) {
	runParallelCommandTest(t)
	previousVersion := Version
	Version = "v9.9.9"
	t.Cleanup(func() { Version = previousVersion })

	previousFlagVersion := flagVersion
	flagVersion = false
	t.Cleanup(func() { flagVersion = previousFlagVersion })

	out := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(io.Discard)
	rootCmd.SetArgs([]string{"--version"})
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
	})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("root command failed: %v", err)
	}

	if got := strings.TrimSpace(out.String()); got != "v9.9.9" {
		t.Fatalf("version output = %q, want %q", got, "v9.9.9")
	}
}
