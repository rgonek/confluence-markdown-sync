package confluence

import (
	"context"
	"encoding/json"
	"io"
	"time"
)

// Compile-time assertion that *Client implements Service.
var _ Service = (*Client)(nil)

// User is a Confluence user.
type User struct {
	AccountID   string
	DisplayName string
	Email       string
}

// Service describes the Confluence operations required by sync orchestration.
type Service interface {
	GetUser(ctx context.Context, accountID string) (User, error)
	ListSpaces(ctx context.Context, opts SpaceListOptions) (SpaceListResult, error)
	GetSpace(ctx context.Context, spaceKey string) (Space, error)
	ListPages(ctx context.Context, opts PageListOptions) (PageListResult, error)
	GetFolder(ctx context.Context, folderID string) (Folder, error)
	GetPage(ctx context.Context, pageID string) (Page, error)
	ListAttachments(ctx context.Context, pageID string) ([]Attachment, error)
	GetAttachment(ctx context.Context, attachmentID string) (Attachment, error)
	DownloadAttachment(ctx context.Context, attachmentID string, pageID string, out io.Writer) error
	UploadAttachment(ctx context.Context, input AttachmentUploadInput) (Attachment, error)

	DeleteAttachment(ctx context.Context, attachmentID string, pageID string) error
	CreatePage(ctx context.Context, input PageUpsertInput) (Page, error)
	UpdatePage(ctx context.Context, pageID string, input PageUpsertInput) (Page, error)
	ListChanges(ctx context.Context, opts ChangeListOptions) (ChangeListResult, error)
	ArchivePages(ctx context.Context, pageIDs []string) (ArchiveResult, error)
	WaitForArchiveTask(ctx context.Context, taskID string, opts ArchiveTaskWaitOptions) (ArchiveTaskStatus, error)
	DeletePage(ctx context.Context, pageID string, opts PageDeleteOptions) error
	CreateFolder(ctx context.Context, input FolderCreateInput) (Folder, error)
	ListFolders(ctx context.Context, opts FolderListOptions) (FolderListResult, error)
	DeleteFolder(ctx context.Context, folderID string) error
	MovePage(ctx context.Context, pageID string, targetID string) error
}

// Space is a Confluence space.
type Space struct {
	ID   string
	Key  string
	Name string
	Type string
}

// SpaceListOptions configures space listing.
type SpaceListOptions struct {
	Keys   []string
	Limit  int
	Cursor string
}

// SpaceListResult is a page of space list results.
type SpaceListResult struct {
	Spaces     []Space
	NextCursor string
}

// Page is a Confluence page.
type Page struct {
	ID                   string
	SpaceID              string
	Title                string
	Status               string // maps to draft vs current
	ContentStatus        string // maps to UI lozenge (e.g. "Ready to review")
	Labels               []string
	ParentPageID         string
	ParentType           string
	Version              int
	AuthorID             string
	CreatedAt            time.Time
	LastModifiedAuthorID string
	LastModified         time.Time
	WebURL               string
	BodyADF              json.RawMessage
}

// PageListOptions configures page listing.
type PageListOptions struct {
	SpaceID  string
	SpaceKey string
	Title    string
	Status   string
	Limit    int
	Cursor   string
}

// PageListResult is a page of page list results.
type PageListResult struct {
	Pages      []Page
	NextCursor string
}

// Folder is a Confluence folder node used in content hierarchy.
type Folder struct {
	ID         string
	SpaceID    string
	Title      string
	ParentID   string
	ParentType string
}

// FolderListOptions configures folder listing.
type FolderListOptions struct {
	SpaceID string
	Title   string
	Limit   int
	Cursor  string
}

// FolderListResult is a page of folder list results.
type FolderListResult struct {
	Folders    []Folder
	NextCursor string
}

// PageUpsertInput is used for create/update operations.
type PageUpsertInput struct {
	SpaceID      string
	ParentPageID string
	Title        string
	Status       string
	Version      int
	BodyADF      json.RawMessage
}

// PageDeleteOptions controls page deletion semantics for current vs draft content.
type PageDeleteOptions struct {
	Purge bool
	Draft bool
}

// Change captures a page change useful for incremental sync planning.
type Change struct {
	PageID       string
	SpaceKey     string
	Title        string
	Version      int
	LastModified time.Time
}

// ChangeListOptions configures incremental page change discovery.
type ChangeListOptions struct {
	SpaceKey string
	Since    time.Time
	Limit    int
	Start    int
}

// ChangeListResult is a page of change results.
type ChangeListResult struct {
	Changes   []Change
	NextStart int
	HasMore   bool
}

// ArchiveResult captures archive task metadata returned by Confluence.
type ArchiveResult struct {
	TaskID string
}

// ArchiveTaskState is the normalized lifecycle state of a Confluence long task.
type ArchiveTaskState string

const (
	ArchiveTaskStateInProgress ArchiveTaskState = "in_progress"
	ArchiveTaskStateSucceeded  ArchiveTaskState = "succeeded"
	ArchiveTaskStateFailed     ArchiveTaskState = "failed"
)

// ArchiveTaskStatus captures Confluence archive long-task progress and outcome.
type ArchiveTaskStatus struct {
	TaskID      string
	State       ArchiveTaskState
	RawStatus   string
	Message     string
	PercentDone int
}

// ArchiveTaskWaitOptions controls long-task polling behavior.
type ArchiveTaskWaitOptions struct {
	Timeout      time.Duration
	PollInterval time.Duration
}

// Attachment represents a Confluence attachment.
type Attachment struct {
	ID        string
	FileID    string
	PageID    string
	Filename  string
	MediaType string
	WebURL    string
}

// AttachmentUploadInput is used to upload an attachment to a page.
type AttachmentUploadInput struct {
	PageID      string
	Filename    string
	ContentType string
	Data        []byte
}

// FolderCreateInput is used to create a Confluence folder.
type FolderCreateInput struct {
	SpaceID    string
	ParentID   string // optional parent folder ID
	ParentType string // "space" or "folder" (defaults to "space" when ParentID is empty)
	Title      string
}
