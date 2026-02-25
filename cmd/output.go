package cmd

import (
	"io"
	"sync"

	"github.com/spf13/cobra"
)

type synchronizedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (w *synchronizedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.w.Write(p)
}

func ensureSynchronizedCmdOutput(cmd *cobra.Command) io.Writer {
	if out, ok := cmd.OutOrStdout().(*synchronizedWriter); ok {
		return out
	}

	out := &synchronizedWriter{w: cmd.OutOrStdout()}
	cmd.SetOut(out)
	return out
}
