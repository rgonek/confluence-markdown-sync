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

func TestRunDiff_FileModeShowsContentChanges(t *testing.T) {
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	localFile := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, localFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "ENG",
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
				BodyADF:      rawJSON(t, simpleADF("new body")),
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
	if !strings.Contains(got, "-old body") {
		t.Fatalf("diff output missing removed local line:\n%s", got)
	}
	if !strings.Contains(got, "+new body") {
		t.Fatalf("diff output missing added remote line:\n%s", got)
	}
}

func TestRunDiff_SpaceModeNoDifferences(t *testing.T) {
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "ENG",
			Version:                2,
			ConfluenceLastModified: "2026-02-01T11:00:00Z",
		},
		Body: "same body\n",
	})

	if err := fs.SaveState(spaceDir, fs.SpaceState{
		PagePathIndex: map[string]string{
			"root.md": "1",
		},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

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

	if err := runDiff(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"}); err != nil {
		t.Fatalf("runDiff() error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "diff completed with no differences") {
		t.Fatalf("expected no-diff message, got:\n%s", got)
	}
}

func TestRunDiff_ReportsBestEffortWarnings(t *testing.T) {
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	localFile := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, localFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "ENG",
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

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runDiff(cmd, config.Target{Mode: config.TargetModeFile, Value: localFile}); err != nil {
		t.Fatalf("runDiff() error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "[unresolved_reference]") {
		t.Fatalf("expected unresolved warning, got:\n%s", got)
	}
}

func TestRunDiff_FolderListFailureFallsBackToPageHierarchy(t *testing.T) {
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	localFile := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, localFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Space:                  "ENG",
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

	setupEnv(t)
	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	out := &bytes.Buffer{}
	cmd.SetOut(out)

	if err := runDiff(cmd, config.Target{Mode: config.TargetModeFile, Value: localFile}); err != nil {
		t.Fatalf("runDiff() error: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "[FOLDER_LOOKUP_UNAVAILABLE]") {
		t.Fatalf("expected folder fallback warning, got:\n%s", got)
	}
	if !strings.Contains(got, "+new body") {
		t.Fatalf("diff output missing added remote line:\n%s", got)
	}
}

func diffUnresolvedADF() map[string]any {
	return map[string]any{
		"version": 1,
		"type":    "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "Missing",
						"marks": []any{
							map[string]any{
								"type": "link",
								"attrs": map[string]any{
									"href":     "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=404",
									"pageId":   "404",
									"spaceKey": "ENG",
								},
							},
						},
					},
				},
			},
		},
	}
}
