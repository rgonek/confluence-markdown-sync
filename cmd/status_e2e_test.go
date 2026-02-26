package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
)

func TestCollectLocalStatusChanges_Success(t *testing.T) {
	tempDir := t.TempDir()

	oldWd, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldWd)

	exec.Command("git", "init").Run()
	client, err := git.NewClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	spaceDir := filepath.Join(tempDir, "TEST")
	os.MkdirAll(spaceDir, 0755)

	os.WriteFile(filepath.Join(spaceDir, "page1.md"), []byte("---\nid: \"1\"\nversion: 1\n---\ntest\n"), 0644)

	client.Run("add", ".")
	client.Run("commit", "-m", "init")

	tagTime := time.Now().UTC().Format("20060102T150405Z")
	client.Run("tag", "-a", "confluence-sync/pull/TEST/"+tagTime, "-m", "pull")

	os.WriteFile(filepath.Join(spaceDir, "page2.md"), []byte("---\nid: \"2\"\nversion: 1\n---\ntest2\n"), 0644)
	client.Run("add", filepath.Join("TEST", "page2.md"))

	os.WriteFile(filepath.Join(spaceDir, "page1.md"), []byte("---\nid: \"1\"\nversion: 1\n---\nmodified\n"), 0644)

	target := config.Target{Value: "TEST", Mode: config.TargetModeSpace}
	added, modified, deleted, err := collectLocalStatusChanges(target, spaceDir, "TEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(added) != 1 || filepath.Base(added[0]) != "page2.md" {
		t.Errorf("expected 1 added file (page2.md), got %v", added)
	}

	if len(modified) != 1 || filepath.Base(modified[0]) != "page1.md" {
		t.Errorf("expected 1 modified file (page1.md), got %v", modified)
	}

	if len(deleted) != 0 {
		t.Errorf("expected 0 deleted files, got %v", deleted)
	}
}

func TestBuildStatusReport_Success(t *testing.T) {
	tempDir := t.TempDir()

	oldWd, _ := os.Getwd()
	os.Chdir(tempDir)
	defer os.Chdir(oldWd)

	exec.Command("git", "init").Run()
	client, err := git.NewClient()
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	spaceDir := filepath.Join(tempDir, "TEST")
	os.MkdirAll(spaceDir, 0755)

	os.WriteFile(filepath.Join(spaceDir, "page1.md"), []byte("---\nid: \"1\"\nversion: 1\n---\ntest\n"), 0644)

	client.Run("add", ".")
	client.Run("commit", "-m", "init")

	tagTime := time.Now().UTC().Format("20060102T150405Z")
	client.Run("tag", "-a", "confluence-sync/pull/TEST/"+tagTime, "-m", "pull")

	mock := &mockStatusRemote{
		space: confluence.Space{ID: "space-1", Key: "TEST"},
		pages: confluence.PageListResult{
			Pages: []confluence.Page{
				{ID: "1", Title: "Page 1", Version: 1},
				{ID: "2", Title: "Page 2", Version: 3}, // Remote added
			},
			NextCursor: "",
		},
	}

	state := fs.SpaceState{
		SpaceKey: "TEST",
		PagePathIndex: map[string]string{
			"page1.md": "1",
		},
	}

	target := config.Target{Value: "TEST", Mode: config.TargetModeSpace}
	initialCtx := initialPullContext{
		spaceDir: spaceDir,
		spaceKey: "TEST",
	}

	report, err := buildStatusReport(context.Background(), mock, target, initialCtx, state, "TEST", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.RemoteAdded) != 1 {
		t.Errorf("expected 1 remote added, got %v", report.RemoteAdded)
	}
}
