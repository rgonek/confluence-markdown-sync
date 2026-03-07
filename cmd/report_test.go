package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
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

type commandReportJSON struct {
	RunID string `json:"run_id"`

	Command string `json:"command"`
	Success bool   `json:"success"`
	Error   string `json:"error"`

	Timing struct {
		StartedAt  string `json:"started_at"`
		FinishedAt string `json:"finished_at"`
		DurationMs int64  `json:"duration_ms"`
	} `json:"timing"`

	Target struct {
		Mode     string `json:"mode"`
		Value    string `json:"value"`
		SpaceKey string `json:"space_key"`
		SpaceDir string `json:"space_dir"`
		File     string `json:"file"`
	} `json:"target"`

	Diagnostics []struct {
		Path           string `json:"path"`
		Code           string `json:"code"`
		Field          string `json:"field"`
		Message        string `json:"message"`
		Category       string `json:"category"`
		ActionRequired bool   `json:"action_required"`
	} `json:"diagnostics"`

	MutatedFiles []string `json:"mutated_files"`

	MutatedPages []struct {
		Path    string `json:"path"`
		PageID  string `json:"page_id"`
		Title   string `json:"title"`
		Version int    `json:"version"`
		Deleted bool   `json:"deleted"`
	} `json:"mutated_pages"`

	AttachmentOperations []struct {
		Type         string `json:"type"`
		Path         string `json:"path"`
		PageID       string `json:"page_id"`
		AttachmentID string `json:"attachment_id"`
	} `json:"attachment_operations"`

	FallbackModes []string `json:"fallback_modes"`

	RecoveryArtifacts []struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"recovery_artifacts"`

	ConflictResolution *struct {
		Policy       string   `json:"policy"`
		Status       string   `json:"status"`
		MutatedFiles []string `json:"mutated_files"`
		Diagnostics  []struct {
			Path           string `json:"path"`
			Code           string `json:"code"`
			Field          string `json:"field"`
			Message        string `json:"message"`
			Category       string `json:"category"`
			ActionRequired bool   `json:"action_required"`
		} `json:"diagnostics"`
		AttachmentOperations []struct {
			Type         string `json:"type"`
			Path         string `json:"path"`
			PageID       string `json:"page_id"`
			AttachmentID string `json:"attachment_id"`
		} `json:"attachment_operations"`
		FallbackModes []string `json:"fallback_modes"`
	} `json:"conflict_resolution"`
}

func TestRunPull_ReportJSONSuccessIsStable(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "Root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
			ConfluenceParentPageID: "folder-1",
		},
		Body: "old body\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		SpaceKey:        "ENG",
		PagePathIndex:   map[string]string{"Root.md": "1"},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      2,
				ParentType:   "folder",
				ParentPageID: "folder-1",
				LastModified: time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC),
			},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      2,
				ParentType:   "folder",
				ParentPageID: "folder-1",
				LastModified: time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, simpleADF("new body")),
			},
		},
		folderErr: &confluence.APIError{
			StatusCode: 500,
			Method:     "GET",
			URL:        "/wiki/api/v2/folders/folder-1",
			Message:    "folder api unavailable",
		},
		attachmentsByPage: map[string][]confluence.Attachment{
			"1": {
				{ID: "att-1", PageID: "1", Filename: "diagram.png"},
			},
		},
		attachments: map[string][]byte{
			"att-1": []byte("png"),
		},
	}

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newPullRemote = oldFactory })

	chdirRepo(t, repo)

	cmd := newPullCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "--force", "Engineering (ENG)"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("pull command failed: %v", err)
	}

	report := decodeCommandReportJSON(t, out.Bytes())
	assertReportMetadata(t, report, "pull", true)
	if report.Target.SpaceKey != "ENG" {
		t.Fatalf("target space key = %q, want ENG", report.Target.SpaceKey)
	}
	if !containsString(report.MutatedFiles, "Root.md") {
		t.Fatalf("mutated files = %v, want Root.md", report.MutatedFiles)
	}
	if !containsString(report.FallbackModes, "folder_lookup_unavailable") {
		t.Fatalf("fallback modes = %v, want folder_lookup_unavailable", report.FallbackModes)
	}
	if !containsDiagnosticCode(report, "FOLDER_LOOKUP_UNAVAILABLE") {
		t.Fatalf("diagnostics = %+v, want FOLDER_LOOKUP_UNAVAILABLE", report.Diagnostics)
	}
}

func TestRunPull_ReportJSONFailureIsStructured(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupEnv(t)
	chdirRepo(t, repo)

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) {
		return nil, errors.New("simulated client failure")
	}
	t.Cleanup(func() { newPullRemote = oldFactory })

	cmd := newPullCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "ENG"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected pull command to fail")
	}

	report := decodeCommandReportJSON(t, out.Bytes())
	assertReportMetadata(t, report, "pull", false)
	if !strings.Contains(report.Error, "simulated client failure") {
		t.Fatalf("error = %q, want simulated client failure", report.Error)
	}
}

func TestRunPull_ReportJSONFailureOnWorkspaceSyncStateIsStructured(t *testing.T) {
	runParallelCommandTest(t)

	repo := createUnmergedWorkspaceRepo(t)
	chdirRepo(t, repo)

	cmd := newPullCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "ENG"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected pull command to fail")
	}

	report := decodeCommandReportJSON(t, out.Bytes())
	assertReportMetadata(t, report, "pull", false)
	if !strings.Contains(report.Error, "syncing state with unresolved files") {
		t.Fatalf("error = %q, want syncing-state failure", report.Error)
	}
}

func TestRunValidate_ReportJSONFailureIsStable(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	rootPath := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Version: 1},
		Body:        "content\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "2", Version: 1},
		Body:        "content\n",
	})

	chdirRepo(t, repo)

	cmd := newValidateCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "Engineering (ENG)"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected validate command to fail")
	}

	report := decodeCommandReportJSON(t, out.Bytes())
	assertReportMetadata(t, report, "validate", false)
	if !containsDiagnostic(report, "immutable", "id") {
		t.Fatalf("diagnostics = %+v, want immutable id entry", report.Diagnostics)
	}
	if len(report.MutatedFiles) != 0 {
		t.Fatalf("mutated files = %v, want empty", report.MutatedFiles)
	}
}

func TestRunDiff_ReportJSONSuccessIsStable(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	setupEnv(t)

	localFile := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, localFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "old body\n",
	})

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Root", Version: 2, LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC)},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, diffUnresolvedADF()),
			},
		},
		attachments: map[string][]byte{},
	}

	oldFactory := newDiffRemote
	newDiffRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newDiffRemote = oldFactory })

	chdirRepo(t, repo)

	cmd := newDiffCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", localFile})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("diff command failed: %v", err)
	}

	report := decodeCommandReportJSON(t, out.Bytes())
	assertReportMetadata(t, report, "diff", true)
	if !containsString(report.MutatedFiles, "root.md") {
		t.Fatalf("mutated files = %v, want root.md", report.MutatedFiles)
	}
	if !containsDiagnosticCode(report, "unresolved_reference") {
		t.Fatalf("diagnostics = %+v, want unresolved_reference", report.Diagnostics)
	}
}

func TestRunDiff_ReportJSONIncludesFolderFallbackDiagnosticsAndModes(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}
	setupEnv(t)

	localFile := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, localFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "old body\n",
	})

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Root", ParentPageID: "folder-1", ParentType: "folder", Version: 2, LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC)},
		},
		folderErr: &confluence.APIError{
			StatusCode: 500,
			Method:     "GET",
			URL:        "/wiki/api/v2/folders",
			Message:    "Internal Server Error",
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				ParentPageID: "folder-1",
				ParentType:   "folder",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, simpleADF("new body")),
			},
		},
		attachments: map[string][]byte{},
	}

	oldFactory := newDiffRemote
	newDiffRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newDiffRemote = oldFactory })

	chdirRepo(t, repo)

	cmd := newDiffCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", localFile})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("diff command failed: %v", err)
	}

	report := decodeCommandReportJSON(t, out.Bytes())
	assertReportMetadata(t, report, "diff", true)
	if !containsDiagnosticCode(report, "FOLDER_LOOKUP_UNAVAILABLE") {
		t.Fatalf("diagnostics = %+v, want FOLDER_LOOKUP_UNAVAILABLE", report.Diagnostics)
	}
	if !containsString(report.FallbackModes, "folder_lookup_unavailable") {
		t.Fatalf("fallback modes = %v, want folder_lookup_unavailable", report.FallbackModes)
	}
}

func TestRunPush_ReportJSONSuccessIsStable(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)
	setupEnv(t)

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "![asset](assets/new.png)\n",
	})

	assetPath := filepath.Join(spaceDir, "assets", "new.png")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local change")

	fake := newCmdFakePushRemote(1)
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	chdirRepo(t, spaceDir)

	cmd := newPushCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "--on-conflict=cancel"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("push command failed: %v", err)
	}

	report := decodeCommandReportJSON(t, out.Bytes())
	assertReportMetadata(t, report, "push", true)
	if !containsString(report.MutatedFiles, "root.md") {
		t.Fatalf("mutated files = %v, want root.md", report.MutatedFiles)
	}
	if len(report.MutatedPages) != 1 || report.MutatedPages[0].PageID != "1" || report.MutatedPages[0].Version != 2 {
		t.Fatalf("mutated pages = %+v, want page 1 version 2", report.MutatedPages)
	}
	if len(report.AttachmentOperations) != 1 {
		t.Fatalf("attachment operations = %+v, want one upload", report.AttachmentOperations)
	}
	if got := report.AttachmentOperations[0]; got.Type != "upload" || got.PageID != "1" || got.Path != "assets/1/new.png" {
		t.Fatalf("unexpected attachment operation: %+v", got)
	}
	if !containsRecoveryArtifact(report, "snapshot_ref", "cleaned_up") {
		t.Fatalf("recovery artifacts = %+v, want cleaned-up snapshot ref", report.RecoveryArtifacts)
	}
	if !containsRecoveryArtifact(report, "sync_branch", "cleaned_up") {
		t.Fatalf("recovery artifacts = %+v, want cleaned-up sync branch", report.RecoveryArtifacts)
	}
}

func TestRunPush_ReportJSONFailureOnWorkspaceSyncStateIsStructured(t *testing.T) {
	runParallelCommandTest(t)

	repo := createUnmergedWorkspaceRepo(t)
	chdirRepo(t, repo)

	cmd := newPushCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "--on-conflict=cancel"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected push command to fail")
	}

	report := decodeCommandReportJSON(t, out.Bytes())
	assertReportMetadata(t, report, "push", false)
	if !strings.Contains(report.Error, "syncing state with unresolved files") {
		t.Fatalf("error = %q, want syncing-state failure", report.Error)
	}
}

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

func TestRunPush_ReportJSONPullMergeEmitsSingleObjectAndCapturesPullMergeReport(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)
	setupEnv(t)

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Updated local content\n",
	})
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local change")

	fake := newCmdFakePushRemote(3)
	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return fake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	chdirRepo(t, spaceDir)

	cmd := newPushCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "--on-conflict=pull-merge"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("push command failed: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	report := decodeCommandReportJSON(t, stdout.Bytes())
	assertReportMetadata(t, report, "push", true)
	if report.ConflictResolution == nil {
		t.Fatalf("conflict resolution = nil, want pull-merge details; report=%+v", report)
	}
	if report.ConflictResolution.Policy != OnConflictPullMerge {
		t.Fatalf("conflict resolution policy = %q, want %q", report.ConflictResolution.Policy, OnConflictPullMerge)
	}
	if report.ConflictResolution.Status != "completed" {
		t.Fatalf("conflict resolution status = %q, want completed", report.ConflictResolution.Status)
	}
	if !containsString(report.ConflictResolution.MutatedFiles, "root.md") {
		t.Fatalf("conflict resolution mutated files = %v, want root.md", report.ConflictResolution.MutatedFiles)
	}
	if !containsString(report.MutatedFiles, "root.md") {
		t.Fatalf("outer mutated files = %v, want root.md from pull-merge", report.MutatedFiles)
	}
}

func TestRunPush_ReportJSONFailureAroundWorktreeSetupIncludesRecoveryArtifacts(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)
	setupEnv(t)

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Updated local content\n",
	})
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local change")

	oldNow := nowUTC
	fixedNow := time.Date(2026, time.February, 1, 12, 34, 58, 0, time.UTC)
	nowUTC = func() time.Time { return fixedNow }
	t.Cleanup(func() { nowUTC = oldNow })

	worktreeDir := filepath.Join(repo, ".confluence-worktrees", "ENG-"+fixedNow.Format("20060102T150405Z"))
	if err := os.MkdirAll(worktreeDir, 0o750); err != nil {
		t.Fatalf("mkdir blocking worktree dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreeDir, "keep.txt"), []byte("block worktree"), 0o600); err != nil {
		t.Fatalf("write blocking worktree file: %v", err)
	}

	chdirRepo(t, spaceDir)

	cmd := newPushCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--report-json", "--on-conflict=cancel"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected push command to fail")
	}

	report := decodeCommandReportJSON(t, out.Bytes())
	assertReportMetadata(t, report, "push", false)
	if !containsRecoveryArtifact(report, "snapshot_ref", "retained") {
		t.Fatalf("recovery artifacts = %+v, want retained snapshot ref", report.RecoveryArtifacts)
	}
	if !containsRecoveryArtifact(report, "sync_branch", "retained") {
		t.Fatalf("recovery artifacts = %+v, want retained sync branch", report.RecoveryArtifacts)
	}
}

func TestReportWriter_JSONModeRoutesPromptsToStderr(t *testing.T) {
	runParallelCommandTest(t)

	cmd := &cobra.Command{}
	addReportJSONFlag(cmd)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if err := cmd.Flags().Set(reportJSONFlagName, "true"); err != nil {
		t.Fatalf("set report-json flag: %v", err)
	}

	actualOut := ensureSynchronizedCmdOutput(cmd)
	policy, err := resolvePushConflictPolicy(strings.NewReader("cancel\n"), reportWriter(cmd, actualOut), "", false)
	if err != nil {
		t.Fatalf("resolve conflict policy: %v", err)
	}
	if policy != OnConflictCancel {
		t.Fatalf("policy = %q, want %q", policy, OnConflictCancel)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Conflict policy for remote-ahead pages") {
		t.Fatalf("stderr = %q, want visible prompt", stderr.String())
	}
}

func decodeCommandReportJSON(t *testing.T, raw []byte) commandReportJSON {
	t.Helper()

	var report commandReportJSON
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("output is not valid JSON report: %v\n%s", err, string(raw))
	}
	return report
}

func assertReportMetadata(t *testing.T, report commandReportJSON, command string, success bool) {
	t.Helper()

	if report.Command != command {
		t.Fatalf("command = %q, want %q", report.Command, command)
	}
	if report.Success != success {
		t.Fatalf("success = %v, want %v", report.Success, success)
	}
	if strings.TrimSpace(report.RunID) == "" {
		t.Fatal("run_id should not be empty")
	}
	if strings.TrimSpace(report.Timing.StartedAt) == "" {
		t.Fatal("timing.started_at should not be empty")
	}
	if strings.TrimSpace(report.Timing.FinishedAt) == "" {
		t.Fatal("timing.finished_at should not be empty")
	}
	if report.Timing.DurationMs < 0 {
		t.Fatalf("timing.duration_ms = %d, want >= 0", report.Timing.DurationMs)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsDiagnostic(report commandReportJSON, code, field string) bool {
	for _, diag := range report.Diagnostics {
		if diag.Code == code && diag.Field == field {
			return true
		}
	}
	return false
}

func containsDiagnosticCode(report commandReportJSON, code string) bool {
	for _, diag := range report.Diagnostics {
		if diag.Code == code {
			return true
		}
	}
	return false
}

func containsRecoveryArtifact(report commandReportJSON, artifactType, status string) bool {
	for _, artifact := range report.RecoveryArtifacts {
		if artifact.Type == artifactType && artifact.Status == status {
			return true
		}
	}
	return false
}

func createUnmergedWorkspaceRepo(t *testing.T) string {
	t.Helper()

	repo := t.TempDir()
	setupGitRepo(t, repo)

	path := filepath.Join(repo, "conflict.md")
	if err := os.WriteFile(path, []byte("base\n"), 0o600); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "base")

	runGitForTest(t, repo, "checkout", "-b", "topic")
	if err := os.WriteFile(path, []byte("topic\n"), 0o600); err != nil {
		t.Fatalf("write topic file: %v", err)
	}
	runGitForTest(t, repo, "commit", "-am", "topic change")

	runGitForTest(t, repo, "checkout", "main")
	if err := os.WriteFile(path, []byte("main\n"), 0o600); err != nil {
		t.Fatalf("write main file: %v", err)
	}
	runGitForTest(t, repo, "commit", "-am", "main change")

	cmd := exec.Command("git", "merge", "topic") //nolint:gosec // test helper intentionally creates merge conflict
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected git merge topic to fail, output:\n%s", string(out))
	}

	return repo
}
