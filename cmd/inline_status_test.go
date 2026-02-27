package cmd

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestRunWithIndeterminateStatus(t *testing.T) {
	runParallelCommandTest(t)
	out := new(bytes.Buffer)

	// A quick successful run
	err := runWithIndeterminateStatus(out, "Doing something...", func() error {
		time.Sleep(10 * time.Millisecond)
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// An indeterminate status with error
	err = runWithIndeterminateStatus(out, "Failing...", func() error {
		return context.DeadlineExceeded
	})
	if err != context.DeadlineExceeded {
		t.Fatalf("expected deadline exceeded error, got %v", err)
	}
}

func TestStartInlineSpinner(t *testing.T) {
	runParallelCommandTest(t)
	out := new(bytes.Buffer)
	cancel := startInlineSpinner(out, "Testing Spinner...")
	time.Sleep(10 * time.Millisecond)
	cancel()
}

func TestStartInlineSpinner_SlowExecution(t *testing.T) {
	runParallelCommandTest(t)
	out := new(bytes.Buffer)
	cancel := startInlineSpinner(out, "Testing Slow...")
	// Wait long enough for the spinner ticker to fire multiple times
	time.Sleep(150 * time.Millisecond)
	cancel()

	// Wait a moment for goroutine to fully stop to prevent data races in checks
	time.Sleep(10 * time.Millisecond)

	output := out.String()
	// Should contain carriage returns to reset line
	if !bytes.Contains([]byte(output), []byte("\r")) {
		t.Fatalf("expected carriage returns in spinner output, got: %q", output)
	}
}
