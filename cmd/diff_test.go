package cmd

import (
	"bytes"
	"context"
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

func TestRunDiff_FileModeShowsContentChanges(t *testing.T) {
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
	if strings.Contains(got, "conf-diff-") {
		t.Fatalf("diff output should not leak temp directory paths:\n%s", got)
	}
	if strings.Contains(strings.ToLower(got), "warning: in the working copy") {
		t.Fatalf("diff output should not include CRLF working-copy warnings:\n%s", got)
	}
}

func TestRunDiff_SpaceModeNoDifferences(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

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
	if !strings.Contains(got, "action required: yes") {
		t.Fatalf("expected actionable unresolved warning, got:\n%s", got)
	}
	if !strings.Contains(got, "unresolved but safely degraded reference") {
		t.Fatalf("expected degraded-reference classification, got:\n%s", got)
	}
}

func TestRunDiff_SpaceModeReportsPlannedPathMoves(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(filepath.Join(spaceDir, "Policies"), 0o750); err != nil {
		t.Fatalf("mkdir policies dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "Policies", "Child.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Child",
			ID:      "2",
			Version: 1,
		},
		Body: "same body\n",
	})

	if err := fs.SaveState(spaceDir, fs.SpaceState{
		PagePathIndex: map[string]string{
			"Policies/Child.md": "2",
		},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	modifiedAt := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-2", ParentType: "folder", Version: 2, LastModified: modifiedAt},
		},
		folderByID: map[string]confluence.Folder{
			"folder-2": {ID: "folder-2", Title: "Archive"},
		},
		pagesByID: map[string]confluence.Page{
			"2": {
				ID:           "2",
				SpaceID:      "space-1",
				Title:        "Child",
				ParentPageID: "folder-2",
				ParentType:   "folder",
				Version:      2,
				LastModified: modifiedAt,
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
	if !strings.Contains(got, "[PAGE_PATH_MOVED]") {
		t.Fatalf("expected path move diagnostic, got:\n%s", got)
	}
	if !strings.Contains(got, "Policies/Child.md") || !strings.Contains(got, "Archive/Child.md") {
		t.Fatalf("expected old and new paths in diff output, got:\n%s", got)
	}
}

func TestRunDiff_FileModeReportsPlannedPathMoves(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(filepath.Join(spaceDir, "Policies"), 0o750); err != nil {
		t.Fatalf("mkdir policies dir: %v", err)
	}

	localFile := filepath.Join(spaceDir, "Policies", "Child.md")
	writeMarkdown(t, localFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Child",
			ID:      "2",
			Version: 1,
		},
		Body: "same body\n",
	})

	if err := fs.SaveState(spaceDir, fs.SpaceState{
		PagePathIndex: map[string]string{
			"Policies/Child.md": "2",
		},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	modifiedAt := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-2", ParentType: "folder", Version: 2, LastModified: modifiedAt},
		},
		folderByID: map[string]confluence.Folder{
			"folder-2": {ID: "folder-2", Title: "Archive"},
		},
		pagesByID: map[string]confluence.Page{
			"2": {
				ID:           "2",
				SpaceID:      "space-1",
				Title:        "Child",
				ParentPageID: "folder-2",
				ParentType:   "folder",
				Version:      2,
				LastModified: modifiedAt,
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
	if !strings.Contains(got, "[PAGE_PATH_MOVED]") {
		t.Fatalf("expected path move diagnostic, got:\n%s", got)
	}
	if !strings.Contains(got, "Policies/Child.md") || !strings.Contains(got, "Archive/Child.md") {
		t.Fatalf("expected old and new paths in diff output, got:\n%s", got)
	}
}

func TestRunDiff_FileModeUsesPlannedPathContextForMovedPageLinks(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "ENG")
	if err := os.MkdirAll(filepath.Join(spaceDir, "Policies"), 0o750); err != nil {
		t.Fatalf("mkdir policies dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(spaceDir, "Archive"), 0o750); err != nil {
		t.Fatalf("mkdir archive dir: %v", err)
	}

	localFile := filepath.Join(spaceDir, "Policies", "Child.md")
	writeMarkdown(t, localFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Child",
			ID:      "2",
			Version: 1,
		},
		Body: "old body\n",
	})
	writeMarkdown(t, filepath.Join(spaceDir, "Archive", "Reference.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Reference",
			ID:      "3",
			Version: 1,
		},
		Body: "reference body\n",
	})

	if err := fs.SaveState(spaceDir, fs.SpaceState{
		PagePathIndex: map[string]string{
			"Policies/Child.md":    "2",
			"Archive/Reference.md": "3",
		},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	modifiedAt := time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC)
	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-2", ParentType: "folder", Version: 2, LastModified: modifiedAt},
			{ID: "3", SpaceID: "space-1", Title: "Reference", ParentPageID: "folder-2", ParentType: "folder", Version: 1, LastModified: modifiedAt},
		},
		folderByID: map[string]confluence.Folder{
			"folder-2": {ID: "folder-2", Title: "Archive"},
		},
		pagesByID: map[string]confluence.Page{
			"2": {
				ID:           "2",
				SpaceID:      "space-1",
				Title:        "Child",
				ParentPageID: "folder-2",
				ParentType:   "folder",
				Version:      2,
				LastModified: modifiedAt,
				BodyADF: rawJSON(t, map[string]any{
					"version": 1,
					"type":    "doc",
					"content": []any{
						map[string]any{
							"type": "paragraph",
							"content": []any{
								map[string]any{
									"type": "text",
									"text": "Reference",
									"marks": []any{
										map[string]any{
											"type": "link",
											"attrs": map[string]any{
												"href":   "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=3",
												"pageId": "3",
											},
										},
									},
								},
							},
						},
					},
				}),
			},
			"3": {
				ID:           "3",
				SpaceID:      "space-1",
				Title:        "Reference",
				ParentPageID: "folder-2",
				ParentType:   "folder",
				Version:      1,
				LastModified: modifiedAt,
				BodyADF:      rawJSON(t, simpleADF("reference body")),
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
	if !strings.Contains(got, "+[Reference](Reference.md)") {
		t.Fatalf("expected moved page to render relative link from planned path context, got:\n%s", got)
	}
	if strings.Contains(got, "../Archive/Reference.md") {
		t.Fatalf("expected moved page diff to avoid old-path link context, got:\n%s", got)
	}
}

func TestRunDiff_PreservedAbsoluteCrossSpaceLinkIsNotReportedAsUnresolved(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	engDir := filepath.Join(repo, "Engineering (ENG)")
	tdDir := filepath.Join(repo, "Technical Docs (TD)")
	if err := os.MkdirAll(engDir, 0o750); err != nil {
		t.Fatalf("mkdir eng dir: %v", err)
	}
	if err := os.MkdirAll(tdDir, 0o750); err != nil {
		t.Fatalf("mkdir td dir: %v", err)
	}

	targetPath := filepath.Join(tdDir, "target.md")
	writeMarkdown(t, targetPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Target", ID: "200", Version: 1},
		Body:        "target body\n",
	})

	localFile := filepath.Join(engDir, "root.md")
	writeMarkdown(t, localFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Version: 1,
		},
		Body: "old body\n",
	})

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Root", Version: 2, LastModified: time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC)},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Version:      2,
				LastModified: time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC),
				BodyADF: rawJSON(t, map[string]any{
					"version": 1,
					"type":    "doc",
					"content": []any{
						map[string]any{
							"type": "paragraph",
							"content": []any{
								map[string]any{
									"type": "text",
									"text": "Cross Space",
									"marks": []any{
										map[string]any{
											"type": "link",
											"attrs": map[string]any{
												"href":   "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=200",
												"pageId": "200",
												"anchor": "section-a",
											},
										},
									},
								},
							},
						},
					},
				}),
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
	if strings.Contains(got, "[unresolved_reference]") {
		t.Fatalf("did not expect unresolved warning for preserved cross-space link, got:\n%s", got)
	}
	if !strings.Contains(got, "[CROSS_SPACE_LINK_PRESERVED]") {
		t.Fatalf("expected preserved cross-space diagnostic, got:\n%s", got)
	}
	if !strings.Contains(got, "preserved external/cross-space link; action required: no") {
		t.Fatalf("expected informational preserved-link classification, got:\n%s", got)
	}
}

func TestRunDiff_FolderListFailureFallsBackToPageHierarchy(t *testing.T) {
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

func TestRunDiff_DeduplicatesFolderFallbackWarnings(t *testing.T) {
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

			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "old body\n",
	})

	fake := &cmdFakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Root", ParentPageID: "folder-1", ParentType: "folder", Version: 2, LastModified: time.Date(2026, time.February, 1, 11, 0, 0, 0, time.UTC)},
			{ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-2", ParentType: "folder", Version: 2, LastModified: time.Date(2026, time.February, 1, 11, 5, 0, 0, time.UTC)},
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
			"2": {
				ID:           "2",
				SpaceID:      "space-1",
				Title:        "Child",
				ParentPageID: "folder-2",
				ParentType:   "folder",
				Version:      2,
				LastModified: time.Date(2026, time.February, 1, 11, 5, 0, 0, time.UTC),
				BodyADF:      rawJSON(t, simpleADF("child body")),
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
	if count := strings.Count(got, "[FOLDER_LOOKUP_UNAVAILABLE]"); count != 1 {
		t.Fatalf("expected one deduplicated folder fallback warning, got %d:\n%s", count, got)
	}
	if strings.Contains(got, "Internal Server Error") {
		t.Fatalf("expected concise operator warning without raw API error, got:\n%s", got)
	}
	if strings.Contains(got, "/wiki/api/v2/folders") {
		t.Fatalf("expected concise operator warning without raw API URL, got:\n%s", got)
	}
	if !strings.Contains(got, "falling back to page-only hierarchy for affected pages") {
		t.Fatalf("expected concise folder fallback warning, got:\n%s", got)
	}
}

func TestRunDiff_RespectsCanceledContext(t *testing.T) {
	runParallelCommandTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetContext(ctx)

	err := runDiff(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got: %v", err)
	}
}

func TestRunDiff_FileModeNewPageWithoutIDPointsToPreflight(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	newFile := filepath.Join(spaceDir, "new-page.md")
	writeMarkdown(t, newFile, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "New Page"},
		Body:        "preview me\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG"}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	chdirRepo(t, repo)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	err := runDiff(cmd, config.Target{Mode: config.TargetModeFile, Value: newFile})
	if err == nil {
		t.Fatal("expected diff guidance error for brand-new file")
	}
	if !strings.Contains(err.Error(), "has no id") {
		t.Fatalf("expected missing-id guidance, got: %v", err)
	}
	if !strings.Contains(err.Error(), "conf push --preflight") {
		t.Fatalf("expected preflight guidance, got: %v", err)
	}
}

func TestRecoverMissingPagesForDiff_SkipsTrashedPages(t *testing.T) {
	runParallelCommandTest(t)
	fake := &cmdFakePullRemote{
		pagesByID: map[string]confluence.Page{
			"10": {
				ID:      "10",
				SpaceID: "space-1",
				Status:  "trashed",
			},
		},
	}

	recovered, err := recoverMissingPagesForDiff(
		context.Background(),
		fake,
		"space-1",
		map[string]string{"old.md": "10"},
		nil,
	)
	if err != nil {
		t.Fatalf("recoverMissingPagesForDiff() error: %v", err)
	}

	if len(recovered) != 0 {
		t.Fatalf("expected trashed page to be skipped, got %+v", recovered)
	}
}

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
	// Meaningful fields must be preserved
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

	// Local file has stale author metadata
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
		labelsByPage:      map[string][]string{"1": []string{"beta", "alpha"}},
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

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Version: 2,
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
	if !strings.Contains(got, "root.md") {
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
