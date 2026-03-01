package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

func TestApplyAndDropStash_KeepBothCreatesSideBySideConflictCopy(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	repoPath := filepath.ToSlash(filepath.Join("Engineering (ENG)", "Page.md"))
	mainFile := filepath.Join(spaceDir, "Page.md")
	if err := os.WriteFile(mainFile, []byte("base\n"), 0o600); err != nil {
		t.Fatalf("write base file: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	if err := os.WriteFile(mainFile, []byte("local edit\n"), 0o600); err != nil {
		t.Fatalf("write local edit: %v", err)
	}
	runGitForTest(t, repo, "stash", "push", "--include-untracked", "-m", "local", "--", repoPath)
	stashRef := strings.TrimSpace(runGitForTest(t, repo, "stash", "list", "-1", "--format=%gd"))
	if stashRef == "" {
		t.Fatal("expected stash ref")
	}

	if err := os.WriteFile(mainFile, []byte("website edit\n"), 0o600); err != nil {
		t.Fatalf("write website edit: %v", err)
	}
	runGitForTest(t, repo, "add", repoPath)
	runGitForTest(t, repo, "commit", "-m", "website update")

	setAutomationFlags(t, false, false)
	out := &bytes.Buffer{}
	if err := applyAndDropStash(repo, stashRef, filepath.ToSlash(filepath.Base(spaceDir)), strings.NewReader("c\n"), out); err != nil {
		t.Fatalf("applyAndDropStash() error: %v", err)
	}

	mainRaw, err := os.ReadFile(mainFile) //nolint:gosec // test path is created under t.TempDir
	if err != nil {
		t.Fatalf("read main file: %v", err)
	}
	if strings.Contains(string(mainRaw), "<<<<<<<") {
		t.Fatalf("expected no conflict markers in main file, got:\n%s", string(mainRaw))
	}
	if !strings.Contains(string(mainRaw), "website edit") {
		t.Fatalf("expected main file to keep website version, got:\n%s", string(mainRaw))
	}

	backupPath := filepath.Join(spaceDir, "Page (My Local Changes).md")
	backupRaw, err := os.ReadFile(backupPath) //nolint:gosec // test path is created under t.TempDir
	if err != nil {
		t.Fatalf("read backup file: %v", err)
	}
	if !strings.Contains(string(backupRaw), "local edit") {
		t.Fatalf("expected backup file to preserve local edits, got:\n%s", string(backupRaw))
	}

	if unmerged := strings.TrimSpace(runGitForTest(t, repo, "diff", "--name-only", "--diff-filter=U")); unmerged != "" {
		t.Fatalf("expected no unmerged paths after keep-both flow, got %q", unmerged)
	}
	if stashList := strings.TrimSpace(runGitForTest(t, repo, "stash", "list")); stashList != "" {
		t.Fatalf("expected stash to be dropped, got %q", stashList)
	}
}

func TestRunPull_DiscardLocalFailureRestoresLocalChanges(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "ENG",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T08:00:00Z",
		},
		Body: "old body\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		SpaceKey:              "ENG",
		LastPullHighWatermark: "2026-02-01T00:00:00Z",
		PagePathIndex: map[string]string{
			"root.md": "1",
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "initial")

	localUntracked := filepath.Join(spaceDir, "local-notes.md")
	if err := os.WriteFile(localUntracked, []byte("keep me\n"), 0o600); err != nil {
		t.Fatalf("write local notes: %v", err)
	}

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{{
			ID:           "1",
			SpaceID:      "space-1",
			Title:        "Root",
			Version:      2,
			LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
		}},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
			},
		},
		getPageErr:  errors.New("simulated page fetch failure"),
		attachments: map[string][]byte{},
	}

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	previousForce := flagPullForce
	flagPullForce = true
	t.Cleanup(func() { flagPullForce = previousForce })

	previousDiscard := flagPullDiscardLocal
	flagPullDiscardLocal = true
	t.Cleanup(func() { flagPullDiscardLocal = previousDiscard })

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if err == nil {
		t.Fatal("runPull() expected error")
	}

	raw, readErr := os.ReadFile(localUntracked) //nolint:gosec // test file path is controlled temp workspace
	if readErr != nil {
		t.Fatalf("expected local notes to be restored on failure: %v", readErr)
	}
	if strings.TrimSpace(string(raw)) != "keep me" {
		t.Fatalf("local notes content = %q, want keep me", string(raw))
	}

	stashList := strings.TrimSpace(runGitForTest(t, repo, "stash", "list"))
	if stashList != "" {
		t.Fatalf("stash should be empty after restoration, got %q", stashList)
	}
}

func TestFixPulledVersionsAfterStashRestore(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir spaceDir: %v", err)
	}

	// Write a file with version 3 (simulating what pull committed to HEAD)
	pullContent := "---\nid: \"42\"\nversion: 3\n---\n\nPulled content\n"
	pagePath := filepath.Join(spaceDir, "page.md")
	if err := os.WriteFile(pagePath, []byte(pullContent), 0o600); err != nil {
		t.Fatalf("write pull content: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "pull commit with version 3")

	// Now simulate stash restore reintroducing version 1 on disk
	oldContent := "---\nid: \"42\"\nversion: 1\n---\n\nLocal edits\n"
	if err := os.WriteFile(pagePath, []byte(oldContent), 0o600); err != nil {
		t.Fatalf("write old content: %v", err)
	}

	// Verify the disk has version 1 before fix
	doc, err := fs.ReadMarkdownDocument(pagePath)
	if err != nil {
		t.Fatalf("read doc: %v", err)
	}
	if doc.Frontmatter.Version != 1 {
		t.Fatalf("expected version 1 on disk before fix, got %d", doc.Frontmatter.Version)
	}

	out := new(bytes.Buffer)
	fixPulledVersionsAfterStashRestore(repo, spaceDir, []string{"page.md"}, out)

	// Verify the disk now has version 3
	docAfter, err := fs.ReadMarkdownDocument(pagePath)
	if err != nil {
		t.Fatalf("read doc after fix: %v", err)
	}
	if docAfter.Frontmatter.Version != 3 {
		t.Fatalf("expected version 3 after fix, got %d", docAfter.Frontmatter.Version)
	}

	if !strings.Contains(out.String(), "Auto-updated version field") {
		t.Fatalf("expected auto-update message, got: %s", out.String())
	}
}

func TestFixPulledVersionsAfterStashRestore_NoOp(t *testing.T) {
	runParallelCommandTest(t)

	// When the disk version already matches the committed version, no fix needed
	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir spaceDir: %v", err)
	}

	content := "---\nid: \"42\"\nversion: 5\n---\n\nContent\n"
	pagePath := filepath.Join(spaceDir, "page.md")
	if err := os.WriteFile(pagePath, []byte(content), 0o600); err != nil {
		t.Fatalf("write content: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "commit version 5")

	out := new(bytes.Buffer)
	fixPulledVersionsAfterStashRestore(repo, spaceDir, []string{"page.md"}, out)

	// Should not print update message — nothing changed
	if strings.Contains(out.String(), "Auto-updated") {
		t.Fatalf("expected no update message for already-matching version, got: %s", out.String())
	}
}
