package cmd

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestAskToContinueOnDownloadError(t *testing.T) {
	out := new(bytes.Buffer)

	oldSupportsProgress := outputSupportsProgress
	outputSupportsProgress = func(out io.Writer) bool { return false }
	defer func() { outputSupportsProgress = oldSupportsProgress }()

	oldNI := flagNonInteractive
	flagNonInteractive = true
	if askToContinueOnDownloadError(nil, out, "att1", "page1", nil) {
		t.Error("expected false for non-interactive")
	}
	flagNonInteractive = oldNI

	oldYes := flagYes
	flagYes = true
	if !askToContinueOnDownloadError(nil, out, "att1", "page1", nil) {
		t.Error("expected true for yes flag")
	}
	flagYes = oldYes

	in := strings.NewReader("n\n")
	if askToContinueOnDownloadError(in, out, "att1", "page1", nil) {
		t.Error("expected false when answering no")
	}

	inYes := strings.NewReader("y\n")
	if !askToContinueOnDownloadError(inYes, out, "att1", "page1", nil) {
		t.Error("expected true when answering yes")
	}
}

func TestReadPromptLine_EOF(t *testing.T) {
	in := strings.NewReader("")
	res, err := readPromptLine(in)
	if err != nil {
		t.Errorf("unexpected error for EOF: %v", err)
	}
	if res != "" {
		t.Errorf("expected empty string for EOF, got %q", res)
	}
}
