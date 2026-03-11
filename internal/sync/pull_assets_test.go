package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

func TestPull_SkipsMissingAssets(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG"},
		pages: []confluence.Page{
			{ID: "1", SpaceID: "space-1", Title: "Page 1"},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:      "1",
				Title:   "Page 1",
				BodyADF: rawJSON(t, sampleRootADF()),
			},
		},
		attachments: map[string][]byte{}, // Empty!
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey:          "ENG",
		SpaceDir:          spaceDir,
		SkipMissingAssets: true,
	})
	if err != nil {
		t.Fatalf("Pull() with skip=true failed: %v", err)
	}

	foundMissing := false
	for _, d := range result.Diagnostics {
		if d.Code == "ATTACHMENT_DOWNLOAD_SKIPPED" && strings.Contains(d.Message, "att-1") {
			foundMissing = true
			break
		}
	}
	if !foundMissing {
		t.Fatalf("expected ATTACHMENT_DOWNLOAD_SKIPPED diagnostic, got %+v", result.Diagnostics)
	}

	// Now try with skip=false (default)
	_, err = Pull(context.Background(), fake, PullOptions{
		SpaceKey:          "ENG",
		SpaceDir:          spaceDir,
		SkipMissingAssets: false,
	})
	if err == nil {
		t.Fatalf("Pull() with skip=false should have failed for missing attachment")
	}
	if !strings.Contains(err.Error(), "att-1") || !strings.Contains(err.Error(), "page 1") {
		t.Fatalf("error message should mention attachment and page, got: %v", err)
	}
}

func TestPull_ResolvesUnknownMediaIDByFilename(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	adf := map[string]any{
		"version": 1,
		"type":    "doc",
		"content": []any{
			map[string]any{
				"type": "mediaSingle",
				"content": []any{
					map[string]any{
						"type": "media",
						"attrs": map[string]any{
							"id":       "UNKNOWN_MEDIA_ID",
							"pageId":   "1",
							"fileName": "diagram.png",
						},
					},
				},
			},
		},
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG"},
		pages: []confluence.Page{{ID: "1", SpaceID: "space-1", Title: "Page 1"}},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:      "1",
				Title:   "Page 1",
				BodyADF: rawJSON(t, adf),
			},
		},
		attachments: map[string][]byte{
			"att-real": []byte("asset-bytes"),
		},
		attachmentsByPage: map[string][]confluence.Attachment{
			"1": {
				{ID: "att-real", PageID: "1", Filename: "diagram.png"},
			},
		},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
	})
	if err != nil {
		t.Fatalf("Pull() unexpected error: %v", err)
	}

	assetPath := filepath.Join(spaceDir, "assets", "1", "att-real-diagram.png")
	raw, err := os.ReadFile(assetPath) //nolint:gosec // path is controlled in test temp dir
	if err != nil {
		t.Fatalf("read resolved asset: %v", err)
	}
	if string(raw) != "asset-bytes" {
		t.Fatalf("asset bytes = %q, want %q", string(raw), "asset-bytes")
	}

	foundResolvedDiagnostic := false
	foundSkippedDiagnostic := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "UNKNOWN_MEDIA_ID_RESOLVED" {
			foundResolvedDiagnostic = true
		}
		if diag.Code == "ATTACHMENT_DOWNLOAD_SKIPPED" {
			foundSkippedDiagnostic = true
		}
	}
	if !foundResolvedDiagnostic {
		t.Fatalf("expected UNKNOWN_MEDIA_ID_RESOLVED diagnostic, got %+v", result.Diagnostics)
	}
	if foundSkippedDiagnostic {
		t.Fatalf("did not expect ATTACHMENT_DOWNLOAD_SKIPPED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPull_PrefersAttachmentIDMetadataForDownloadedAssetPaths(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	adf := map[string]any{
		"version": 1,
		"type":    "doc",
		"content": []any{
			map[string]any{
				"type": "mediaSingle",
				"content": []any{
					map[string]any{
						"type": "media",
						"attrs": map[string]any{
							"id":           "file-real",
							"attachmentId": "att-real",
							"pageId":       "1",
							"fileName":     "diagram.png",
						},
					},
				},
			},
		},
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG"},
		pages: []confluence.Page{{ID: "1", SpaceID: "space-1", Title: "Page 1"}},
		pagesByID: map[string]confluence.Page{
			"1": {ID: "1", Title: "Page 1", BodyADF: rawJSON(t, adf)},
		},
		attachments: map[string][]byte{
			"att-real": []byte("asset-bytes"),
		},
		attachmentsByPage: map[string][]confluence.Attachment{
			"1": {
				{ID: "att-real", FileID: "file-real", PageID: "1", Filename: "diagram.png"},
			},
		},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
	})
	if err != nil {
		t.Fatalf("Pull() unexpected error: %v", err)
	}

	assetPath := filepath.Join(spaceDir, "assets", "1", "att-real-diagram.png")
	if _, err := os.Stat(assetPath); err != nil {
		t.Fatalf("expected asset at attachment-id path: %v", err)
	}
	if got := strings.TrimSpace(result.State.AttachmentIndex["assets/1/att-real-diagram.png"]); got != "att-real" {
		t.Fatalf("attachment index = %q, want att-real", got)
	}
}

func TestPull_ResolvesFileIDToAttachmentIDForDownloadedAssetPaths(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	adf := map[string]any{
		"version": 1,
		"type":    "doc",
		"content": []any{
			map[string]any{
				"type": "mediaSingle",
				"content": []any{
					map[string]any{
						"type": "media",
						"attrs": map[string]any{
							"id":       "file-real",
							"pageId":   "1",
							"fileName": "diagram.png",
						},
					},
				},
			},
		},
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG"},
		pages: []confluence.Page{{ID: "1", SpaceID: "space-1", Title: "Page 1"}},
		pagesByID: map[string]confluence.Page{
			"1": {ID: "1", Title: "Page 1", BodyADF: rawJSON(t, adf)},
		},
		attachments: map[string][]byte{
			"att-real": []byte("asset-bytes"),
		},
		attachmentsByPage: map[string][]confluence.Attachment{
			"1": {
				{ID: "att-real", FileID: "file-real", PageID: "1", Filename: "diagram.png"},
			},
		},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
	})
	if err != nil {
		t.Fatalf("Pull() unexpected error: %v", err)
	}

	assetPath := filepath.Join(spaceDir, "assets", "1", "att-real-diagram.png")
	if _, err := os.Stat(assetPath); err != nil {
		t.Fatalf("expected asset at attachment-id path after fileId resolution: %v", err)
	}
	if got := strings.TrimSpace(result.State.AttachmentIndex["assets/1/att-real-diagram.png"]); got != "att-real" {
		t.Fatalf("attachment index = %q, want att-real", got)
	}
}

func TestPull_ResolvesFileIDOnlyMediaNodesToLocalMarkdownAssets(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space: %v", err)
	}

	adf := map[string]any{
		"version": 1,
		"type":    "doc",
		"content": []any{
			map[string]any{
				"type": "paragraph",
				"content": []any{
					map[string]any{
						"type": "mediaInline",
						"attrs": map[string]any{
							"id":         "file-image",
							"collection": "contentId-1",
							"type":       "image",
						},
					},
					map[string]any{
						"type": "text",
						"text": " ",
					},
					map[string]any{
						"type": "mediaInline",
						"attrs": map[string]any{
							"id":         "file-doc",
							"collection": "contentId-1",
							"type":       "file",
						},
					},
				},
			},
		},
	}

	fake := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG"},
		pages: []confluence.Page{{ID: "1", SpaceID: "space-1", Title: "Page 1"}},
		pagesByID: map[string]confluence.Page{
			"1": {ID: "1", Title: "Page 1", BodyADF: rawJSON(t, adf)},
		},
		attachments: map[string][]byte{
			"att-image": []byte("png"),
			"att-doc":   []byte("pdf"),
		},
		attachmentsByPage: map[string][]confluence.Attachment{
			"1": {
				{ID: "att-image", FileID: "file-image", PageID: "1", Filename: "diagram.png"},
				{ID: "att-doc", FileID: "file-doc", PageID: "1", Filename: "manual.txt"},
			},
		},
	}

	result, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
	})
	if err != nil {
		t.Fatalf("Pull() unexpected error: %v", err)
	}

	pagePath := filepath.Join(spaceDir, "Page-1.md")
	docRaw, err := os.ReadFile(pagePath) //nolint:gosec // test path is under t.TempDir
	if err != nil {
		t.Fatalf("read pulled markdown: %v", err)
	}
	docBody := string(docRaw)
	if !strings.Contains(docBody, "assets/1/att-image-diagram.png") {
		t.Fatalf("expected image asset link in markdown, got:\n%s", docBody)
	}
	if !strings.Contains(docBody, "assets/1/att-doc-manual.txt") {
		t.Fatalf("expected file asset link in markdown, got:\n%s", docBody)
	}
	if got := strings.TrimSpace(result.State.AttachmentIndex["assets/1/att-image-diagram.png"]); got != "att-image" {
		t.Fatalf("image attachment index = %q, want att-image", got)
	}
	if got := strings.TrimSpace(result.State.AttachmentIndex["assets/1/att-doc-manual.txt"]); got != "att-doc" {
		t.Fatalf("file attachment index = %q, want att-doc", got)
	}
}
