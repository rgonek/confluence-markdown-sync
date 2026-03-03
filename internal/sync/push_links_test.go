package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestPush_PreflightStrictFailureSkipsRemoteMutations(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "new.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New",
		},
		Body: "[Broken](missing.md)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeAdd,
			Path: "new.md",
		}},
	})
	if err == nil {
		t.Fatal("expected strict conversion error")
	}
	if !strings.Contains(err.Error(), "strict conversion failed") {
		t.Fatalf("unexpected error: %v", err)
	}

	if remote.createPageCalls != 0 {
		t.Fatalf("create page calls = %d, want 0", remote.createPageCalls)
	}
	if remote.updatePageCalls != 0 {
		t.Fatalf("update page calls = %d, want 0", remote.updatePageCalls)
	}
	if remote.uploadAttachmentCalls != 0 {
		t.Fatalf("upload attachment calls = %d, want 0", remote.uploadAttachmentCalls)
	}
}

func TestPush_PreflightStrictResolvesCrossSpaceLinkWithGlobalIndex(t *testing.T) {
	repo := t.TempDir()
	engDir := filepath.Join(repo, "Engineering (ENG)")
	tdDir := filepath.Join(repo, "Technical Docs (TD)")
	if err := os.MkdirAll(engDir, 0o750); err != nil {
		t.Fatalf("mkdir eng dir: %v", err)
	}
	if err := os.MkdirAll(tdDir, 0o750); err != nil {
		t.Fatalf("mkdir td dir: %v", err)
	}

	mdPath := filepath.Join(engDir, "new.md")
	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New",
		},
		Body: "[Cross Space](../Technical%20Docs%20(TD)/target.md)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	targetPath := filepath.Join(tdDir, "target.md")
	if err := fs.WriteMarkdownDocument(targetPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Target",
			ID:    "200",

			Version: 1,
		},
		Body: "target\n",
	}); err != nil {
		t.Fatalf("write cross-space markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:            "ENG",
		SpaceDir:            engDir,
		Domain:              "https://example.atlassian.net",
		State:               fs.SpaceState{SpaceKey: "ENG"},
		GlobalPageIndex:     GlobalPageIndex{"200": targetPath},
		ConflictPolicy:      PushConflictPolicyCancel,
		Changes:             []PushFileChange{{Type: PushChangeAdd, Path: "new.md"}},
		ArchiveTimeout:      confluence.DefaultArchiveTaskTimeout,
		ArchivePollInterval: confluence.DefaultArchiveTaskPollInterval,
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}
	if len(result.Commits) != 1 {
		t.Fatalf("commit count = %d, want 1", len(result.Commits))
	}
}

func TestPush_ResolvesLinksBetweenSimultaneousNewPages(t *testing.T) {
	spaceDir := t.TempDir()

	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "Fancy-Extensions.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Fancy Extensions",
		},
		Body: "[New page](New-Page.md)\n",
	}); err != nil {
		t.Fatalf("write Fancy-Extensions.md: %v", err)
	}

	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "New-Page.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New Page",
		},
		Body: "new page body\n",
	}); err != nil {
		t.Fatalf("write New-Page.md: %v", err)
	}

	remote := newRollbackPushRemote()
	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{
			{Type: PushChangeAdd, Path: "Fancy-Extensions.md"},
			{Type: PushChangeAdd, Path: "New-Page.md"},
		},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	fancyID := strings.TrimSpace(result.State.PagePathIndex["Fancy-Extensions.md"])
	newPageID := strings.TrimSpace(result.State.PagePathIndex["New-Page.md"])
	if fancyID == "" || newPageID == "" {
		t.Fatalf("expected IDs for both new pages, got state index: %+v", result.State.PagePathIndex)
	}

	updateInput, ok := remote.updateInputsByPageID[fancyID]
	if !ok {
		t.Fatalf("expected update payload for Fancy-Extensions page ID %s", fancyID)
	}

	body := string(updateInput.BodyADF)
	if !strings.Contains(body, "pageId="+newPageID) {
		t.Fatalf("expected Fancy-Extensions link to resolve to new page ID %s, body=%s", newPageID, body)
	}
	if strings.Contains(body, "pending-page-") {
		t.Fatalf("expected final ADF to avoid pending page IDs, body=%s", body)
	}
}
