package cmd

import (
	"bytes"
	"errors"
	"io"
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
