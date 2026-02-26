package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var inlineSpinnerFrames = []string{"|", "/", "-", "\\"}

const inlineSpinnerInterval = 120 * time.Millisecond

func runWithIndeterminateStatus(out io.Writer, message string, fn func() error) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return fn()
	}

	if !outputSupportsProgress(out) {
		_, _ = fmt.Fprintf(out, "%s...\n", message)
		return fn()
	}

	stop := startInlineSpinner(out, message)
	err := fn()
	stop()
	return err
}

func startInlineSpinner(out io.Writer, message string) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	lineWidth := lipgloss.Width(message) + 2

	go func() {
		ticker := time.NewTicker(inlineSpinnerInterval)
		defer ticker.Stop()

		frameIdx := 0
		for {
			frame := inlineSpinnerFrames[frameIdx%len(inlineSpinnerFrames)]
			frameIdx++
			_, _ = fmt.Fprintf(out, "\r%s %s", frame, message)

			select {
			case <-done:
				_, _ = fmt.Fprintf(out, "\r%s\r", strings.Repeat(" ", lineWidth))
				close(stopped)
				return
			case <-ticker.C:
			}
		}
	}()

	return func() {
		close(done)
		<-stopped
	}
}
