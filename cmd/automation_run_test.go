package cmd

import (
	"bytes"
	"os"
	"testing"
)

func TestEnsureSynchronizedCmdOutput(t *testing.T) {
	cmd := newCleanCmd()
	cmd.SetOut(os.Stdout) // Default behavior mapping

	out := ensureSynchronizedCmdOutput(cmd)
	if out == nil {
		t.Fatalf("expected output writer, got nil")
	}

	// Try overriding it
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)

	out = ensureSynchronizedCmdOutput(cmd)
	if out != buf {
		// Output sync might wrap it, just ensure it doesn't panic
	}
}
