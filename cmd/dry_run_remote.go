//nolint:errcheck // dry-run output writes are best-effort diagnostics only
package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

type dryRunPushRemote struct {
	inner          syncflow.PushRemote
	out            io.Writer
	domain         string
	emitOperations bool
}

func (d *dryRunPushRemote) GetSpace(ctx context.Context, spaceKey string) (confluence.Space, error) {
	return d.inner.GetSpace(ctx, spaceKey)
}

func (d *dryRunPushRemote) ListPages(ctx context.Context, opts confluence.PageListOptions) (confluence.PageListResult, error) {
	return d.inner.ListPages(ctx, opts)
}

func (d *dryRunPushRemote) ListContentStates(ctx context.Context) ([]confluence.ContentState, error) {
	return d.inner.ListContentStates(ctx)
}

func (d *dryRunPushRemote) ListSpaceContentStates(ctx context.Context, spaceKey string) ([]confluence.ContentState, error) {
	return d.inner.ListSpaceContentStates(ctx, spaceKey)
}

func (d *dryRunPushRemote) GetAvailableContentStates(ctx context.Context, pageID string) ([]confluence.ContentState, error) {
	return d.inner.GetAvailableContentStates(ctx, pageID)
}

func (d *dryRunPushRemote) GetPage(ctx context.Context, pageID string) (confluence.Page, error) {
	return d.inner.GetPage(ctx, pageID)
}

func (d *dryRunPushRemote) GetContentStatus(ctx context.Context, pageID string, pageStatus string) (string, error) {
	if strings.HasPrefix(pageID, "dry-run-") {
		return "", nil
	}
	return d.inner.GetContentStatus(ctx, pageID, pageStatus)
}

func (d *dryRunPushRemote) SetContentStatus(ctx context.Context, pageID string, pageStatus string, state confluence.ContentState) error {
	d.printf("[DRY-RUN] SET CONTENT STATUS (PUT %s/wiki/rest/api/content/%s/state?status=%s)\n", d.domain, pageID, pageStatus)
	d.printf("  Name: %s\n\n", state.Name)
	return nil
}

func (d *dryRunPushRemote) DeleteContentStatus(ctx context.Context, pageID string, pageStatus string) error {
	d.printf("[DRY-RUN] DELETE CONTENT STATUS (DELETE %s/wiki/rest/api/content/%s/state?status=%s)\n\n", d.domain, pageID, pageStatus)
	return nil
}

func (d *dryRunPushRemote) GetLabels(ctx context.Context, pageID string) ([]string, error) {
	if strings.HasPrefix(pageID, "dry-run-") {
		return nil, nil
	}
	return d.inner.GetLabels(ctx, pageID)
}

func (d *dryRunPushRemote) AddLabels(ctx context.Context, pageID string, labels []string) error {
	d.printf("[DRY-RUN] ADD LABELS (POST %s/wiki/rest/api/content/%s/label)\n", d.domain, pageID)
	d.printf("  Labels: %v\n\n", labels)
	return nil
}

func (d *dryRunPushRemote) RemoveLabel(ctx context.Context, pageID string, labelName string) error {
	d.printf("[DRY-RUN] REMOVE LABEL (DELETE %s/wiki/rest/api/content/%s/label?name=%s)\n\n", d.domain, pageID, labelName)
	return nil
}

func (d *dryRunPushRemote) CreatePage(ctx context.Context, input confluence.PageUpsertInput) (confluence.Page, error) {
	d.printf("[DRY-RUN] CREATE PAGE (POST %s/wiki/api/v2/pages)\n", d.domain)
	d.printf("  Title: %s\n", input.Title)
	if input.ParentPageID != "" {
		d.printf("  ParentPageID: %s\n", input.ParentPageID)
	}
	d.printf("  Status: %s\n", input.Status)
	d.printBodyPreview(ctx, input.BodyADF)
	d.println()

	return confluence.Page{
		ID:           "dry-run-new-page-id",
		SpaceID:      input.SpaceID,
		Title:        input.Title,
		Status:       input.Status,
		ParentPageID: input.ParentPageID,
		Version:      1,
		WebURL:       fmt.Sprintf("%s/spaces/%s/pages/%s", d.domain, input.SpaceID, "dry-run-new-page-id"),
	}, nil
}

func (d *dryRunPushRemote) UpdatePage(ctx context.Context, pageID string, input confluence.PageUpsertInput) (confluence.Page, error) {

	d.printf("[DRY-RUN] UPDATE PAGE (PUT %s/wiki/api/v2/pages/%s)\n", d.domain, pageID)
	d.printf("  Title: %s\n", input.Title)
	if input.ParentPageID != "" {
		d.printf("  ParentPageID: %s\n", input.ParentPageID)
	}
	d.printf("  Version: %d\n", input.Version)
	d.printBodyPreview(ctx, input.BodyADF)
	d.println()

	return confluence.Page{
		ID:           pageID,
		SpaceID:      input.SpaceID,
		Title:        input.Title,
		Status:       input.Status,
		ParentPageID: input.ParentPageID,
		Version:      input.Version,
		WebURL:       fmt.Sprintf("%s/spaces/%s/pages/%s", d.domain, input.SpaceID, pageID),
	}, nil
}

// printDryRunBodyPreview renders ADF as Markdown for human-readable dry-run output.
// Falls back to indented JSON if conversion fails.
func printDryRunBodyPreview(ctx context.Context, out io.Writer, adfJSON []byte) {
	res, err := converter.Forward(ctx, adfJSON, converter.ForwardConfig{}, "")
	if err == nil {
		body := strings.TrimSpace(res.Markdown)
		if body == "" {
			fmt.Fprintf(out, "  Body (Markdown preview): (empty)\n")
		} else {
			fmt.Fprintf(out, "  Body (Markdown preview):\n")
			for _, line := range strings.Split(strings.TrimRight(res.Markdown, "\n"), "\n") {
				fmt.Fprintf(out, "  | %s\n", line)
			}
		}
		return
	}

	// Fallback: pretty-print the raw ADF JSON
	var formattedADF bytes.Buffer
	if err := json.Indent(&formattedADF, adfJSON, "  ", "  "); err == nil {
		fmt.Fprintf(out, "  BodyADF:\n  %s\n", formattedADF.String())
	} else {
		fmt.Fprintf(out, "  BodyADF: %s\n", string(adfJSON))
	}
}

func (d *dryRunPushRemote) ArchivePages(ctx context.Context, pageIDs []string) (confluence.ArchiveResult, error) {
	d.printf("[DRY-RUN] ARCHIVE PAGES (POST %s/wiki/rest/api/content/archive)\n", d.domain)
	for _, id := range pageIDs {
		d.printf("  PageID: %s\n", id)
	}
	d.println()
	return confluence.ArchiveResult{TaskID: "dry-run-task-id"}, nil
}

func (d *dryRunPushRemote) WaitForArchiveTask(ctx context.Context, taskID string, opts confluence.ArchiveTaskWaitOptions) (confluence.ArchiveTaskStatus, error) {
	d.printf("[DRY-RUN] WAIT ARCHIVE TASK (GET %s/wiki/rest/api/longtask/%s)\n", d.domain, taskID)
	if opts.Timeout > 0 {
		d.printf("  Timeout: %s\n", opts.Timeout)
	}
	if opts.PollInterval > 0 {
		d.printf("  PollInterval: %s\n", opts.PollInterval)
	}
	d.println()
	return confluence.ArchiveTaskStatus{TaskID: taskID, State: confluence.ArchiveTaskStateSucceeded, RawStatus: "DRY_RUN"}, nil
}

func (d *dryRunPushRemote) DeletePage(ctx context.Context, pageID string, opts confluence.PageDeleteOptions) error {
	query := ""
	switch {
	case opts.Draft:
		query = "?draft=true"
	case opts.Purge:
		query = "?purge=true"
	}
	d.printf("[DRY-RUN] DELETE PAGE (DELETE %s/wiki/api/v2/pages/%s%s)\n\n", d.domain, pageID, query)
	return nil
}

func (d *dryRunPushRemote) ListAttachments(ctx context.Context, pageID string) ([]confluence.Attachment, error) {
	if strings.HasPrefix(pageID, "dry-run-") {
		return nil, nil
	}
	return d.inner.ListAttachments(ctx, pageID)
}

func (d *dryRunPushRemote) GetAttachment(ctx context.Context, attachmentID string) (confluence.Attachment, error) {
	if strings.HasPrefix(attachmentID, "dry-run-") {
		return confluence.Attachment{ID: attachmentID, FileID: attachmentID}, nil
	}
	return d.inner.GetAttachment(ctx, attachmentID)
}

func (d *dryRunPushRemote) UploadAttachment(ctx context.Context, input confluence.AttachmentUploadInput) (confluence.Attachment, error) {
	d.printf("[DRY-RUN] UPLOAD ATTACHMENT (POST %s/wiki/rest/api/content/%s/child/attachment)\n", d.domain, input.PageID)
	d.printf("  Filename: %s\n", input.Filename)
	d.printf("  ContentType: %s\n", input.ContentType)
	d.printf("  Size: %d bytes\n\n", len(input.Data))

	return confluence.Attachment{
		ID:        "dry-run-attachment-id-" + input.Filename,
		FileID:    "dry-run-file-id-" + input.Filename,
		PageID:    input.PageID,
		Filename:  input.Filename,
		MediaType: input.ContentType,
		WebURL:    fmt.Sprintf("%s/download/attachments/%s/%s", d.domain, input.PageID, input.Filename),
	}, nil
}

func (d *dryRunPushRemote) DeleteAttachment(ctx context.Context, attachmentID string, pageID string) error {
	d.printf("[DRY-RUN] DELETE ATTACHMENT (DELETE %s/wiki/api/v2/attachments/%s, page %s)\n\n", d.domain, attachmentID, pageID)
	return nil
}

func (d *dryRunPushRemote) CreateFolder(ctx context.Context, input confluence.FolderCreateInput) (confluence.Folder, error) {
	d.printf("[DRY-RUN] CREATE FOLDER (POST %s/wiki/api/v2/folders)\n", d.domain)
	d.printf("  Title: %s\n", input.Title)
	d.printf("  SpaceID: %s\n", input.SpaceID)
	if input.ParentID != "" {
		d.printf("  ParentID: %s\n", input.ParentID)
		d.printf("  ParentType: %s\n", input.ParentType)
	}
	d.println()

	return confluence.Folder{
		ID:         "dry-run-folder-id",
		SpaceID:    input.SpaceID,
		Title:      input.Title,
		ParentID:   input.ParentID,
		ParentType: input.ParentType,
	}, nil
}

func (d *dryRunPushRemote) ListFolders(ctx context.Context, opts confluence.FolderListOptions) (confluence.FolderListResult, error) {
	return d.inner.ListFolders(ctx, opts)
}

func (d *dryRunPushRemote) MovePage(ctx context.Context, pageID string, targetID string) error {
	d.printf("[DRY-RUN] MOVE PAGE (PUT %s/wiki/rest/api/content/%s/move/append/%s)\n\n", d.domain, pageID, targetID)
	return nil
}

func (d *dryRunPushRemote) Close() error {
	closeRemoteIfPossible(d.inner)
	return nil
}

func (d *dryRunPushRemote) printf(format string, args ...any) {
	if !d.emitOperations {
		return
	}
	_, _ = fmt.Fprintf(d.out, format, args...)
}

func (d *dryRunPushRemote) println() {
	if !d.emitOperations {
		return
	}
	_, _ = fmt.Fprintln(d.out)
}

func (d *dryRunPushRemote) printBodyPreview(ctx context.Context, adfJSON []byte) {
	if !d.emitOperations {
		return
	}
	printDryRunBodyPreview(ctx, d.out, adfJSON)
}
