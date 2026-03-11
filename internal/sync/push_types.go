package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

const pushPageBatchSize = 100

// PushRemote defines remote operations required by push orchestration.
type PushRemote interface {
	GetSpace(ctx context.Context, spaceKey string) (confluence.Space, error)
	ListPages(ctx context.Context, opts confluence.PageListOptions) (confluence.PageListResult, error)
	ListContentStates(ctx context.Context) ([]confluence.ContentState, error)
	ListSpaceContentStates(ctx context.Context, spaceKey string) ([]confluence.ContentState, error)
	GetAvailableContentStates(ctx context.Context, pageID string) ([]confluence.ContentState, error)
	GetPage(ctx context.Context, pageID string) (confluence.Page, error)
	GetContentStatus(ctx context.Context, pageID string, pageStatus string) (string, error)
	SetContentStatus(ctx context.Context, pageID string, pageStatus string, state confluence.ContentState) error
	DeleteContentStatus(ctx context.Context, pageID string, pageStatus string) error
	GetLabels(ctx context.Context, pageID string) ([]string, error)
	AddLabels(ctx context.Context, pageID string, labels []string) error
	RemoveLabel(ctx context.Context, pageID string, labelName string) error
	CreatePage(ctx context.Context, input confluence.PageUpsertInput) (confluence.Page, error)
	UpdatePage(ctx context.Context, pageID string, input confluence.PageUpsertInput) (confluence.Page, error)
	ArchivePages(ctx context.Context, pageIDs []string) (confluence.ArchiveResult, error)
	WaitForArchiveTask(ctx context.Context, taskID string, opts confluence.ArchiveTaskWaitOptions) (confluence.ArchiveTaskStatus, error)
	DeletePage(ctx context.Context, pageID string, opts confluence.PageDeleteOptions) error
	ListAttachments(ctx context.Context, pageID string) ([]confluence.Attachment, error)
	GetAttachment(ctx context.Context, attachmentID string) (confluence.Attachment, error)
	UploadAttachment(ctx context.Context, input confluence.AttachmentUploadInput) (confluence.Attachment, error)
	DeleteAttachment(ctx context.Context, attachmentID string, pageID string) error
	CreateFolder(ctx context.Context, input confluence.FolderCreateInput) (confluence.Folder, error)
	ListFolders(ctx context.Context, opts confluence.FolderListOptions) (confluence.FolderListResult, error)
	MovePage(ctx context.Context, pageID string, targetID string) error
}

// PushConflictPolicy controls remote-ahead conflict behavior.
type PushConflictPolicy string

const (
	PushConflictPolicyPullMerge PushConflictPolicy = "pull-merge"
	PushConflictPolicyForce     PushConflictPolicy = "force"
	PushConflictPolicyCancel    PushConflictPolicy = "cancel"
)

// PushChangeType is the git-derived file change type for push planning.
type PushChangeType string

const (
	PushChangeAdd      PushChangeType = "A"
	PushChangeModify   PushChangeType = "M"
	PushChangeDelete   PushChangeType = "D"
	PushChangeTypeNone PushChangeType = ""
)

// PushFileChange captures one changed markdown path inside a space scope.
type PushFileChange struct {
	Type PushChangeType
	Path string
}

// PushOptions controls push orchestration.
type PushOptions struct {
	SpaceKey            string
	SpaceDir            string
	Domain              string
	State               fs.SpaceState
	GlobalPageIndex     GlobalPageIndex
	Changes             []PushFileChange
	RemoteFolderByTitle map[string]confluence.Folder
	ConflictPolicy      PushConflictPolicy
	HardDelete          bool
	KeepOrphanAssets    bool
	DryRun              bool
	ArchiveTimeout      time.Duration
	ArchivePollInterval time.Duration
	Progress            Progress
	folderListTracker   *folderListFallbackTracker
	folderMode          tenantFolderMode
	contentStatusMode   tenantContentStatusMode
	contentStateCatalog pushContentStateCatalog
}

// PushCommitPlan describes local paths and metadata for one push commit.
type PushCommitPlan struct {
	Path        string
	Deleted     bool
	PageID      string
	PageTitle   string
	Version     int
	SpaceKey    string
	URL         string
	StagedPaths []string
}

// PushDiagnostic captures non-fatal push diagnostics.
type PushDiagnostic struct {
	Path    string
	Code    string
	Message string
}

// PushResult captures outputs of push orchestration.
type PushResult struct {
	State       fs.SpaceState
	Commits     []PushCommitPlan
	Diagnostics []PushDiagnostic
}

type pushMetadataSnapshot struct {
	ContentStatus      string
	PageStatus         string
	TrackContentStatus bool
	Labels             []string
}

type pushContentStateCatalog struct {
	space            map[string]confluence.ContentState
	global           map[string]confluence.ContentState
	perPage          map[string]map[string]confluence.ContentState
	spaceAvailable   bool
	globalAvailable  bool
	perPageAvailable map[string]bool
}

type pushContentSnapshot struct {
	SpaceID      string
	Title        string
	ParentPageID string
	Status       string
	BodyADF      json.RawMessage
}

type rollbackAttachment struct {
	PageID       string
	AttachmentID string
	Path         string
}

type pushRollbackTracker struct {
	relPath            string
	contentStatusMode  tenantContentStatusMode
	createdPageID      string
	createdPageStatus  string
	uploadedAssets     []rollbackAttachment
	contentPageID      string
	contentSnapshot    *pushContentSnapshot
	contentRestoreReq  bool
	metadataPageID     string
	metadataSnapshot   *pushMetadataSnapshot
	metadataRestoreReq bool
	diagnostics        *[]PushDiagnostic
}

// PushConflictError indicates a remote-ahead page conflict.
type PushConflictError struct {
	Path          string
	PageID        string
	LocalVersion  int
	RemoteVersion int
	Policy        PushConflictPolicy
}

func (e *PushConflictError) Error() string {
	return fmt.Sprintf(
		"remote version conflict for %s (page %s): local=%d remote=%d policy=%s",
		e.Path,
		e.PageID,
		e.LocalVersion,
		e.RemoteVersion,
		e.Policy,
	)
}

// FolderPageFallbackRequiredError reports that continuing a push would require
// rewriting a local directory-backed folder into a page-with-subpages node.
type FolderPageFallbackRequiredError struct {
	Path    string
	Reason  string
	Message string
	Cause   error
}

func (e *FolderPageFallbackRequiredError) Error() string {
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return fmt.Sprintf("folder %s requires explicit page fallback: %v", e.Path, e.Cause)
}

func (e *FolderPageFallbackRequiredError) Unwrap() error {
	return e.Cause
}
