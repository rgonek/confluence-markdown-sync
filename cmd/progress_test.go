package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestConsoleProgress_SetDescriptionUpdatesState(t *testing.T) {
	oldDelay := progressDescriptionSwitchDelay
	progressDescriptionSwitchDelay = 0
	t.Cleanup(func() { progressDescriptionSwitchDelay = oldDelay })

	out := &bytes.Buffer{}
	p := newConsoleProgress(out, "Syncing")
	p.SetDescription("Analyzing")

	if p.description != "Analyzing" {
		t.Fatalf("description = %q, want Analyzing", p.description)
	}
}

func TestConsoleProgress_SetCurrentItemRetainsBaseDescription(t *testing.T) {
	out := &bytes.Buffer{}
	p := newConsoleProgress(out, "Syncing")

	p.SetCurrentItem(strings.Repeat("x", 64))
	if p.description != "Syncing" {
		t.Fatalf("description = %q, want Syncing", p.description)
	}

	p.SetCurrentItem("")
	if p.description != "Syncing" {
		t.Fatalf("description after reset = %q, want Syncing", p.description)
	}
}

func TestConsoleProgress_DoneWritesCarriageReturn(t *testing.T) {
	out := &bytes.Buffer{}
	p := newConsoleProgress(out, "Syncing")
	p.Done()

	if !strings.Contains(out.String(), "\r") {
		t.Fatalf("expected done output to include carriage return, got %q", out.String())
	}
}
