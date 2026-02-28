package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestProgress_Truncation(t *testing.T) {
	if got := truncateLeftWithEllipsis("1234567890", 5); got != "...90" {
		t.Errorf("left truncate failed: %q", got)
	}
	if got := truncateRightWithEllipsis("1234567890", 5); got != "12..." {
		t.Errorf("right truncate failed: %q", got)
	}
}

func TestProgress_ConsoleProgress(t *testing.T) {
	out := new(bytes.Buffer)
	p := newConsoleProgress(out, "starting")
	p.SetDescription("running")
	p.SetTotal(10)
	p.SetCurrentItem("item1")
	p.Add(2)
	p.Done()

	output := out.String()
	if !strings.Contains(output, "running") {
		t.Errorf("expected output to contain description, got %q", output)
	}
}
