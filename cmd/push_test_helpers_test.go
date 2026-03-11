package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func preparePushRepoWithBaseline(t *testing.T, repo string) string {
	t.Helper()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Baseline\n",
	})

	if err := fs.SaveState(spaceDir, fs.SpaceState{
		PagePathIndex: map[string]string{
			"root.md": "1",
		},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")
	runGitForTest(t, repo, "tag", "-a", "confluence-sync/pull/ENG/20260201T120000Z", "-m", "baseline pull")

	return spaceDir
}

func preparePushRepoWithLinkedChildBaseline(t *testing.T, repo string) string {
	t.Helper()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Root",
			ID:                     "1",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "[Child](child.md)\n",
	})
	writeMarkdown(t, filepath.Join(spaceDir, "child.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:                  "Child",
			ID:                     "2",
			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "child body\n",
	})

	if err := fs.SaveState(spaceDir, fs.SpaceState{
		SpaceKey: "ENG",
		PagePathIndex: map[string]string{
			"root.md":  "1",
			"child.md": "2",
		},
		AttachmentIndex: map[string]string{},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n.confluence-state.json\n"), 0o600); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")
	runGitForTest(t, repo, "tag", "-a", "confluence-sync/pull/ENG/20260201T120000Z", "-m", "baseline pull")

	return spaceDir
}

type cmdFakePushRemote struct {
	space                 confluence.Space
	pages                 []confluence.Page
	pagesByID             map[string]confluence.Page
	updateCalls           []cmdPushUpdateCall
	archiveCalls          [][]string
	deletePageCalls       []string
	uploadAttachmentCalls []confluence.AttachmentUploadInput
	deleteAttachmentCalls []string
	webURL                string
}

type cmdPushUpdateCall struct {
	PageID string
	Input  confluence.PageUpsertInput
}

func newCmdFakePushRemote(remoteVersion int) *cmdFakePushRemote {
	page := confluence.Page{
		ID:           "1",
		SpaceID:      "space-1",
		Title:        "Root",
		Version:      remoteVersion,
		LastModified: time.Date(2026, time.February, 1, 10, 0, 0, 0, time.UTC),
		WebURL:       "https://example.atlassian.net/wiki/pages/1",
		BodyADF:      []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"remote content"}]}]}`),
	}
	return &cmdFakePushRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{page},
		pagesByID: map[string]confluence.Page{
			"1": page,
		},
		webURL: page.WebURL,
	}
}

func (f *cmdFakePushRemote) GetUser(_ context.Context, accountID string) (confluence.User, error) {
	return confluence.User{AccountID: accountID, DisplayName: "User " + accountID}, nil
}

func (f *cmdFakePushRemote) GetSpace(_ context.Context, _ string) (confluence.Space, error) {
	return f.space, nil
}

func (f *cmdFakePushRemote) ListPages(_ context.Context, _ confluence.PageListOptions) (confluence.PageListResult, error) {
	return confluence.PageListResult{Pages: f.pages}, nil
}

func (f *cmdFakePushRemote) ListContentStates(_ context.Context) ([]confluence.ContentState, error) {
	return []confluence.ContentState{{ID: 80, Name: "Ready to review", Color: "FFAB00"}}, nil
}

func (f *cmdFakePushRemote) ListSpaceContentStates(_ context.Context, _ string) ([]confluence.ContentState, error) {
	return []confluence.ContentState{{ID: 80, Name: "Ready to review", Color: "FFAB00"}}, nil
}

func (f *cmdFakePushRemote) GetAvailableContentStates(_ context.Context, _ string) ([]confluence.ContentState, error) {
	return []confluence.ContentState{{ID: 80, Name: "Ready to review", Color: "FFAB00"}}, nil
}

func (f *cmdFakePushRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
	page, ok := f.pagesByID[pageID]
	if !ok {
		return confluence.Page{}, confluence.ErrNotFound
	}
	return page, nil
}

func (f *cmdFakePushRemote) GetContentStatus(_ context.Context, _ string, _ string) (string, error) {
	return "", nil
}

func (f *cmdFakePushRemote) SetContentStatus(_ context.Context, _ string, _ string, _ confluence.ContentState) error {
	return nil
}

func (f *cmdFakePushRemote) DeleteContentStatus(_ context.Context, _ string, _ string) error {
	return nil
}

func (f *cmdFakePushRemote) GetLabels(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (f *cmdFakePushRemote) ListAttachments(_ context.Context, _ string) ([]confluence.Attachment, error) {
	return nil, nil
}

func (f *cmdFakePushRemote) GetAttachment(_ context.Context, attachmentID string) (confluence.Attachment, error) {
	return confluence.Attachment{ID: strings.TrimSpace(attachmentID)}, nil
}

func (f *cmdFakePushRemote) AddLabels(_ context.Context, _ string, _ []string) error {
	return nil
}

func (f *cmdFakePushRemote) RemoveLabel(_ context.Context, _ string, _ string) error {
	return nil
}

func (f *cmdFakePushRemote) CreatePage(_ context.Context, input confluence.PageUpsertInput) (confluence.Page, error) {
	id := fmt.Sprintf("new-page-%d", len(f.pagesByID)+1)
	created := confluence.Page{
		ID:           id,
		SpaceID:      input.SpaceID,
		Title:        input.Title,
		ParentPageID: input.ParentPageID,
		Version:      1,
		LastModified: time.Now().UTC(),
		WebURL:       fmt.Sprintf("https://example.atlassian.net/wiki/pages/%s", id),
	}
	f.pagesByID[id] = created
	f.pages = append(f.pages, created)
	return created, nil
}

func (f *cmdFakePushRemote) UpdatePage(_ context.Context, pageID string, input confluence.PageUpsertInput) (confluence.Page, error) {
	f.updateCalls = append(f.updateCalls, cmdPushUpdateCall{PageID: pageID, Input: input})
	updated := confluence.Page{
		ID:           pageID,
		SpaceID:      input.SpaceID,
		Title:        input.Title,
		ParentPageID: input.ParentPageID,
		Version:      input.Version,
		LastModified: time.Date(2026, time.February, 1, 12, 0, 0, 0, time.UTC),
		WebURL:       firstOrDefault(strings.TrimSpace(f.webURL), fmt.Sprintf("https://example.atlassian.net/wiki/pages/%s", pageID)),
	}
	f.pagesByID[pageID] = updated
	f.pages = []confluence.Page{updated}
	return updated, nil
}

func (f *cmdFakePushRemote) ArchivePages(_ context.Context, pageIDs []string) (confluence.ArchiveResult, error) {
	clone := append([]string(nil), pageIDs...)
	f.archiveCalls = append(f.archiveCalls, clone)
	return confluence.ArchiveResult{TaskID: "task-1"}, nil
}

func (f *cmdFakePushRemote) WaitForArchiveTask(_ context.Context, taskID string, _ confluence.ArchiveTaskWaitOptions) (confluence.ArchiveTaskStatus, error) {
	return confluence.ArchiveTaskStatus{TaskID: taskID, State: confluence.ArchiveTaskStateSucceeded}, nil
}

func (f *cmdFakePushRemote) DeletePage(_ context.Context, pageID string, _ confluence.PageDeleteOptions) error {
	f.deletePageCalls = append(f.deletePageCalls, pageID)
	return nil
}

func (f *cmdFakePushRemote) UploadAttachment(_ context.Context, input confluence.AttachmentUploadInput) (confluence.Attachment, error) {
	f.uploadAttachmentCalls = append(f.uploadAttachmentCalls, input)
	id := fmt.Sprintf("att-%d", len(f.uploadAttachmentCalls))
	return confluence.Attachment{ID: id, PageID: input.PageID, Filename: input.Filename}, nil
}

func (f *cmdFakePushRemote) GetFolder(_ context.Context, _ string) (confluence.Folder, error) {
	return confluence.Folder{}, confluence.ErrNotFound
}

func (f *cmdFakePushRemote) ListChanges(_ context.Context, _ confluence.ChangeListOptions) (confluence.ChangeListResult, error) {
	return confluence.ChangeListResult{
		Changes: []confluence.Change{
			{PageID: "1", SpaceKey: "ENG", Version: 1, LastModified: time.Now().UTC()},
		},
	}, nil
}

func (f *cmdFakePushRemote) DownloadAttachment(_ context.Context, _ string, _ string, out io.Writer) error {
	_, err := out.Write([]byte("fake-bytes"))
	return err
}

func (f *cmdFakePushRemote) DeleteAttachment(_ context.Context, attachmentID string, _ string) error {
	f.deleteAttachmentCalls = append(f.deleteAttachmentCalls, attachmentID)
	return nil
}

func (f *cmdFakePushRemote) CreateFolder(_ context.Context, input confluence.FolderCreateInput) (confluence.Folder, error) {
	id := fmt.Sprintf("folder-%d", len(f.pagesByID)+1)
	return confluence.Folder{
		ID:         id,
		SpaceID:    input.SpaceID,
		Title:      input.Title,
		ParentID:   input.ParentID,
		ParentType: input.ParentType,
	}, nil
}

func (f *cmdFakePushRemote) ListFolders(_ context.Context, _ confluence.FolderListOptions) (confluence.FolderListResult, error) {
	return confluence.FolderListResult{}, nil
}

func (f *cmdFakePushRemote) DeleteFolder(_ context.Context, _ string) error {
	return nil
}

func (f *cmdFakePushRemote) MovePage(_ context.Context, _ string, _ string) error {
	return nil
}

func firstOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
