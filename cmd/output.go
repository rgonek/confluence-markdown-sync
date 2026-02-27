package cmd

import (
	"io"
	"sync"

	"github.com/spf13/cobra"
	"golang.org/x/term"
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

type fdWriter interface {
	Fd() uintptr
}

var outputSupportsProgress = func(out io.Writer) bool {
	if synced, ok := out.(*synchronizedWriter); ok {
		out = synced.w
	}
	fileLike, ok := out.(fdWriter)
	if !ok {
		return false
	}
	return term.IsTerminal(int(fileLike.Fd()))
}

func outputTerminalWidth(out io.Writer) int {
	if synced, ok := out.(*synchronizedWriter); ok {
		out = synced.w
	}
	fileLike, ok := out.(fdWriter)
	if !ok {
		return 0
	}
	width, _, err := term.GetSize(int(fileLike.Fd())) //nolint:gosec // Fd is small and fits in int
	if err != nil || width <= 0 {
		return 0
	}
	return width
}
