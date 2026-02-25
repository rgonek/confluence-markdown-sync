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

func TestConsoleProgress_AddUpdatesView(t *testing.T) {
	out := &bytes.Buffer{}
	p := newConsoleProgress(out, "Syncing")
	p.SetTotal(10)
	p.Add(5)

	view := p.model.View()
	if !strings.Contains(view, "5/10") {
		t.Fatalf("expected view to contain 5/10, got %q", view)
	}
}

func TestConsoleProgress_SetTotalResetsCount(t *testing.T) {
	out := &bytes.Buffer{}
	p := newConsoleProgress(out, "Syncing")
	p.SetTotal(10)
	p.Add(5)
	p.SetTotal(20)

	if p.model.current != 0 {
		t.Fatalf("current = %d after SetTotal, want 0", p.model.current)
	}
	if p.model.total != 20 {
		t.Fatalf("total = %d, want 20", p.model.total)
	}
}
