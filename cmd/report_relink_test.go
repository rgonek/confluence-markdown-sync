package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

func TestRunPull_ReportJSONWithRelinkKeepsStdoutJSONAndCapturesRelinkedFiles(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	targetDir := filepath.Join(repo, "Target (TGT)")
	sourceDir := filepath.Join(repo, "Source (SRC)")
	if err := os.MkdirAll(targetDir, 0o750); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.MkdirAll(sourceDir, 0o750); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(targetDir, "target.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Target",
			ID:                     "42",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "old target body\n",
	})
	if err := fs.SaveState(targetDir, fs.SpaceState{
		SpaceKey:        "TGT",
		PagePathIndex:   map[string]string{"target.md": "42"},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save target state: %v", err)
	}

	sourceDocPath := filepath.Join(sourceDir, "doc.md")
	writeMarkdown(t, sourceDocPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Source", ID: "101", Version: 1},
		Body:        "[Target](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=42)\n",
	})
	if err := fs.SaveState(sourceDir, fs.SpaceState{
		SpaceKey:        "SRC",
		PagePathIndex:   map[string]string{"doc.md": "101"},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save source state: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-2", Key: "TGT", Name: "Target"},
		pages: []confluence.Page{
			{
				ID:           "42",
				SpaceID:      "space-2",
				Title:        "Target",
				Version:      2,
				LastModified: time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC),
			},
		},
		pagesByID: map[string]confluence.Page{
			"42": {
				ID:           "42",
				SpaceID:      "space-2",
				Title:        "Target",
				Version:      2,
				LastModified: time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, simpleADF("new target body")),
			},
		},
		attachments: map[string][]byte{},
	}

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	chdirRepo(t, repo)

	cmd := newPullCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "--relink", "--force", "Target (TGT)"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("pull command failed: %v", err)
	}

	report := decodeCommandReportJSON(t, stdout.Bytes())
	assertReportMetadata(t, report, "pull", true)
	if !containsString(report.MutatedFiles, "target.md") {
		t.Fatalf("mutated files = %v, want target.md", report.MutatedFiles)
	}
	if !containsString(report.MutatedFiles, "../Source (SRC)/doc.md") {
		t.Fatalf("mutated files = %v, want relinked source doc", report.MutatedFiles)
	}

	raw, err := os.ReadFile(sourceDocPath) //nolint:gosec // test path is controlled in temp repo
	if err != nil {
		t.Fatalf("read source doc: %v", err)
	}
	if !strings.Contains(string(raw), "../Target%20%28TGT%29/target.md") {
		t.Fatalf("expected source doc to be relinked, got:\n%s", string(raw))
	}
}

func TestRunPull_ReportJSONWithRelinkPreservesAppliedFilesOnLaterError(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	targetDir := filepath.Join(repo, "Target (TGT)")
	sourceDir := filepath.Join(repo, "Source (SRC)")
	if err := os.MkdirAll(targetDir, 0o750); err != nil {
		t.Fatalf("mkdir target dir: %v", err)
	}
	if err := os.MkdirAll(sourceDir, 0o750); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(targetDir, "target.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Target",
			ID:                     "42",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "old target body\n",
	})
	if err := fs.SaveState(targetDir, fs.SpaceState{
		SpaceKey:        "TGT",
		PagePathIndex:   map[string]string{"target.md": "42"},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save target state: %v", err)
	}

	appliedPath := filepath.Join(sourceDir, "a.md")
	readOnlyPath := filepath.Join(sourceDir, "b.md")
	writeMarkdown(t, appliedPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Applied", ID: "101", Version: 1},
		Body:        "[Target](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=42)\n",
	})
	writeMarkdown(t, readOnlyPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Blocked", ID: "102", Version: 1},
		Body:        "[Target](https://example.atlassian.net/wiki/pages/viewpage.action?pageId=42)\n",
	})
	if err := os.Chmod(readOnlyPath, 0o400); err != nil {
		t.Fatalf("chmod read-only relink file: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyPath, 0o600) })

	if err := fs.SaveState(sourceDir, fs.SpaceState{
		SpaceKey:        "SRC",
		PagePathIndex:   map[string]string{"a.md": "101", "b.md": "102"},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save source state: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-2", Key: "TGT", Name: "Target"},
		pages: []confluence.Page{
			{
				ID:           "42",
				SpaceID:      "space-2",
				Title:        "Target",
				Version:      2,
				LastModified: time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC),
			},
		},
		pagesByID: map[string]confluence.Page{
			"42": {
				ID:           "42",
				SpaceID:      "space-2",
				Title:        "Target",
				Version:      2,
				LastModified: time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, simpleADF("new target body")),
			},
		},
		attachments: map[string][]byte{},
	}

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	chdirRepo(t, repo)

	cmd := newPullCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "--relink", "--force", "Target (TGT)"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected pull command to fail")
	}

	report := decodeCommandReportJSON(t, stdout.Bytes())
	assertReportMetadata(t, report, "pull", false)
	if !strings.Contains(report.Error, "auto-relink") {
		t.Fatalf("error = %q, want auto-relink failure", report.Error)
	}
	if !containsString(report.MutatedFiles, "target.md") {
		t.Fatalf("mutated files = %v, want target.md", report.MutatedFiles)
	}
	if !containsString(report.MutatedFiles, "../Source (SRC)/a.md") {
		t.Fatalf("mutated files = %v, want applied relink file", report.MutatedFiles)
	}

	appliedRaw, err := os.ReadFile(appliedPath) //nolint:gosec // test path is controlled in temp repo
	if err != nil {
		t.Fatalf("read applied relink file: %v", err)
	}
	if !strings.Contains(string(appliedRaw), "../Target%20%28TGT%29/target.md") {
		t.Fatalf("expected applied relink file to be rewritten, got:\n%s", string(appliedRaw))
	}
}
