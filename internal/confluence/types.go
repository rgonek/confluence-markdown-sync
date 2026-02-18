package confluence

import (
	"context"
	"encoding/json"
	"time"
)

// Service describes the Confluence operations required by sync orchestration.
type Service interface {
	ListSpaces(ctx context.Context, opts SpaceListOptions) (SpaceListResult, error)
	GetSpace(ctx context.Context, spaceKey string) (Space, error)
	ListPages(ctx context.Context, opts PageListOptions) (PageListResult, error)
	GetPage(ctx context.Context, pageID string) (Page, error)
	DownloadAttachment(ctx context.Context, attachmentID string) ([]byte, error)
	UploadAttachment(ctx context.Context, input AttachmentUploadInput) (Attachment, error)
	DeleteAttachment(ctx context.Context, attachmentID string) error
	CreatePage(ctx context.Context, input PageUpsertInput) (Page, error)
	UpdatePage(ctx context.Context, pageID string, input PageUpsertInput) (Page, error)
	ListChanges(ctx context.Context, opts ChangeListOptions) (ChangeListResult, error)
	ArchivePages(ctx context.Context, pageIDs []string) (ArchiveResult, error)
	DeletePage(ctx context.Context, pageID string, hardDelete bool) error
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
	ID           string
	SpaceID      string
	Title        string
	Status       string
	ParentPageID string
	Version      int
	LastModified time.Time
	WebURL       string
	BodyADF      json.RawMessage
}

// PageListOptions configures page listing.
type PageListOptions struct {
	SpaceID  string
	SpaceKey string
	Status   string
	Limit    int
	Cursor   string
}

// PageListResult is a page of page list results.
type PageListResult struct {
	Pages      []Page
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

// Attachment represents a Confluence attachment.
type Attachment struct {
	ID        string
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
