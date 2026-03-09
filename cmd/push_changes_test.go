package cmd

import (
	"bytes"
	"strings"
	"testing"

	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

func TestPrintPushSyncSummary_UploadOnlyPush(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	printPushSyncSummary(out, []syncflow.PushCommitPlan{{Path: "root.md"}}, []syncflow.PushDiagnostic{
		{Path: "assets/1/new.png", Code: "ATTACHMENT_CREATED", Message: "uploaded attachment att-1 from assets/1/new.png"},
	})

	got := out.String()
	if !strings.Contains(got, "pages changed: 1 (archived remotely: 0)") {
		t.Fatalf("expected page count summary, got:\n%s", got)
	}
	if !strings.Contains(got, "attachments: uploaded 1, deleted 0, preserved 0, skipped 0") {
		t.Fatalf("expected upload-focused attachment summary, got:\n%s", got)
	}
}

func TestPrintPushSyncSummary_DeleteOnlyPush(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	printPushSyncSummary(out, []syncflow.PushCommitPlan{{Path: "root.md", Deleted: true}}, []syncflow.PushDiagnostic{
		{Path: "assets/1/old.png", Code: "ATTACHMENT_DELETED", Message: "deleted attachment att-1 during page removal"},
	})

	got := out.String()
	if !strings.Contains(got, "pages changed: 1 (archived remotely: 1)") {
		t.Fatalf("expected archived page count summary, got:\n%s", got)
	}
	if !strings.Contains(got, "attachments: uploaded 0, deleted 1, preserved 0, skipped 0") {
		t.Fatalf("expected delete-focused attachment summary, got:\n%s", got)
	}
}

func TestPrintPushSyncSummary_MixedPageAndAttachmentPush(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	printPushSyncSummary(out, []syncflow.PushCommitPlan{
		{Path: "root.md"},
		{Path: "old.md", Deleted: true},
	}, []syncflow.PushDiagnostic{
		{Path: "assets/1/new.png", Code: "ATTACHMENT_CREATED", Message: "uploaded attachment att-1 from assets/1/new.png"},
		{Path: "assets/2/old.png", Code: "ATTACHMENT_DELETED", Message: "deleted attachment att-2 during page removal"},
	})

	got := out.String()
	if !strings.Contains(got, "pages changed: 2 (archived remotely: 1)") {
		t.Fatalf("expected mixed page summary, got:\n%s", got)
	}
	if !strings.Contains(got, "attachments: uploaded 1, deleted 1, preserved 0, skipped 0") {
		t.Fatalf("expected mixed attachment summary, got:\n%s", got)
	}
	if !strings.Contains(got, "diagnostics: 2") {
		t.Fatalf("expected diagnostics count, got:\n%s", got)
	}
}

func TestPrintPushSyncSummary_KeepOrphanAssetsCountsPreservedAndSkipped(t *testing.T) {
	t.Parallel()

	out := &bytes.Buffer{}
	printPushSyncSummary(out, []syncflow.PushCommitPlan{{Path: "root.md"}}, []syncflow.PushDiagnostic{
		{Path: "assets/1/orphan.png", Code: "ATTACHMENT_PRESERVED", Message: "kept unreferenced attachment att-1 because --keep-orphan-assets is enabled"},
	})

	got := out.String()
	if !strings.Contains(got, "attachments: uploaded 0, deleted 0, preserved 1, skipped 1") {
		t.Fatalf("expected keep-orphan-assets summary to show preserved and skipped counts, got:\n%s", got)
	}
}
