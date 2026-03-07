package sync

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

type fakePullRemote struct {
	space             confluence.Space
	pages             []confluence.Page
	folderByID        map[string]confluence.Folder
	folderErr         error
	getFolderCalls    []string
	changes           []confluence.Change
	listChangesFunc   func(opts confluence.ChangeListOptions) (confluence.ChangeListResult, error)
	pagesByID         map[string]confluence.Page
	attachments       map[string][]byte
	attachmentsByPage map[string][]confluence.Attachment
	labels            map[string][]string
	users             map[string]confluence.User
	contentStatuses   map[string]string
	contentStatusErr  error
	getStatusCalls    []string
	lastChangeSince   time.Time
	getPageHook       func(pageID string)
}

func (f *fakePullRemote) GetUser(_ context.Context, accountID string) (confluence.User, error) {
	if f.users == nil {
		return confluence.User{AccountID: accountID, DisplayName: "User " + accountID}, nil
	}
	user, ok := f.users[accountID]
	if !ok {
		return confluence.User{}, confluence.ErrNotFound
	}
	return user, nil
}

func (f *fakePullRemote) GetSpace(_ context.Context, _ string) (confluence.Space, error) {
	return f.space, nil
}

func (f *fakePullRemote) ListPages(_ context.Context, _ confluence.PageListOptions) (confluence.PageListResult, error) {
	return confluence.PageListResult{Pages: f.pages}, nil
}

func (f *fakePullRemote) GetFolder(_ context.Context, folderID string) (confluence.Folder, error) {
	f.getFolderCalls = append(f.getFolderCalls, folderID)
	if f.folderErr != nil {
		return confluence.Folder{}, f.folderErr
	}
	folder, ok := f.folderByID[folderID]
	if !ok {
		return confluence.Folder{}, confluence.ErrNotFound
	}
	return folder, nil
}

func (f *fakePullRemote) ListChanges(_ context.Context, opts confluence.ChangeListOptions) (confluence.ChangeListResult, error) {
	if f.listChangesFunc != nil {
		return f.listChangesFunc(opts)
	}
	f.lastChangeSince = opts.Since
	return confluence.ChangeListResult{Changes: f.changes}, nil
}

func (f *fakePullRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
	if f.getPageHook != nil {
		f.getPageHook(pageID)
	}
	page, ok := f.pagesByID[pageID]
	if !ok {
		return confluence.Page{}, confluence.ErrNotFound
	}
	return page, nil
}

func (f *fakePullRemote) GetContentStatus(_ context.Context, pageID string, _ string) (string, error) {
	f.getStatusCalls = append(f.getStatusCalls, pageID)
	if f.contentStatusErr != nil {
		return "", f.contentStatusErr
	}
	if f.contentStatuses == nil {
		return "", nil
	}
	return f.contentStatuses[pageID], nil
}

func (f *fakePullRemote) GetLabels(_ context.Context, pageID string) ([]string, error) {
	if f.labels == nil {
		return nil, nil
	}
	return f.labels[pageID], nil
}

func (f *fakePullRemote) ListAttachments(_ context.Context, pageID string) ([]confluence.Attachment, error) {
	if f.attachmentsByPage == nil {
		return nil, nil
	}
	attachments := append([]confluence.Attachment(nil), f.attachmentsByPage[pageID]...)
	return attachments, nil
}

func (f *fakePullRemote) DownloadAttachment(_ context.Context, attachmentID string, pageID string, out io.Writer) error {
	raw, ok := f.attachments[attachmentID]
	if !ok {
		return confluence.ErrNotFound
	}
	_, err := out.Write(raw)
	return err
}

func rawJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return raw
}

func sampleRootADF() map[string]any {
	return map[string]any{
		"version": 1,
		"type":    "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "Known",
						"marks": []any{
							map[string]any{
								"type": "link",
								"attrs": map[string]any{
									"href":     "https://example.atlassian.net/wiki/pages/viewpage.action?pageId=2",
									"pageId":   "2",
									"spaceKey": "ENG",
									"anchor":   "section-a",
								},
							},
						},
					},
					map[string]any{
						"type": "text",
						"text": " ",
					},
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
			map[string]any{
				"type": "mediaSingle",
				"content": []any{
					map[string]any{
						"type": "media",
						"attrs": map[string]any{
							"type":         "image",
							"id":           "att-1",
							"attachmentId": "att-1",
							"pageId":       "1",
							"fileName":     "diagram.png",
							"alt":          "Diagram",
						},
					},
				},
			},
		},
	}
}

func sampleChildADF() map[string]any {
	return map[string]any{
		"version": 1,
		"type":    "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "text",
						"text": "Child body",
					},
					map[string]any{
						"type": "mediaInline",
						"attrs": map[string]any{
							"id":       "att-2",
							"fileName": "inline.png",
						},
					},
				},
			},
		},
	}
}
