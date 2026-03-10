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

func (f *fakeFolderPushRemote) ListContentStates(_ context.Context) ([]confluence.ContentState, error) {
	return nil, nil
}

func (f *fakeFolderPushRemote) ListSpaceContentStates(_ context.Context, _ string) ([]confluence.ContentState, error) {
	return nil, nil
}

func (f *fakeFolderPushRemote) GetAvailableContentStates(_ context.Context, _ string) ([]confluence.ContentState, error) {
	return nil, nil
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

func (f *fakeFolderPushRemote) SetContentStatus(_ context.Context, pageID string, _ string, status confluence.ContentState) error {
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

func (f *fakeFolderPushRemote) ListAttachments(_ context.Context, pageID string) ([]confluence.Attachment, error) {
	return nil, nil
}

func (f *fakeFolderPushRemote) GetAttachment(_ context.Context, attachmentID string) (confluence.Attachment, error) {
	return confluence.Attachment{ID: strings.TrimSpace(attachmentID)}, nil
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
	space                     confluence.Space
	pages                     []confluence.Page
	pagesByID                 map[string]confluence.Page
	contentStatuses           map[string]string
	labelsByPage              map[string][]string
	folders                   []confluence.Folder
	attachmentsByPage         map[string][]confluence.Attachment
	nextPageID                int
	nextAttachmentID          int
	createPageCalls           int
	createFolderCalls         int
	updatePageCalls           int
	uploadAttachmentCalls     int
	archiveTaskCalls          []string
	deletePageCalls           []string
	deletePageOpts            []confluence.PageDeleteOptions
	deleteAttachmentCalls     []string
	getContentStatusCalls     []string
	setContentStatusCalls     []string
	setContentStatusArgs      []contentStatusCall
	deleteContentStatusCalls  []string
	deleteContentStatusArgs   []contentStatusCall
	addLabelsCalls            []string
	removeLabelCalls          []string
	archiveTaskStatus         confluence.ArchiveTaskStatus
	archivePagesErr           error
	archiveTaskWaitErr        error
	listFoldersErr            error
	createFolderErr           error
	getContentStatusErr       error
	failUpdate                bool
	failCreatePageErr         error
	failAddLabels             bool
	failSetContentStatus      bool
	failDeleteContentStatus   bool
	contentStatusVersionBump  bool
	rejectParentID            string
	rejectParentErr           error
	updateInputsByPageID      map[string]confluence.PageUpsertInput
	updateCallInputs          []confluence.PageUpsertInput
	contentStates             []confluence.ContentState
	spaceContentStates        []confluence.ContentState
	availableStatesByPage     map[string][]confluence.ContentState
	waitForArchiveTaskHook    func(*rollbackPushRemote, string)
	listContentStatesErr      error
	listSpaceContentStatesErr error
	getAvailableStatesErr     error
}

func newRollbackPushRemote() *rollbackPushRemote {
	return &rollbackPushRemote{
		space:                 confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pagesByID:             map[string]confluence.Page{},
		contentStatuses:       map[string]string{},
		labelsByPage:          map[string][]string{},
		attachmentsByPage:     map[string][]confluence.Attachment{},
		updateInputsByPageID:  map[string]confluence.PageUpsertInput{},
		availableStatesByPage: map[string][]confluence.ContentState{},
		nextPageID:            1,
		nextAttachmentID:      1,
		contentStates: []confluence.ContentState{
			{ID: 80, Name: "Ready to review", Color: "FFAB00"},
			{ID: 81, Name: "In progress", Color: "0052CC"},
			{ID: 82, Name: "Ready", Color: "36B37E"},
			{ID: 83, Name: "In review", Color: "6554C0"},
		},
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

func (f *rollbackPushRemote) ListContentStates(_ context.Context) ([]confluence.ContentState, error) {
	if f.listContentStatesErr != nil {
		return nil, f.listContentStatesErr
	}
	return append([]confluence.ContentState(nil), f.contentStates...), nil
}

func (f *rollbackPushRemote) ListSpaceContentStates(_ context.Context, _ string) ([]confluence.ContentState, error) {
	if f.listSpaceContentStatesErr != nil {
		return nil, f.listSpaceContentStatesErr
	}
	if len(f.spaceContentStates) == 0 {
		return append([]confluence.ContentState(nil), f.contentStates...), nil
	}
	return append([]confluence.ContentState(nil), f.spaceContentStates...), nil
}

func (f *rollbackPushRemote) GetAvailableContentStates(_ context.Context, pageID string) ([]confluence.ContentState, error) {
	if f.getAvailableStatesErr != nil {
		return nil, f.getAvailableStatesErr
	}
	if states, ok := f.availableStatesByPage[strings.TrimSpace(pageID)]; ok {
		return append([]confluence.ContentState(nil), states...), nil
	}
	return append([]confluence.ContentState(nil), f.contentStates...), nil
}

func (f *rollbackPushRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
	page, ok := f.pagesByID[pageID]
	if !ok {
		return confluence.Page{}, confluence.ErrNotFound
	}
	return page, nil
}

func (f *rollbackPushRemote) GetContentStatus(_ context.Context, pageID string, _ string) (string, error) {
	f.getContentStatusCalls = append(f.getContentStatusCalls, pageID)
	if f.getContentStatusErr != nil {
		return "", f.getContentStatusErr
	}
	return f.contentStatuses[pageID], nil
}

func (f *rollbackPushRemote) SetContentStatus(_ context.Context, pageID string, pageStatus string, status confluence.ContentState) error {
	f.setContentStatusCalls = append(f.setContentStatusCalls, pageID)
	statusName := strings.TrimSpace(status.Name)
	f.setContentStatusArgs = append(f.setContentStatusArgs, contentStatusCall{
		PageID:     pageID,
		PageStatus: strings.TrimSpace(pageStatus),
		StatusName: statusName,
	})
	if f.failSetContentStatus {
		return errors.New("simulated set content status failure")
	}
	f.contentStatuses[pageID] = strings.TrimSpace(statusName)
	if f.contentStatusVersionBump {
		f.bumpPageVersion(pageID)
	}
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
	if f.contentStatusVersionBump {
		f.bumpPageVersion(pageID)
	}
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
	if f.failCreatePageErr != nil {
		return confluence.Page{}, f.failCreatePageErr
	}
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
	if f.waitForArchiveTaskHook != nil {
		f.waitForArchiveTaskHook(f, taskID)
	}
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
	delete(f.attachmentsByPage, pageID)
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

func (f *rollbackPushRemote) ListAttachments(_ context.Context, pageID string) ([]confluence.Attachment, error) {
	return append([]confluence.Attachment(nil), f.attachmentsByPage[pageID]...), nil
}

func (f *rollbackPushRemote) GetAttachment(_ context.Context, attachmentID string) (confluence.Attachment, error) {
	attachmentID = strings.TrimSpace(attachmentID)
	for _, attachments := range f.attachmentsByPage {
		for _, attachment := range attachments {
			if strings.TrimSpace(attachment.ID) == attachmentID {
				return attachment, nil
			}
		}
	}
	return confluence.Attachment{}, confluence.ErrNotFound
}

func (f *rollbackPushRemote) UploadAttachment(_ context.Context, input confluence.AttachmentUploadInput) (confluence.Attachment, error) {
	f.uploadAttachmentCalls++
	id := fmt.Sprintf("att-%d", f.nextAttachmentID)
	fileID := fmt.Sprintf("file-%d", f.nextAttachmentID)
	f.nextAttachmentID++
	attachment := confluence.Attachment{ID: id, FileID: fileID, PageID: input.PageID, Filename: input.Filename}
	f.attachmentsByPage[input.PageID] = append(f.attachmentsByPage[input.PageID], attachment)
	return attachment, nil
}

func (f *rollbackPushRemote) DeleteAttachment(_ context.Context, attachmentID string, pageID string) error {
	f.deleteAttachmentCalls = append(f.deleteAttachmentCalls, attachmentID)
	if strings.TrimSpace(pageID) != "" {
		filtered := make([]confluence.Attachment, 0, len(f.attachmentsByPage[pageID]))
		for _, attachment := range f.attachmentsByPage[pageID] {
			if strings.TrimSpace(attachment.ID) == strings.TrimSpace(attachmentID) {
				continue
			}
			filtered = append(filtered, attachment)
		}
		f.attachmentsByPage[pageID] = filtered
	}
	return nil
}

func (f *rollbackPushRemote) CreateFolder(_ context.Context, input confluence.FolderCreateInput) (confluence.Folder, error) {
	f.createFolderCalls++
	if f.createFolderErr != nil {
		return confluence.Folder{}, f.createFolderErr
	}
	folder := confluence.Folder{ID: fmt.Sprintf("folder-%d", f.createFolderCalls), SpaceID: input.SpaceID, Title: input.Title, ParentID: input.ParentID, ParentType: input.ParentType}
	f.folders = append(f.folders, folder)
	return folder, nil
}

func (f *rollbackPushRemote) ListFolders(_ context.Context, _ confluence.FolderListOptions) (confluence.FolderListResult, error) {
	if f.listFoldersErr != nil {
		return confluence.FolderListResult{}, f.listFoldersErr
	}
	return confluence.FolderListResult{Folders: append([]confluence.Folder(nil), f.folders...)}, nil
}

func (f *rollbackPushRemote) DeleteFolder(_ context.Context, _ string) error {
	return nil
}

func (f *rollbackPushRemote) MovePage(_ context.Context, pageID string, targetID string) error {
	return nil
}

func (f *rollbackPushRemote) bumpPageVersion(pageID string) {
	page, ok := f.pagesByID[strings.TrimSpace(pageID)]
	if !ok {
		return
	}
	page.Version++
	f.pagesByID[pageID] = page
	for i := range f.pages {
		if strings.TrimSpace(f.pages[i].ID) == strings.TrimSpace(pageID) {
			f.pages[i] = page
		}
	}
}
