package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestRunDoctor_ReportsStructuralAndSemanticIssues(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)
	chdirRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	runGitForTest(t, repo, "add", "README.md")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	spaceDir := filepath.Join(repo, "TEST")
	if err := os.MkdirAll(filepath.Join(spaceDir, "Parent"), 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	state := fs.NewSpaceState()
	state.SpaceKey = "TEST"
	state.PagePathIndex = map[string]string{
		"page.md":          "1",
		"missing.md":       "2",
		"empty.md":         "",
		"conflict.md":      "4",
		"mismatch.md":      "6",
		"unknown-media.md": "8",
		"embedded-only.md": "9",
		"Parent.md":        "10",
		"Parent/Child.md":  "11",
		"unreadable.md":    "12",
	}
	if err := fs.SaveState(spaceDir, state); err != nil {
		t.Fatalf("write state: %v", err)
	}

	writeDoctorMarkdown(t, filepath.Join(spaceDir, "page.md"), "1", "page")
	writeDoctorMarkdown(t, filepath.Join(spaceDir, "orphan.md"), "3", "orphan")
	writeDoctorMarkdown(t, filepath.Join(spaceDir, "mismatch.md"), "5", "mismatch")
	writeDoctorMarkdown(t, filepath.Join(spaceDir, "unknown-media.md"), "8", "[Embedded content] [Media: UNKNOWN_MEDIA_ID]")
	writeDoctorMarkdown(t, filepath.Join(spaceDir, "embedded-only.md"), "9", "before\n[Embedded content]\nafter")
	writeDoctorMarkdown(t, filepath.Join(spaceDir, "Parent.md"), "10", "parent in wrong place")
	writeDoctorMarkdown(t, filepath.Join(spaceDir, "Parent", "Child.md"), "11", "child")

	conflictContent := "---\nid: 4\nversion: 1\n---\n<<<<<<<\nlocal\n=======\nremote\n>>>>>>>\n"
	if err := os.WriteFile(filepath.Join(spaceDir, "conflict.md"), []byte(conflictContent), 0o600); err != nil {
		t.Fatalf("write conflict: %v", err)
	}

	unreadableFile := filepath.Join(spaceDir, "unreadable.md")
	if err := os.WriteFile(unreadableFile, []byte("---\nid: 12\nversion: 1\n---\n"), 0o200); err != nil {
		t.Fatalf("write unreadable: %v", err)
	}

	out := new(bytes.Buffer)
	cmd := newDoctorCmd()
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))

	target := config.Target{Value: spaceDir, Mode: config.TargetModeSpace}
	if err := runDoctor(cmd, target, false); err != nil {
		t.Fatalf("runDoctor failed: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"[error][repairable] missing-file: missing.md",
		"[error][repairable] empty-index-entry: empty.md",
		"[error][manual] id-mismatch: mismatch.md",
		"[error][manual] conflict-markers: conflict.md",
		"[warning][manual] unknown-media-placeholder: unknown-media.md",
		"[warning][manual] embedded-content-placeholder: embedded-only.md",
		"[warning][manual] hierarchy-layout: Parent.md",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "Run with --repair to automatically fix repairable issues.") {
		t.Fatalf("expected repair hint, got:\n%s", got)
	}
}

func TestRunDoctor_RepairRemovesOnlySafeIssues(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)
	chdirRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	runGitForTest(t, repo, "add", "README.md")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	spaceDir := filepath.Join(repo, "TEST")
	if err := os.MkdirAll(filepath.Join(spaceDir, "Parent"), 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	state := fs.NewSpaceState()
	state.SpaceKey = "TEST"
	state.PagePathIndex = map[string]string{
		"page.md":          "1",
		"missing.md":       "2",
		"unknown-media.md": "8",
		"Parent.md":        "10",
		"Parent/Child.md":  "11",
	}
	if err := fs.SaveState(spaceDir, state); err != nil {
		t.Fatalf("write state: %v", err)
	}

	writeDoctorMarkdown(t, filepath.Join(spaceDir, "page.md"), "1", "page")
	writeDoctorMarkdown(t, filepath.Join(spaceDir, "orphan.md"), "3", "orphan")
	writeDoctorMarkdown(t, filepath.Join(spaceDir, "unknown-media.md"), "8", "[Embedded content] [Media: UNKNOWN_MEDIA_ID]")
	writeDoctorMarkdown(t, filepath.Join(spaceDir, "Parent.md"), "10", "parent in wrong place")
	writeDoctorMarkdown(t, filepath.Join(spaceDir, "Parent", "Child.md"), "11", "child")

	syncBranch := "sync/TEST/20260305T211238Z"
	runGitForTest(t, repo, "branch", syncBranch, "main")

	out := new(bytes.Buffer)
	cmd := newDoctorCmd()
	cmd.SetOut(out)
	cmd.SetErr(new(bytes.Buffer))

	target := config.Target{Value: spaceDir, Mode: config.TargetModeSpace}
	if err := runDoctor(cmd, target, true); err != nil {
		t.Fatalf("runDoctor repair failed: %v", err)
	}

	newState, err := fs.LoadState(spaceDir)
	if err != nil {
		t.Fatalf("load state after repair: %v", err)
	}
	if _, ok := newState.PagePathIndex["missing.md"]; ok {
		t.Fatalf("expected missing.md to be removed from state")
	}
	if newState.PagePathIndex["orphan.md"] != "3" {
		t.Fatalf("expected orphan.md to be added to state, got %q", newState.PagePathIndex["orphan.md"])
	}
	if branchList := strings.TrimSpace(runGitForTest(t, repo, "branch", "--list", syncBranch)); branchList != "" {
		t.Fatalf("expected sync branch to be deleted, got %q", branchList)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, "unknown-media.md")); err != nil {
		t.Fatalf("expected unknown-media.md to remain unchanged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, "Parent.md")); err != nil {
		t.Fatalf("expected Parent.md to remain unchanged: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"repaired [missing-file]: removed stale state entry for missing.md",
		"repaired [untracked-id]: added orphan.md -> 3 to state index",
		"repaired [stale-sync-branch]: deleted stale recovery branch sync/TEST/20260305T211238Z",
		"[unknown-media-placeholder] unknown-media.md",
		"[hierarchy-layout] Parent.md",
		"manual resolution required",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected repair output to contain %q, got:\n%s", want, got)
		}
	}
}

func writeDoctorMarkdown(t *testing.T, path, id, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir markdown dir: %v", err)
	}
	if err := fs.WriteMarkdownDocument(path, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			ID:      id,
			Version: 1,
		},
		Body: body,
	}); err != nil {
		t.Fatalf("write markdown %s: %v", path, err)
	}
}
