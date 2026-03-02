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
	inner  syncflow.PushRemote
	out    io.Writer
	domain string
}

func (d *dryRunPushRemote) GetSpace(ctx context.Context, spaceKey string) (confluence.Space, error) {
	return d.inner.GetSpace(ctx, spaceKey)
}

func (d *dryRunPushRemote) ListPages(ctx context.Context, opts confluence.PageListOptions) (confluence.PageListResult, error) {
	return d.inner.ListPages(ctx, opts)
}

func (d *dryRunPushRemote) GetPage(ctx context.Context, pageID string) (confluence.Page, error) {
	return d.inner.GetPage(ctx, pageID)
}

func (d *dryRunPushRemote) GetContentStatus(ctx context.Context, pageID string) (string, error) {
	if strings.HasPrefix(pageID, "dry-run-") {
		return "", nil
	}
	return d.inner.GetContentStatus(ctx, pageID)
}

func (d *dryRunPushRemote) SetContentStatus(ctx context.Context, pageID string, statusName string) error {
	fmt.Fprintf(d.out, "[DRY-RUN] SET CONTENT STATUS (PUT %s/wiki/rest/api/content/%s/state)\n", d.domain, pageID)
	fmt.Fprintf(d.out, "  Name: %s\n\n", statusName)
	return nil
}

func (d *dryRunPushRemote) DeleteContentStatus(ctx context.Context, pageID string) error {
	fmt.Fprintf(d.out, "[DRY-RUN] DELETE CONTENT STATUS (DELETE %s/wiki/rest/api/content/%s/state)\n\n", d.domain, pageID)
	return nil
}

func (d *dryRunPushRemote) GetLabels(ctx context.Context, pageID string) ([]string, error) {
	if strings.HasPrefix(pageID, "dry-run-") {
		return nil, nil
	}
	return d.inner.GetLabels(ctx, pageID)
}

func (d *dryRunPushRemote) AddLabels(ctx context.Context, pageID string, labels []string) error {
	fmt.Fprintf(d.out, "[DRY-RUN] ADD LABELS (POST %s/wiki/rest/api/content/%s/label)\n", d.domain, pageID)
	fmt.Fprintf(d.out, "  Labels: %v\n\n", labels)
	return nil
}

func (d *dryRunPushRemote) RemoveLabel(ctx context.Context, pageID string, labelName string) error {
	fmt.Fprintf(d.out, "[DRY-RUN] REMOVE LABEL (DELETE %s/wiki/rest/api/content/%s/label?name=%s)\n\n", d.domain, pageID, labelName)
	return nil
}

func (d *dryRunPushRemote) CreatePage(ctx context.Context, input confluence.PageUpsertInput) (confluence.Page, error) {
	fmt.Fprintf(d.out, "[DRY-RUN] CREATE PAGE (POST %s/wiki/api/v2/pages)\n", d.domain)
	fmt.Fprintf(d.out, "  Title: %s\n", input.Title)
	if input.ParentPageID != "" {
		fmt.Fprintf(d.out, "  ParentPageID: %s\n", input.ParentPageID)
	}
	fmt.Fprintf(d.out, "  Status: %s\n", input.Status)
	printDryRunBodyPreview(ctx, d.out, input.BodyADF)
	_, _ = fmt.Fprintln(d.out)

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

	fmt.Fprintf(d.out, "[DRY-RUN] UPDATE PAGE (PUT %s/wiki/api/v2/pages/%s)\n", d.domain, pageID)
	fmt.Fprintf(d.out, "  Title: %s\n", input.Title)
	if input.ParentPageID != "" {
		fmt.Fprintf(d.out, "  ParentPageID: %s\n", input.ParentPageID)
	}
	fmt.Fprintf(d.out, "  Version: %d\n", input.Version)
	printDryRunBodyPreview(ctx, d.out, input.BodyADF)
	_, _ = fmt.Fprintln(d.out)

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
	fmt.Fprintf(d.out, "[DRY-RUN] ARCHIVE PAGES (POST %s/wiki/rest/api/content/archive)\n", d.domain)
	for _, id := range pageIDs {
		fmt.Fprintf(d.out, "  PageID: %s\n", id)
	}
	_, _ = fmt.Fprintln(d.out)
	return confluence.ArchiveResult{TaskID: "dry-run-task-id"}, nil
}

func (d *dryRunPushRemote) WaitForArchiveTask(ctx context.Context, taskID string, opts confluence.ArchiveTaskWaitOptions) (confluence.ArchiveTaskStatus, error) {
	fmt.Fprintf(d.out, "[DRY-RUN] WAIT ARCHIVE TASK (GET %s/wiki/rest/api/longtask/%s)\n", d.domain, taskID)
	if opts.Timeout > 0 {
		fmt.Fprintf(d.out, "  Timeout: %s\n", opts.Timeout)
	}
	if opts.PollInterval > 0 {
		fmt.Fprintf(d.out, "  PollInterval: %s\n", opts.PollInterval)
	}
	_, _ = fmt.Fprintln(d.out)
	return confluence.ArchiveTaskStatus{TaskID: taskID, State: confluence.ArchiveTaskStateSucceeded, RawStatus: "DRY_RUN"}, nil
}

func (d *dryRunPushRemote) DeletePage(ctx context.Context, pageID string, hardDelete bool) error {
	purge := ""
	if hardDelete {
		purge = "?purge=true"
	}
	fmt.Fprintf(d.out, "[DRY-RUN] DELETE PAGE (DELETE %s/wiki/api/v2/pages/%s%s)\n\n", d.domain, pageID, purge)
	return nil
}

func (d *dryRunPushRemote) UploadAttachment(ctx context.Context, input confluence.AttachmentUploadInput) (confluence.Attachment, error) {
	fmt.Fprintf(d.out, "[DRY-RUN] UPLOAD ATTACHMENT (POST %s/wiki/rest/api/content/%s/child/attachment)\n", d.domain, input.PageID)
	fmt.Fprintf(d.out, "  Filename: %s\n", input.Filename)
	fmt.Fprintf(d.out, "  ContentType: %s\n", input.ContentType)
	fmt.Fprintf(d.out, "  Size: %d bytes\n\n", len(input.Data))

	return confluence.Attachment{
		ID:        "dry-run-attachment-id-" + input.Filename,
		PageID:    input.PageID,
		Filename:  input.Filename,
		MediaType: input.ContentType,
		WebURL:    fmt.Sprintf("%s/download/attachments/%s/%s", d.domain, input.PageID, input.Filename),
	}, nil
}

func (d *dryRunPushRemote) DeleteAttachment(ctx context.Context, attachmentID string, pageID string) error {
	fmt.Fprintf(d.out, "[DRY-RUN] DELETE ATTACHMENT (DELETE %s/wiki/api/v2/attachments/%s, page %s)\n\n", d.domain, attachmentID, pageID)
	return nil
}

func (d *dryRunPushRemote) CreateFolder(ctx context.Context, input confluence.FolderCreateInput) (confluence.Folder, error) {
	fmt.Fprintf(d.out, "[DRY-RUN] CREATE FOLDER (POST %s/wiki/api/v2/folders)\n", d.domain)
	fmt.Fprintf(d.out, "  Title: %s\n", input.Title)
	fmt.Fprintf(d.out, "  SpaceID: %s\n", input.SpaceID)
	if input.ParentID != "" {
		fmt.Fprintf(d.out, "  ParentID: %s\n", input.ParentID)
		fmt.Fprintf(d.out, "  ParentType: %s\n", input.ParentType)
	}
	_, _ = fmt.Fprintln(d.out)

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
	fmt.Fprintf(d.out, "[DRY-RUN] MOVE PAGE (PUT %s/wiki/rest/api/content/%s/move/append/%s)\n\n", d.domain, pageID, targetID)
	return nil
}

func (d *dryRunPushRemote) Close() error {
	closeRemoteIfPossible(d.inner)
	return nil
}
