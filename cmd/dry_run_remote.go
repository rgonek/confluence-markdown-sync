package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
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

func (d *dryRunPushRemote) UpdatePage(ctx context.Context, pageID string, input confluence.PageUpsertInput) (confluence.Page, error) {
	fmt.Fprintf(d.out, "[DRY-RUN] UPDATE PAGE (PUT %s/wiki/api/v2/pages/%s)\n", d.domain, pageID)
	fmt.Fprintf(d.out, "  Title: %s\n", input.Title)
	if input.ParentPageID != "" {
		fmt.Fprintf(d.out, "  ParentPageID: %s\n", input.ParentPageID)
	}
	fmt.Fprintf(d.out, "  Version: %d\n", input.Version)

	var formattedADF bytes.Buffer
	if err := json.Indent(&formattedADF, input.BodyADF, "  ", "  "); err == nil {
		fmt.Fprintf(d.out, "  BodyADF:\n  %s\n", formattedADF.String())
	} else {
		fmt.Fprintf(d.out, "  BodyADF: %s\n", string(input.BodyADF))
	}
	fmt.Fprintln(d.out)

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

func (d *dryRunPushRemote) ArchivePages(ctx context.Context, pageIDs []string) (confluence.ArchiveResult, error) {
	fmt.Fprintf(d.out, "[DRY-RUN] ARCHIVE PAGES (POST %s/wiki/rest/api/content/archive)\n", d.domain)
	for _, id := range pageIDs {
		fmt.Fprintf(d.out, "  PageID: %s\n", id)
	}
	fmt.Fprintln(d.out)
	return confluence.ArchiveResult{TaskID: "dry-run-task-id"}, nil
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

func (d *dryRunPushRemote) DeleteAttachment(ctx context.Context, attachmentID string) error {
	fmt.Fprintf(d.out, "[DRY-RUN] DELETE ATTACHMENT (DELETE %s/wiki/api/v2/attachments/%s)\n\n", d.domain, attachmentID)
	return nil
}
