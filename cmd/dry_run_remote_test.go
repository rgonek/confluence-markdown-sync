package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

// Minimal mock
type dummyPushRemote struct {
	syncflow.PushRemote
}

func (d *dummyPushRemote) GetPage(ctx context.Context, pageID string) (confluence.Page, error) {
	return confluence.Page{}, nil
}
func (d *dummyPushRemote) Close() error { return nil }

func TestDryRunRemote(t *testing.T) {
	ctx := context.Background()
	remote := &dryRunPushRemote{out: new(bytes.Buffer), inner: &dummyPushRemote{}}

	if _, err := remote.GetPage(ctx, "123"); err != nil {
		t.Error("GetPage failed")
	}

	if err := remote.SetContentStatus(ctx, "123", "current", confluence.ContentState{Name: "Ready"}); err != nil {
		t.Error("SetContentStatus failed")
	}

	if err := remote.DeleteContentStatus(ctx, "123", "current"); err != nil {
		t.Error("DeleteContentStatus failed")
	}

	if err := remote.AddLabels(ctx, "123", []string{"a"}); err != nil {
		t.Error("AddLabels failed")
	}

	if err := remote.RemoveLabel(ctx, "123", "a"); err != nil {
		t.Error("RemoveLabel failed")
	}

	if _, err := remote.ArchivePages(ctx, []string{"123"}); err != nil {
		t.Error("ArchivePages failed")
	}

	if _, err := remote.WaitForArchiveTask(ctx, "task1", confluence.ArchiveTaskWaitOptions{}); err != nil {
		t.Error("WaitForArchiveTask failed")
	}

	if err := remote.DeletePage(ctx, "123", confluence.PageDeleteOptions{Purge: true}); err != nil {
		t.Error("DeletePage failed")
	}

	if _, err := remote.UploadAttachment(ctx, confluence.AttachmentUploadInput{PageID: "123"}); err != nil {
		t.Error("UploadAttachment failed")
	}

	if err := remote.DeleteAttachment(ctx, "123", "att1"); err != nil {
		t.Error("DeleteAttachment failed")
	}

	if _, err := remote.CreateFolder(ctx, confluence.FolderCreateInput{SpaceID: "Space", Title: "Folder"}); err != nil {
		t.Error("CreateFolder failed")
	}

	if err := remote.MovePage(ctx, "123", "456"); err != nil {
		t.Error("MovePage failed")
	}

	if err := remote.Close(); err != nil {
		t.Error("Close failed")
	}
}
