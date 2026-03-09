package cmd

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

func buildBulkPullRemote(t *testing.T, pageCount int) *cmdFakePullRemote {
	t.Helper()

	pages := make([]confluence.Page, 0, pageCount)
	pagesByID := make(map[string]confluence.Page, pageCount)
	for i := 1; i <= pageCount; i++ {
		id := fmt.Sprintf("%d", i)
		title := fmt.Sprintf("Page %d", i)
		page := confluence.Page{
			ID:           id,
			SpaceID:      "space-1",
			Title:        title,
			Version:      1,
			LastModified: time.Date(2026, time.February, 2, 10, i, 0, 0, time.UTC),
			BodyADF:      rawJSON(t, simpleADF(fmt.Sprintf("Body %d", i))),
		}
		pages = append(pages, confluence.Page{
			ID:           page.ID,
			SpaceID:      page.SpaceID,
			Title:        page.Title,
			Version:      page.Version,
			LastModified: page.LastModified,
		})
		pagesByID[id] = page
	}

	return &cmdFakePullRemote{
		space:       confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages:       pages,
		pagesByID:   pagesByID,
		attachments: map[string][]byte{},
	}
}

type cmdFakePullRemote struct {
	space             confluence.Space
	pages             []confluence.Page
	folderByID        map[string]confluence.Folder
	folderErr         error
	getPageErr        error
	getPageFunc       func(pageID string) (confluence.Page, error)
	changes           []confluence.Change
	listChanges       func(opts confluence.ChangeListOptions) (confluence.ChangeListResult, error)
	pagesByID         map[string]confluence.Page
	attachments       map[string][]byte
	attachmentsByPage map[string][]confluence.Attachment
	contentStatusByID map[string]string
	labelsByPage      map[string][]string
}

func (f *cmdFakePullRemote) GetUser(_ context.Context, accountID string) (confluence.User, error) {
	return confluence.User{AccountID: accountID, DisplayName: "User " + accountID}, nil
}

func (f *cmdFakePullRemote) GetSpace(_ context.Context, _ string) (confluence.Space, error) {
	return f.space, nil
}

func (f *cmdFakePullRemote) ListPages(_ context.Context, _ confluence.PageListOptions) (confluence.PageListResult, error) {
	return confluence.PageListResult{Pages: f.pages}, nil
}

func (f *cmdFakePullRemote) GetFolder(_ context.Context, folderID string) (confluence.Folder, error) {
	if f.folderErr != nil {
		return confluence.Folder{}, f.folderErr
	}
	folder, ok := f.folderByID[folderID]
	if !ok {
		return confluence.Folder{}, confluence.ErrNotFound
	}
	return folder, nil
}

func (f *cmdFakePullRemote) ListChanges(_ context.Context, opts confluence.ChangeListOptions) (confluence.ChangeListResult, error) {
	if f.listChanges != nil {
		return f.listChanges(opts)
	}
	return confluence.ChangeListResult{Changes: f.changes}, nil
}

func (f *cmdFakePullRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
	if f.getPageFunc != nil {
		return f.getPageFunc(pageID)
	}
	if f.getPageErr != nil {
		return confluence.Page{}, f.getPageErr
	}
	page, ok := f.pagesByID[pageID]
	if !ok {
		return confluence.Page{}, confluence.ErrNotFound
	}
	return page, nil
}

func (f *cmdFakePullRemote) GetContentStatus(_ context.Context, pageID string, _ string) (string, error) {
	if f.contentStatusByID == nil {
		return "", nil
	}
	return f.contentStatusByID[pageID], nil
}

func (f *cmdFakePullRemote) GetLabels(_ context.Context, pageID string) ([]string, error) {
	if f.labelsByPage == nil {
		return nil, nil
	}
	return append([]string(nil), f.labelsByPage[pageID]...), nil
}

func (f *cmdFakePullRemote) ListAttachments(_ context.Context, pageID string) ([]confluence.Attachment, error) {
	if f.attachmentsByPage == nil {
		return nil, nil
	}
	attachments := append([]confluence.Attachment(nil), f.attachmentsByPage[pageID]...)
	return attachments, nil
}

func (f *cmdFakePullRemote) DownloadAttachment(_ context.Context, attachmentID string, pageID string, out io.Writer) error {
	raw, ok := f.attachments[attachmentID]
	if !ok {
		return confluence.ErrNotFound
	}
	_, err := out.Write(raw)
	return err
}
