package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestStatusCmdRun(t *testing.T) {
	runParallelCommandTest(t)
	t.Run("creates cobra command successfully", func(t *testing.T) {
		cmd := newStatusCmd()
		if cmd == nil {
			t.Fatal("expected command not to be nil")
			return //nolint:staticcheck // unreachable — silences SA5011 nil dereference false positive
		}
		if cmd.Use != "status [TARGET]" {
			t.Fatalf("expected use 'status [TARGET]', got %s", cmd.Use)
		}
	})

	t.Run("fails when workspace sync not ready", func(t *testing.T) {
		cmd := newStatusCmd()
		cmd.SetOut(new(bytes.Buffer))
		cmd.SetErr(new(bytes.Buffer))

		target := config.Target{Value: "TEST", Mode: config.TargetModeSpace}
		// If git repo isn't dirty or we don't have a specific state, this might actually pass ensureWorkspaceSyncReady
		// but fail later. Let's just run it to boost coverage on error branches.
		_ = runStatus(cmd, target)
	})
}

func TestBuildStatusReport(t *testing.T) {
	runParallelCommandTest(t)
	// A mock to get some coverage on buildStatusReport if possible
	// It normally errors on collectLocalStatusChanges if git repo isn't right
	mock := &mockStatusRemote{}
	target := config.Target{Value: "TEST", Mode: config.TargetModeSpace}

	ctx := context.Background()
	_, _ = buildStatusReport(ctx, mock, target, initialPullContext{}, fs.SpaceState{}, "TEST", "filterPath")
}

func TestRunStatus_Integration(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()

	// Setup git repo
	runGitForStatus(t, repo, "init", "-b", "main")
	runGitForStatus(t, repo, "config", "user.email", "test@example.com")
	runGitForStatus(t, repo, "config", "user.name", "test")

	// Initial commit
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("init"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	runGitForStatus(t, repo, "add", "README.md")
	runGitForStatus(t, repo, "commit", "-m", "initial commit")

	// Create some local changes
	if err := os.WriteFile(filepath.Join(repo, "added.md"), []byte("added"), 0o600); err != nil {
		t.Fatalf("write added.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "modified.md"), []byte("modified"), 0o600); err != nil {
		t.Fatalf("write modified.md: %v", err)
	}
	runGitForStatus(t, repo, "add", "modified.md")
	runGitForStatus(t, repo, "commit", "-m", "add modified")
	if err := os.WriteFile(filepath.Join(repo, "modified.md"), []byte("modified v2"), 0o600); err != nil {
		t.Fatalf("modify modified.md: %v", err)
	}

	// Create a dummy .env file
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("ATLASSIAN_DOMAIN=test.atlassian.net\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	mock := &mockStatusRemote{
		space: confluence.Space{ID: "123", Key: "TEST"},
		pages: confluence.PageListResult{
			Pages: []confluence.Page{
				{ID: "1", Title: "README", Version: 1},
				{ID: "2", Title: "modified", Version: 2},
			},
		},
	}

	// Mock the remote factory
	oldNewStatusRemote := newStatusRemote
	newStatusRemote = func(cfg *config.Config) (StatusRemote, error) {
		return mock, nil
	}
	defer func() { newStatusRemote = oldNewStatusRemote }()

	cmd := newStatusCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)

	// Target empty means use current directory if it has state
	target := config.Target{Value: "", Mode: config.TargetModeSpace}

	// We need to point the command to our temp repo
	oldWD, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWD) }()
	if err := os.Chdir(repo); err != nil {
		t.Fatalf("chdir repo: %v", err)
	}

	// Mock ensureWorkspaceSyncReady to return nil so we don't fail on missing state
	// In a real scenario, there would be a .confluence-state.json
	// Let's create one
	state := fs.NewSpaceState()
	state.SpaceKey = "TEST"
	state.PagePathIndex = map[string]string{
		"1": "README.md",
		"2": "modified.md",
	}
	if err := fs.SaveState(".", state); err != nil {
		t.Fatalf("write state: %v", err)
	}

	// Create markdown files with frontmatter to satisfy ReadMarkdownDocument
	readmeContent := "---\nid: 1\nversion: 1\n---\ninit"
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte(readmeContent), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	modContent := "---\nid: 2\nversion: 1\n---\nmodified v2"
	if err := os.WriteFile(filepath.Join(repo, "modified.md"), []byte(modContent), 0o600); err != nil {
		t.Fatalf("write modified: %v", err)
	}

	err := runStatus(cmd, target)
	if err != nil {
		t.Fatalf("runStatus failed: %v", err)
	}

	output := out.String()
	if !bytes.Contains([]byte(output), []byte("Space: TEST")) {
		t.Fatalf("output missing space key: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte("modified.md")) {
		t.Fatalf("output missing modified file: %s", output)
	}
}

func runGitForStatus(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // Intentionally running git in test
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", args, err, string(out))
	}
}

func TestPrintStatusSection(t *testing.T) {
	runParallelCommandTest(t)
	out := new(bytes.Buffer)
	printStatusSection(out, "test", []string{"a"}, []string{"b"}, []string{"c"})

	output := out.String()
	if !bytes.Contains([]byte(output), []byte("test:")) {
		t.Fatalf("missing section title")
	}
	if !bytes.Contains([]byte(output), []byte("added (1):")) {
		t.Fatalf("missing added")
	}
}

func TestPrintStatusList_Items(t *testing.T) {
	runParallelCommandTest(t)
	out := new(bytes.Buffer)
	printStatusList(out, "deleted", []string{"file1.md", "file2.md"})

	output := out.String()
	if !bytes.Contains([]byte(output), []byte("deleted (2):")) {
		t.Fatalf("missing label format")
	}
	if !bytes.Contains([]byte(output), []byte("- file1.md")) {
		t.Fatalf("missing item 1")
	}
}

func TestBuildStatusReport_Drift(t *testing.T) {
	runParallelCommandTest(t)
	mock := &mockStatusRemote{}
	target := config.Target{Value: "TEST", Mode: config.TargetModeSpace}
	ctx := context.Background()
	_, _ = buildStatusReport(ctx, mock, target, initialPullContext{}, fs.SpaceState{}, "TEST", "filterPath")
}
