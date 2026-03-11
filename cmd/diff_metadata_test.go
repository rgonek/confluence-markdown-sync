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
	"github.com/spf13/cobra"
)

func TestNormalizeDiffMarkdown_StripsReadOnlyMetadata(t *testing.T) {
	t.Parallel()
	doc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "My Page",
			ID:    "42",

			Version:   3,
			CreatedBy: "alice@example.com",
			CreatedAt: "2026-01-01T00:00:00Z",
			UpdatedBy: "bob@example.com",
			UpdatedAt: "2026-02-01T12:00:00Z",
		},
		Body: "some content\n",
	}
	raw, err := fs.FormatMarkdownDocument(doc)
	if err != nil {
		t.Fatalf("FormatMarkdownDocument: %v", err)
	}

	normalized, err := normalizeDiffMarkdown(raw)
	if err != nil {
		t.Fatalf("normalizeDiffMarkdown: %v", err)
	}

	parsed, err := fs.ParseMarkdownDocument(normalized)
	if err != nil {
		t.Fatalf("ParseMarkdownDocument: %v", err)
	}

	if parsed.Frontmatter.CreatedBy != "" {
		t.Errorf("CreatedBy not stripped: %q", parsed.Frontmatter.CreatedBy)
	}
	if parsed.Frontmatter.CreatedAt != "" {
		t.Errorf("CreatedAt not stripped: %q", parsed.Frontmatter.CreatedAt)
	}
	if parsed.Frontmatter.UpdatedBy != "" {
		t.Errorf("UpdatedBy not stripped: %q", parsed.Frontmatter.UpdatedBy)
	}
	if parsed.Frontmatter.UpdatedAt != "" {
		t.Errorf("UpdatedAt not stripped: %q", parsed.Frontmatter.UpdatedAt)
	}
	if parsed.Frontmatter.Title != "My Page" {
		t.Errorf("Title changed: %q", parsed.Frontmatter.Title)
	}
	if parsed.Frontmatter.ID != "42" {
		t.Errorf("ID changed: %q", parsed.Frontmatter.ID)
	}
	if parsed.Frontmatter.Version != 3 {
		t.Errorf("Version changed: %d", parsed.Frontmatter.Version)
	}
	if parsed.Body != "some content\n" {
		t.Errorf("Body changed: %q", parsed.Body)
	}
}

func TestRunDiff_FileModeIgnoresMetadataOnlyChanges(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	localFile := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, localFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version:   2,
			UpdatedBy: "old-user@example.com",
			UpdatedAt: "2026-01-01T00:00:00Z",
		},
		Body: "same body\n",
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
				BodyADF:      rawJSON(t, simpleADF("same body")),
			},
		},
		attachments: map[string][]byte{},
	}

	oldFactory := newDiffRemote
	newDiffRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newDiffRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runDiff(cmd, config.Target{Mode: config.TargetModeFile, Value: localFile}); err != nil {
		t.Fatalf("runDiff() error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "diff completed with no differences") {
		t.Fatalf("expected no-diff when only metadata differs, got:\n%s", got)
	}
}

func TestRunDiff_FileModeShowsSyncedMetadataParity(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	localFile := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, localFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Version: 2,
		},
		Body: "same body\n",
	})

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Root", Status: "draft", Version: 2, LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC)},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Status:       "draft",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, simpleADF("same body")),
			},
		},
		attachments:       map[string][]byte{},
		contentStatusByID: map[string]string{"1": "Ready to review"},
		labelsByPage:      map[string][]string{"1": {"beta", "alpha"}},
	}

	oldFactory := newDiffRemote
	newDiffRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newDiffRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runDiff(cmd, config.Target{Mode: config.TargetModeFile, Value: localFile}); err != nil {
		t.Fatalf("runDiff() error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "+state: draft") {
		t.Fatalf("expected state metadata diff, got:\n%s", got)
	}
	if !strings.Contains(got, "+status: Ready to review") {
		t.Fatalf("expected content-status diff, got:\n%s", got)
	}
	if !strings.Contains(got, "+labels:") || !strings.Contains(got, "+    - alpha") || !strings.Contains(got, "+    - beta") {
		t.Fatalf("expected normalized labels diff, got:\n%s", got)
	}
}

func TestRunDiff_FileModeShowsLabelOnlyMetadataSummary(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	localFile := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, localFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Version: 2,
			Labels:  []string{"beta"},
		},
		Body: "same body\n",
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
				BodyADF:      rawJSON(t, simpleADF("same body")),
			},
		},
		attachments:  map[string][]byte{},
		labelsByPage: map[string][]string{"1": {"gamma", "alpha", "gamma"}},
	}

	oldFactory := newDiffRemote
	newDiffRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newDiffRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runDiff(cmd, config.Target{Mode: config.TargetModeFile, Value: localFile}); err != nil {
		t.Fatalf("runDiff() error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "metadata drift summary") {
		t.Fatalf("expected metadata summary, got:\n%s", got)
	}
	if !strings.Contains(got, "labels: [beta] -> [alpha, gamma]") {
		t.Fatalf("expected normalized label summary, got:\n%s", got)
	}
	if strings.Index(got, "metadata drift summary") > strings.Index(got, "diff --git") {
		t.Fatalf("expected metadata summary before textual diff, got:\n%s", got)
	}
}

func TestRunDiff_SpaceModeShowsMetadataSummaryForRemoteMetadataOnlyChanges(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "Root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Version: 2,
		},
		Body: "same body\n",
	})

	if err := fs.SaveState(spaceDir, fs.SpaceState{
		PagePathIndex: map[string]string{
			"Root.md": "1",
		},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Root", Status: "draft", Version: 2, LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC)},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Status:       "draft",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, simpleADF("same body")),
			},
		},
		attachments:       map[string][]byte{},
		contentStatusByID: map[string]string{"1": "Ready to review"},
		labelsByPage:      map[string][]string{"1": {"beta", "alpha"}},
	}

	oldFactory := newDiffRemote
	newDiffRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newDiffRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runDiff(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"}); err != nil {
		t.Fatalf("runDiff() error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "metadata drift summary") {
		t.Fatalf("expected metadata summary, got:\n%s", got)
	}
	if !strings.Contains(got, "Root.md") {
		t.Fatalf("expected metadata summary to include path, got:\n%s", got)
	}
	if !strings.Contains(got, "state: current -> draft") {
		t.Fatalf("expected state summary, got:\n%s", got)
	}
	if !strings.Contains(got, `status: "" -> "Ready to review"`) {
		t.Fatalf("expected status summary, got:\n%s", got)
	}
	if !strings.Contains(got, "labels: [] -> [alpha, beta]") {
		t.Fatalf("expected labels summary, got:\n%s", got)
	}
}

func TestRunDiff_FileModeOmitsMetadataSummaryForContentOnlyChanges(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	localFile := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, localFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Version: 2,
			State:   "draft",
			Status:  "Ready to review",
			Labels:  []string{"alpha", "beta"},
		},
		Body: "old body\n",
	})

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Root", Status: "draft", Version: 2, LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC)},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Status:       "draft",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, simpleADF("new body")),
			},
		},
		attachments:       map[string][]byte{},
		contentStatusByID: map[string]string{"1": "Ready to review"},
		labelsByPage:      map[string][]string{"1": {"beta", "alpha"}},
	}

	oldFactory := newDiffRemote
	newDiffRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newDiffRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runDiff(cmd, config.Target{Mode: config.TargetModeFile, Value: localFile}); err != nil {
		t.Fatalf("runDiff() error: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "metadata drift summary") {
		t.Fatalf("did not expect metadata summary for content-only changes, got:\n%s", got)
	}
	if !strings.Contains(got, "-old body") || !strings.Contains(got, "+new body") {
		t.Fatalf("expected content diff, got:\n%s", got)
	}
}

func TestRunDiff_FileModeShowsMetadataSummaryBeforeCombinedMetadataAndContentChanges(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	localFile := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, localFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Version: 2,
			Labels:  []string{"beta"},
		},
		Body: "old body\n",
	})

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Root", Status: "draft", Version: 2, LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC)},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Status:       "draft",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, simpleADF("new body")),
			},
		},
		attachments:       map[string][]byte{},
		contentStatusByID: map[string]string{"1": "Ready to review"},
		labelsByPage:      map[string][]string{"1": {"beta", "alpha"}},
	}

	oldFactory := newDiffRemote
	newDiffRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return fake, nil }
	t.Cleanup(func() { newDiffRemote = oldFactory })

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runDiff(cmd, config.Target{Mode: config.TargetModeFile, Value: localFile}); err != nil {
		t.Fatalf("runDiff() error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "metadata drift summary") {
		t.Fatalf("expected metadata summary, got:\n%s", got)
	}
	if !strings.Contains(got, "state: current -> draft") {
		t.Fatalf("expected state summary, got:\n%s", got)
	}
	if !strings.Contains(got, "-old body") || !strings.Contains(got, "+new body") {
		t.Fatalf("expected content diff, got:\n%s", got)
	}
	if strings.Index(got, "metadata drift summary") > strings.Index(got, "diff --git") {
		t.Fatalf("expected metadata summary before textual diff, got:\n%s", got)
	}
}
