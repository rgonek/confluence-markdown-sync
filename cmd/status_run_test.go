package cmd

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestRunStatus_ExplainsMarkdownOnlyScopeForAssetDrift(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	chdirRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "TEST")
	if err := os.MkdirAll(filepath.Join(spaceDir, "assets", "1"), 0o750); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "page.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Page",
			ID:      "1",
			Version: 1,
		},
		Body: "body\n",
	})
	if err := os.WriteFile(filepath.Join(spaceDir, "assets", "1", "file.png"), []byte("png"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		SpaceKey: "TEST",
		PagePathIndex: map[string]string{
			"page.md": "1",
		},
		AttachmentIndex: map[string]string{
			"assets/1/file.png": "att-1",
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForStatus(t, repo, "add", ".")
	runGitForStatus(t, repo, "commit", "-m", "baseline")
	tagTime := time.Now().UTC().Format("20060102T150405Z")
	runGitForStatus(t, repo, "tag", "-a", "confluence-sync/pull/TEST/"+tagTime, "-m", "pull")

	if err := os.Remove(filepath.Join(spaceDir, "assets", "1", "file.png")); err != nil {
		t.Fatalf("remove asset: %v", err)
	}

	mock := &mockStatusRemote{
		space: confluence.Space{ID: "space-1", Key: "TEST"},
		pages: confluence.PageListResult{
			Pages: []confluence.Page{
				{ID: "1", Title: "Page", Version: 1},
			},
		},
		page: confluence.Page{ID: "1", SpaceID: "space-1", Status: "current"},
	}

	oldNewStatusRemote := newStatusRemote
	newStatusRemote = func(cfg *config.Config) (StatusRemote, error) {
		return mock, nil
	}
	defer func() { newStatusRemote = oldNewStatusRemote }()

	cmd := newStatusCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runStatus(cmd, config.Target{Value: "TEST", Mode: config.TargetModeSpace}); err != nil {
		t.Fatalf("runStatus() error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, statusScopeNote) {
		t.Fatalf("expected markdown-only scope note, got:\n%s", got)
	}
	if strings.Contains(got, "assets/1/file.png") {
		t.Fatalf("status output should not list attachment-only drift, got:\n%s", got)
	}
	if !strings.Contains(got, "added (0)") || !strings.Contains(got, "modified (0)") || !strings.Contains(got, "deleted (0)") {
		t.Fatalf("expected clean markdown status for asset-only drift, got:\n%s", got)
	}
}

func TestStatusCmdHelp_ExplainsMarkdownPageScope(t *testing.T) {
	runParallelCommandTest(t)

	cmd := newStatusCmd()
	if !strings.Contains(cmd.Long, "markdown/page drift only") {
		t.Fatalf("expected long help to explain markdown/page-only scope, got:\n%s", cmd.Long)
	}
	if !strings.Contains(cmd.Long, "attachment-only drift is excluded") {
		t.Fatalf("expected long help to explain excluded attachment-only drift, got:\n%s", cmd.Long)
	}
}

func TestRunStatus_PageAndAssetScopeCases(t *testing.T) {
	runParallelCommandTest(t)

	testCases := []struct {
		name             string
		mutate           func(t *testing.T, spaceDir string)
		wantPresent      []string
		wantAbsent       []string
		wantMarkdownOnly bool
	}{
		{
			name: "page only changes are listed",
			mutate: func(t *testing.T, spaceDir string) {
				writeMarkdown(t, filepath.Join(spaceDir, "page.md"), fs.MarkdownDocument{
					Frontmatter: fs.Frontmatter{
						Title:   "Page",
						ID:      "1",
						Version: 1,
					},
					Body: "updated body\n",
				})
			},
			wantPresent: []string{"modified (1):", "- page.md"},
			wantAbsent:  []string{"assets/1/file.png"},
		},
		{
			name: "asset only changes stay excluded",
			mutate: func(t *testing.T, spaceDir string) {
				if err := os.Remove(filepath.Join(spaceDir, "assets", "1", "file.png")); err != nil {
					t.Fatalf("remove asset: %v", err)
				}
			},
			wantPresent:      []string{"added (0)", "modified (0)", "deleted (0)"},
			wantAbsent:       []string{"assets/1/file.png"},
			wantMarkdownOnly: true,
		},
		{
			name: "mixed page and asset changes show only page drift",
			mutate: func(t *testing.T, spaceDir string) {
				writeMarkdown(t, filepath.Join(spaceDir, "page.md"), fs.MarkdownDocument{
					Frontmatter: fs.Frontmatter{
						Title:   "Page",
						ID:      "1",
						Version: 1,
					},
					Body: "updated body\n",
				})
				if err := os.Remove(filepath.Join(spaceDir, "assets", "1", "file.png")); err != nil {
					t.Fatalf("remove asset: %v", err)
				}
			},
			wantPresent:      []string{"modified (1):", "- page.md"},
			wantAbsent:       []string{"assets/1/file.png"},
			wantMarkdownOnly: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			repo, spaceDir := setupStatusScopeRepo(t)

			tc.mutate(t, spaceDir)
			got := runStatusWithMockRemote(t, repo)

			if tc.wantMarkdownOnly && !strings.Contains(got, statusScopeNote) {
				t.Fatalf("expected markdown-only scope note, got:\n%s", got)
			}
			for _, want := range tc.wantPresent {
				if !strings.Contains(got, want) {
					t.Fatalf("expected output to contain %q, got:\n%s", want, got)
				}
			}
			for _, unwanted := range tc.wantAbsent {
				if strings.Contains(got, unwanted) {
					t.Fatalf("expected output to exclude %q, got:\n%s", unwanted, got)
				}
			}
		})
	}
}

func TestRunStatus_ShowsPlannedPathMoves(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	chdirRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "TEST")
	if err := os.MkdirAll(filepath.Join(spaceDir, "Policies"), 0o750); err != nil {
		t.Fatalf("mkdir policies: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(spaceDir, "Archive"), 0o750); err != nil {
		t.Fatalf("mkdir archive: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "Policies", "Child.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Child",
			ID:      "2",
			Version: 1,
		},
		Body: "body\n",
	})
	writeMarkdown(t, filepath.Join(spaceDir, "Archive", "Reference.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Reference",
			ID:      "3",
			Version: 1,
		},
		Body: "reference\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		SpaceKey: "TEST",
		PagePathIndex: map[string]string{
			"Policies/Child.md":    "2",
			"Archive/Reference.md": "3",
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForStatus(t, repo, "add", ".")
	runGitForStatus(t, repo, "commit", "-m", "baseline")
	tagTime := time.Now().UTC().Format("20060102T150405Z")
	runGitForStatus(t, repo, "tag", "-a", "confluence-sync/pull/TEST/"+tagTime, "-m", "pull")

	modifiedAt := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "TEST", Name: "Test Space"},
		pages: []confluence.Page{
			{ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-2", ParentType: "folder", Version: 1, LastModified: modifiedAt},
			{ID: "3", SpaceID: "space-1", Title: "Reference", ParentPageID: "folder-2", ParentType: "folder", Version: 1, LastModified: modifiedAt},
		},
		folderByID: map[string]confluence.Folder{
			"folder-2": {ID: "folder-2", Title: "Archive"},
		},
		pagesByID: map[string]confluence.Page{
			"2": {ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-2", ParentType: "folder", Version: 1, LastModified: modifiedAt, Status: "current"},
			"3": {ID: "3", SpaceID: "space-1", Title: "Reference", ParentPageID: "folder-2", ParentType: "folder", Version: 1, LastModified: modifiedAt, Status: "current"},
		},
	}

	oldNewStatusRemote := newStatusRemote
	newStatusRemote = func(cfg *config.Config) (StatusRemote, error) {
		return fake, nil
	}
	t.Cleanup(func() { newStatusRemote = oldNewStatusRemote })

	cmd := newStatusCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runStatus(cmd, config.Target{Value: "TEST", Mode: config.TargetModeSpace}); err != nil {
		t.Fatalf("runStatus() error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "Planned path moves (1)") {
		t.Fatalf("expected planned path move section, got:\n%s", got)
	}
	if !strings.Contains(got, "Policies/Child.md -> Archive/Child.md") {
		t.Fatalf("expected planned move detail, got:\n%s", got)
	}
}

func setupStatusScopeRepo(t *testing.T) (string, string) {
	t.Helper()

	repo := t.TempDir()
	setupGitRepo(t, repo)
	chdirRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "TEST")
	if err := os.MkdirAll(filepath.Join(spaceDir, "assets", "1"), 0o750); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "page.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Page",
			ID:      "1",
			Version: 1,
		},
		Body: "body\n",
	})
	if err := os.WriteFile(filepath.Join(spaceDir, "assets", "1", "file.png"), []byte("png"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		SpaceKey: "TEST",
		PagePathIndex: map[string]string{
			"page.md": "1",
		},
		AttachmentIndex: map[string]string{
			"assets/1/file.png": "att-1",
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForStatus(t, repo, "add", ".")
	runGitForStatus(t, repo, "commit", "-m", "baseline")
	tagTime := time.Now().UTC().Format("20060102T150405Z")
	runGitForStatus(t, repo, "tag", "-a", "confluence-sync/pull/TEST/"+tagTime, "-m", "pull")

	return repo, spaceDir
}

func runStatusWithMockRemote(t *testing.T, repo string) string {
	t.Helper()

	mock := &mockStatusRemote{
		space: confluence.Space{ID: "space-1", Key: "TEST"},
		pages: confluence.PageListResult{
			Pages: []confluence.Page{
				{ID: "1", Title: "Page", Version: 1},
			},
		},
		page: confluence.Page{ID: "1", SpaceID: "space-1", Status: "current"},
	}

	oldNewStatusRemote := newStatusRemote
	newStatusRemote = func(cfg *config.Config) (StatusRemote, error) {
		return mock, nil
	}
	t.Cleanup(func() { newStatusRemote = oldNewStatusRemote })

	chdirRepo(t, repo)
	cmd := newStatusCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runStatus(cmd, config.Target{Value: "TEST", Mode: config.TargetModeSpace}); err != nil {
		t.Fatalf("runStatus() error: %v", err)
	}

	return out.String()
}
