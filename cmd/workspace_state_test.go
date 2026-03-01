package cmd

import (
	"errors"
	"strings"
	"testing"
)

func TestTranslateWorkspaceGitError(t *testing.T) {
	err := errors.New("needs merge")
	trans := translateWorkspaceGitError(err, "push")
	if !strings.Contains(trans.Error(), "syncing state with unresolved files") {
		t.Errorf("expected translation, got %v", trans)
	}

	err = errors.New("something else")
	trans = translateWorkspaceGitError(err, "push")
	if trans.Error() != "something else" {
		t.Errorf("expected wrapped error, got %v", trans)
	}
}

func TestSummarizePaths(t *testing.T) {
	paths := []string{"a", "b", "c"}
	if summarizePaths(paths, 2) != "a, b, +1 more" {
		t.Errorf("failed summarize 2: %v", summarizePaths(paths, 2))
	}
	if summarizePaths(paths, 5) != "a, b, c" {
		t.Errorf("failed summarize 5: %v", summarizePaths(paths, 5))
	}
}
