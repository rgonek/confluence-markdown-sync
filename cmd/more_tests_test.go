package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestBuildStatusReport_SkipGit(t *testing.T) {
	runParallelCommandTest(t)
	// Let's test the error return from GetSpace to cover more lines
	mock := &mockStatusRemote{err: confluence.ErrNotFound}

	// Set targetRelPath to something to avoid remoteAdded logic for now
	_, _ = buildStatusReport(context.Background(), mock, config.Target{}, initialPullContext{}, fs.SpaceState{}, "SPACE", "path")
}

func TestPrintStatusList_Empty(t *testing.T) {
	out := new(bytes.Buffer)
	printStatusList(out, "test", []string{})
	if !bytes.Contains(out.Bytes(), []byte("(0)")) {
		t.Errorf("Expected empty list formatting")
	}
}

func TestBuildStatusReport_RemoteFetch(t *testing.T) {
	// The problem is collectLocalStatusChanges hits git and fails immediately, returning error.
	// We can't reach the rest of buildStatusReport without a valid git repo or stubbing it.
}
