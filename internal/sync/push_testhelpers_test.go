package sync

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

// fakeFolderPushRemote is a minimal fake used for folder/hierarchy tests.
type fakeFolderPushRemote struct {
	folders     []confluence.Folder
	foldersByID map[string]confluence.Folder
	pages       []confluence.Page
	pagesByID   map[string]confluence.Page
	moves       []fakePageMove
}

type fakePageMove struct {
	pageID   string
	targetID string
}

type contentStatusCall struct {
	PageID     string
	PageStatus string
	StatusName string
}

func (f *fakeFolderPushRemote) GetSpace(_ context.Context, spaceKey string) (confluence.Space, error) {
	return confluence.Space{ID: "space-1", Key: spaceKey}, nil
}

func (f *fakeFolderPushRemote) ListPages(_ context.Context, _ confluence.PageListOptions) (confluence.PageListResult, error) {
	return confluence.PageListResult{Pages: f.pages}, nil
}

func (f *fakeFolderPushRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
	if page, ok := f.pagesByID[pageID]; ok {
		return page, nil
	}
	return confluence.Page{}, confluence.ErrNotFound
}

func (f *fakeFolderPushRemote) GetContentStatus(_ context.Context, pageID string, _ string) (string, error) {
	return "", nil
}

func (f *fakeFolderPushRemote) SetContentStatus(_ context.Context, pageID string, _ string, statusName string) error {
	return nil
}

func (f *fakeFolderPushRemote) DeleteContentStatus(_ context.Context, pageID string, _ string) error {
	return nil
}

func (f *fakeFolderPushRemote) GetLabels(_ context.Context, pageID string) ([]string, error) {
	return nil, nil
}

func (f *fakeFolderPushRemote) AddLabels(_ context.Context, pageID string, labels []string) error {
	return nil
}

func (f *fakeFolderPushRemote) RemoveLabel(_ context.Context, pageID string, labelName string) error {
	return nil
}

func (f *fakeFolderPushRemote) CreatePage(_ context.Context, input confluence.PageUpsertInput) (confluence.Page, error) {
	return confluence.Page{}, nil
}

func (f *fakeFolderPushRemote) UpdatePage(_ context.Context, pageID string, input confluence.PageUpsertInput) (confluence.Page, error) {
	return confluence.Page{}, nil
}

func (f *fakeFolderPushRemote) ArchivePages(_ context.Context, pageIDs []string) (confluence.ArchiveResult, error) {
	return confluence.ArchiveResult{}, nil
}

func (f *fakeFolderPushRemote) WaitForArchiveTask(_ context.Context, _ string, _ confluence.ArchiveTaskWaitOptions) (confluence.ArchiveTaskStatus, error) {
	return confluence.ArchiveTaskStatus{State: confluence.ArchiveTaskStateSucceeded}, nil
}

func (f *fakeFolderPushRemote) DeletePage(_ context.Context, pageID string, opts confluence.PageDeleteOptions) error {
	return nil
}

func (f *fakeFolderPushRemote) UploadAttachment(_ context.Context, input confluence.AttachmentUploadInput) (confluence.Attachment, error) {
	return confluence.Attachment{}, nil
}

func (f *fakeFolderPushRemote) DeleteAttachment(_ context.Context, attachmentID string, pageID string) error {
	return nil
}

func (f *fakeFolderPushRemote) CreateFolder(_ context.Context, input confluence.FolderCreateInput) (confluence.Folder, error) {
	id := "folder-new"
	if len(f.folders) > 0 {
		id = f.folders[len(f.folders)-1].ID + "-new"
	}
	created := confluence.Folder{
		ID:         id,
		SpaceID:    input.SpaceID,
		Title:      input.Title,
		ParentID:   input.ParentID,
		ParentType: input.ParentType,
	}
	f.foldersByID[id] = created
	f.folders = append(f.folders, created)
	return created, nil
}

func (f *fakeFolderPushRemote) ListFolders(_ context.Context, _ confluence.FolderListOptions) (confluence.FolderListResult, error) {
	return confluence.FolderListResult{Folders: append([]confluence.Folder(nil), f.folders...)}, nil
}

func (f *fakeFolderPushRemote) DeleteFolder(_ context.Context, _ string) error {
	return nil
}

func (f *fakeFolderPushRemote) MovePage(_ context.Context, pageID string, targetID string) error {
	f.moves = append(f.moves, fakePageMove{pageID: pageID, targetID: targetID})
	return nil
}

// rollbackPushRemote is a configurable fake used for rollback and integration tests.
type rollbackPushRemote struct {
	space                    confluence.Space
	pages                    []confluence.Page
	pagesByID                map[string]confluence.Page
	contentStatuses          map[string]string
	labelsByPage             map[string][]string
	nextPageID               int
	nextAttachmentID         int
	createPageCalls          int
	updatePageCalls          int
	uploadAttachmentCalls    int
	archiveTaskCalls         []string
	deletePageCalls          []string
	deletePageOpts           []confluence.PageDeleteOptions
	deleteAttachmentCalls    []string
	setContentStatusCalls    []string
	setContentStatusArgs     []contentStatusCall
	deleteContentStatusCalls []string
	deleteContentStatusArgs  []contentStatusCall
	addLabelsCalls           []string
	removeLabelCalls         []string
	archiveTaskStatus        confluence.ArchiveTaskStatus
	archivePagesErr          error
	archiveTaskWaitErr       error
	failUpdate               bool
	failAddLabels            bool
	failSetContentStatus     bool
	failDeleteContentStatus  bool
	rejectParentID           string
	rejectParentErr          error
	updateInputsByPageID     map[string]confluence.PageUpsertInput
	updateCallInputs         []confluence.PageUpsertInput
}

func newRollbackPushRemote() *rollbackPushRemote {
	return &rollbackPushRemote{
		space:                confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pagesByID:            map[string]confluence.Page{},
		contentStatuses:      map[string]string{},
		labelsByPage:         map[string][]string{},
		updateInputsByPageID: map[string]confluence.PageUpsertInput{},
		nextPageID:           1,
		nextAttachmentID:     1,
		archiveTaskStatus: confluence.ArchiveTaskStatus{
			State: confluence.ArchiveTaskStateSucceeded,
		},
	}
}

func (f *rollbackPushRemote) GetSpace(_ context.Context, spaceKey string) (confluence.Space, error) {
	return f.space, nil
}

func (f *rollbackPushRemote) ListPages(_ context.Context, _ confluence.PageListOptions) (confluence.PageListResult, error) {
	return confluence.PageListResult{Pages: append([]confluence.Page(nil), f.pages...)}, nil
}

func (f *rollbackPushRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
	page, ok := f.pagesByID[pageID]
	if !ok {
		return confluence.Page{}, confluence.ErrNotFound
	}
	return page, nil
}

func (f *rollbackPushRemote) GetContentStatus(_ context.Context, pageID string, _ string) (string, error) {
	return f.contentStatuses[pageID], nil
}

func (f *rollbackPushRemote) SetContentStatus(_ context.Context, pageID string, pageStatus string, statusName string) error {
	f.setContentStatusCalls = append(f.setContentStatusCalls, pageID)
	f.setContentStatusArgs = append(f.setContentStatusArgs, contentStatusCall{
		PageID:     pageID,
		PageStatus: strings.TrimSpace(pageStatus),
		StatusName: strings.TrimSpace(statusName),
	})
	if f.failSetContentStatus {
		return errors.New("simulated set content status failure")
	}
	f.contentStatuses[pageID] = strings.TrimSpace(statusName)
	return nil
}

func (f *rollbackPushRemote) DeleteContentStatus(_ context.Context, pageID string, pageStatus string) error {
	f.deleteContentStatusCalls = append(f.deleteContentStatusCalls, pageID)
	f.deleteContentStatusArgs = append(f.deleteContentStatusArgs, contentStatusCall{
		PageID:     pageID,
		PageStatus: strings.TrimSpace(pageStatus),
	})
	if f.failDeleteContentStatus {
		return errors.New("simulated delete content status failure")
	}
	f.contentStatuses[pageID] = ""
	return nil
}

func (f *rollbackPushRemote) GetLabels(_ context.Context, pageID string) ([]string, error) {
	labels := append([]string(nil), f.labelsByPage[pageID]...)
	return labels, nil
}

func (f *rollbackPushRemote) AddLabels(_ context.Context, pageID string, labels []string) error {
	f.addLabelsCalls = append(f.addLabelsCalls, pageID)
	if f.failAddLabels {
		return errors.New("simulated add labels failure")
	}
	f.labelsByPage[pageID] = append(f.labelsByPage[pageID], labels...)
	return nil
}

func (f *rollbackPushRemote) RemoveLabel(_ context.Context, pageID string, labelName string) error {
	f.removeLabelCalls = append(f.removeLabelCalls, pageID)
	filtered := make([]string, 0)
	for _, existing := range f.labelsByPage[pageID] {
		if existing == labelName {
			continue
		}
		filtered = append(filtered, existing)
	}
	f.labelsByPage[pageID] = filtered
	return nil
}

func (f *rollbackPushRemote) CreatePage(_ context.Context, input confluence.PageUpsertInput) (confluence.Page, error) {
	f.createPageCalls++
	id := fmt.Sprintf("new-page-%d", f.nextPageID)
	f.nextPageID++
	page := confluence.Page{
		ID:           id,
		SpaceID:      input.SpaceID,
		ParentPageID: input.ParentPageID,
		Title:        input.Title,
		Status:       input.Status,
		Version:      1,
		WebURL:       "https://example.atlassian.net/wiki/pages/" + id,
		BodyADF:      []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	f.pagesByID[id] = page
	f.pages = append(f.pages, page)
	return page, nil
}

func (f *rollbackPushRemote) UpdatePage(_ context.Context, pageID string, input confluence.PageUpsertInput) (confluence.Page, error) {
	f.updatePageCalls++
	f.updateInputsByPageID[pageID] = input
	f.updateCallInputs = append(f.updateCallInputs, input)
	if f.failUpdate {
		return confluence.Page{}, errors.New("simulated update failure")
	}
	if strings.TrimSpace(f.rejectParentID) != "" && strings.TrimSpace(input.ParentPageID) == strings.TrimSpace(f.rejectParentID) {
		err := f.rejectParentErr
		if err == nil {
			err = confluence.ErrNotFound
		}
		return confluence.Page{}, err
	}
	updated := confluence.Page{
		ID:           pageID,
		SpaceID:      input.SpaceID,
		ParentPageID: input.ParentPageID,
		Title:        input.Title,
		Status:       input.Status,
		Version:      input.Version,
		WebURL:       "https://example.atlassian.net/wiki/pages/" + pageID,
		BodyADF:      input.BodyADF,
	}
	f.pagesByID[pageID] = updated
	for i := range f.pages {
		if f.pages[i].ID == pageID {
			f.pages[i] = updated
		}
	}
	return updated, nil
}

func (f *rollbackPushRemote) ArchivePages(_ context.Context, _ []string) (confluence.ArchiveResult, error) {
	if f.archivePagesErr != nil {
		return confluence.ArchiveResult{}, f.archivePagesErr
	}
	return confluence.ArchiveResult{TaskID: "task-1"}, nil
}

func (f *rollbackPushRemote) WaitForArchiveTask(_ context.Context, taskID string, _ confluence.ArchiveTaskWaitOptions) (confluence.ArchiveTaskStatus, error) {
	f.archiveTaskCalls = append(f.archiveTaskCalls, taskID)
	if f.archiveTaskWaitErr != nil {
		status := f.archiveTaskStatus
		if strings.TrimSpace(status.TaskID) == "" {
			status.TaskID = taskID
		}
		return status, f.archiveTaskWaitErr
	}
	status := f.archiveTaskStatus
	if strings.TrimSpace(status.TaskID) == "" {
		status.TaskID = taskID
	}
	if status.State == "" {
		status.State = confluence.ArchiveTaskStateSucceeded
	}
	return status, nil
}

func (f *rollbackPushRemote) DeletePage(_ context.Context, pageID string, opts confluence.PageDeleteOptions) error {
	f.deletePageCalls = append(f.deletePageCalls, pageID)
	f.deletePageOpts = append(f.deletePageOpts, opts)
	delete(f.pagesByID, pageID)
	filtered := make([]confluence.Page, 0, len(f.pages))
	for _, page := range f.pages {
		if page.ID == pageID {
			continue
		}
		filtered = append(filtered, page)
	}
	f.pages = filtered
	return nil
}

func (f *rollbackPushRemote) UploadAttachment(_ context.Context, input confluence.AttachmentUploadInput) (confluence.Attachment, error) {
	f.uploadAttachmentCalls++
	id := fmt.Sprintf("att-%d", f.nextAttachmentID)
	f.nextAttachmentID++
	return confluence.Attachment{ID: id, PageID: input.PageID, Filename: input.Filename}, nil
}

func (f *rollbackPushRemote) DeleteAttachment(_ context.Context, attachmentID string, _ string) error {
	f.deleteAttachmentCalls = append(f.deleteAttachmentCalls, attachmentID)
	return nil
}

func (f *rollbackPushRemote) CreateFolder(_ context.Context, input confluence.FolderCreateInput) (confluence.Folder, error) {
	return confluence.Folder{ID: "folder-1", SpaceID: input.SpaceID, Title: input.Title, ParentID: input.ParentID}, nil
}

func (f *rollbackPushRemote) ListFolders(_ context.Context, _ confluence.FolderListOptions) (confluence.FolderListResult, error) {
	return confluence.FolderListResult{}, nil
}

func (f *rollbackPushRemote) DeleteFolder(_ context.Context, _ string) error {
	return nil
}

func (f *rollbackPushRemote) MovePage(_ context.Context, pageID string, targetID string) error {
	return nil
}
