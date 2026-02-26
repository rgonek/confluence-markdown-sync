package sync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestEnsureADFMediaCollection(t *testing.T) {
	testCases := []struct {
		name     string
		adf      string
		pageID   string
		expected string
	}{
		{
			name:     "adds collection and type to media node",
			adf:      `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"media","attrs":{"id":"att1"}}]}]}`,
			pageID:   "123",
			expected: `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"media","attrs":{"id":"att1","collection":"contentId-123","type":"file"}}]}]}`,
		},
		{
			name:     "adds collection and type to mediaInline node",
			adf:      `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"mediaInline","attrs":{"id":"att2"}}]}]}`,
			pageID:   "456",
			expected: `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"mediaInline","attrs":{"id":"att2","collection":"contentId-456","type":"file"}}]}]}`,
		},
		{
			name:     "does not overwrite existing collection or type",
			adf:      `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"media","attrs":{"id":"att3","collection":"other","type":"image"}}]}]}`,
			pageID:   "789",
			expected: `{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"media","attrs":{"id":"att3","collection":"other","type":"image"}}]}]}`,
		},
		{
			name:     "handles nested nodes",
			adf:      `{"type":"doc","content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"media","attrs":{"id":"att4"}}]}]}]}]}`,
			pageID:   "101",
			expected: `{"type":"doc","content":[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"media","attrs":{"id":"att4","collection":"contentId-101","type":"file"}}]}]}]}]}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ensureADFMediaCollection([]byte(tc.adf), tc.pageID)
			if err != nil {
				t.Fatalf("ensureADFMediaCollection() error: %v", err)
			}

			var gotObj, wantObj any
			if err := json.Unmarshal(got, &gotObj); err != nil {
				t.Fatalf("unmarshal got: %v", err)
			}
			if err := json.Unmarshal([]byte(tc.expected), &wantObj); err != nil {
				t.Fatalf("unmarshal expected: %v", err)
			}

			gotJSON, _ := json.Marshal(gotObj)
			wantJSON, _ := json.Marshal(wantObj)

			if string(gotJSON) != string(wantJSON) {
				t.Errorf("got  %s\nwant %s", string(gotJSON), string(wantJSON))
			}
		})
	}
}

func TestResolveParentIDFromHierarchy_PrefersIndexPageOverFolder(t *testing.T) {
	pageIndex := PageIndex{
		"Root/Root.md": "page-root",
	}
	folderIndex := map[string]string{
		"Root": "folder-123",
	}

	if got := resolveParentIDFromHierarchy("Root/Child.md", "page-child", "", pageIndex, folderIndex); got != "page-root" {
		t.Fatalf("parent for Root/Child.md = %q, want page-root (index page takes precedence)", got)
	}
}

func TestResolveParentIDFromHierarchy_NestedFolder(t *testing.T) {
	pageIndex := PageIndex{}
	folderIndex := map[string]string{
		"Engineering":         "folder-eng",
		"Engineering/Backend": "folder-be",
	}

	if got := resolveParentIDFromHierarchy("Engineering/Backend/Api.md", "page-api", "", pageIndex, folderIndex); got != "folder-be" {
		t.Fatalf("parent = %q, want folder-be", got)
	}
}

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

func (f *fakeFolderPushRemote) GetSpace(_ context.Context, spaceKey string) (confluence.Space, error) {
	return confluence.Space{ID: "space-1", Key: spaceKey}, nil
}

func (f *fakeFolderPushRemote) ListPages(_ context.Context, _ confluence.PageListOptions) (confluence.PageListResult, error) {
	return confluence.PageListResult{Pages: f.pages}, nil
}

func (f *fakeFolderPushRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
	if page, ok := f.pagesByID[pageID]; ok {
		return page, nil
	}
	return confluence.Page{}, confluence.ErrNotFound
}

func (f *fakeFolderPushRemote) GetContentStatus(_ context.Context, pageID string) (string, error) {
	return "", nil
}

func (f *fakeFolderPushRemote) SetContentStatus(_ context.Context, pageID string, statusName string) error {
	return nil
}

func (f *fakeFolderPushRemote) DeleteContentStatus(_ context.Context, pageID string) error {
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

func (f *fakeFolderPushRemote) DeletePage(_ context.Context, pageID string, hardDelete bool) error {
	return nil
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
	f.folders = append(f.folders, created)
	f.foldersByID[id] = created
	return created, nil
}

func (f *fakeFolderPushRemote) MovePage(_ context.Context, pageID string, targetID string) error {
	f.moves = append(f.moves, fakePageMove{pageID: pageID, targetID: targetID})
	return nil
}

func TestEnsureFolderHierarchy_CreatesMissingFolders(t *testing.T) {
	remote := &fakeFolderPushRemote{
		foldersByID: make(map[string]confluence.Folder),
	}
	folderIndex := map[string]string{}

	result, err := ensureFolderHierarchy(
		context.Background(),
		remote,
		"space-1",
		"Engineering/Backend",
		"",
		nil,
		folderIndex,
		nil,
	)
	if err != nil {
		t.Fatalf("ensureFolderHierarchy() error: %v", err)
	}

	if result["Engineering"] == "" {
		t.Error("expected folder Engineering to be created")
	}
	if result["Engineering/Backend"] == "" {
		t.Error("expected folder Engineering/Backend to be created")
	}
}

func TestEnsureFolderHierarchy_SkipsExistingFolders(t *testing.T) {
	remote := &fakeFolderPushRemote{
		foldersByID: make(map[string]confluence.Folder),
	}
	folderIndex := map[string]string{
		"Engineering": "folder-existing",
	}

	result, err := ensureFolderHierarchy(
		context.Background(),
		remote,
		"space-1",
		"Engineering/Backend",
		"",
		nil,
		folderIndex,
		nil,
	)
	if err != nil {
		t.Fatalf("ensureFolderHierarchy() error: %v", err)
	}

	if result["Engineering"] != "folder-existing" {
		t.Errorf("expected Engineering to remain folder-existing, got %q", result["Engineering"])
	}
}

func TestEnsureFolderHierarchy_EmitsDiagnostics(t *testing.T) {
	remote := &fakeFolderPushRemote{
		foldersByID: make(map[string]confluence.Folder),
	}
	folderIndex := map[string]string{}
	diagnostics := []PushDiagnostic{}

	result, err := ensureFolderHierarchy(
		context.Background(),
		remote,
		"space-1",
		"NewFolder",
		"",
		nil,
		folderIndex,
		&diagnostics,
	)
	if err != nil {
		t.Fatalf("ensureFolderHierarchy() error: %v", err)
	}

	if len(diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(diagnostics))
	}
	if diagnostics[0].Code != "FOLDER_CREATED" {
		t.Errorf("expected diagnostic code FOLDER_CREATED, got %s", diagnostics[0].Code)
	}
	if result["NewFolder"] == "" {
		t.Error("expected folder to be created")
	}
}

func TestPush_BlocksImmutableIDTampering(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "2",
			Space:   "ENG",
			Version: 1,
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := &fakeFolderPushRemote{
		foldersByID: map[string]confluence.Folder{},
	}

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		Domain:   "https://example.atlassian.net",
		State: fs.SpaceState{
			SpaceKey:      "ENG",
			PagePathIndex: map[string]string{"root.md": "1"},
		},
		Changes: []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err == nil {
		t.Fatal("expected immutable id validation error")
	}
	if !strings.Contains(err.Error(), "changed immutable id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPush_IgnoresFrontmatterSpace(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "OPS",
			Version: 1,
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "current",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		Domain:   "https://example.atlassian.net",
		State: fs.SpaceState{
			SpaceKey:      "ENG",
			PagePathIndex: map[string]string{"root.md": "1"},
		},
		Changes: []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err != nil {
		t.Fatalf("expected push success with ignored space key, got: %v", err)
	}
}

func TestPush_BlocksCurrentToDraftTransition(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
			State:   "draft",
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := &fakeFolderPushRemote{
		foldersByID: map[string]confluence.Folder{},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:      "1",
				SpaceID: "space-1",
				Title:   "Root",
				Status:  "current",
				Version: 1,
				BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
			},
		},
		pages: []confluence.Page{{
			ID:      "1",
			SpaceID: "space-1",
			Title:   "Root",
			Status:  "current",
			Version: 1,
		}},
	}

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		Domain:   "https://example.atlassian.net",
		State: fs.SpaceState{
			SpaceKey:      "ENG",
			PagePathIndex: map[string]string{"root.md": "1"},
		},
		Changes: []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err == nil {
		t.Fatal("expected current-to-draft validation error")
	}
	if !strings.Contains(err.Error(), "cannot be transitioned from current to draft") {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, statErr := os.Stat(mdPath); statErr != nil {
		t.Fatalf("markdown file should remain present: %v", statErr)
	}
}

func TestBuildStrictAttachmentIndex_AssignsPendingIDsForLocalAssets(t *testing.T) {
	spaceDir := t.TempDir()
	sourcePath := filepath.Join(spaceDir, "root.md")
	assetPath := filepath.Join(spaceDir, "assets", "new.png")

	if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	index, refs, err := BuildStrictAttachmentIndex(
		spaceDir,
		sourcePath,
		"![asset](assets/new.png)\n",
		map[string]string{},
	)
	if err != nil {
		t.Fatalf("BuildStrictAttachmentIndex() error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "assets/new.png" {
		t.Fatalf("referenced assets = %v, want [assets/new.png]", refs)
	}
	if got := strings.TrimSpace(index["assets/new.png"]); !strings.HasPrefix(got, "pending-attachment-") {
		t.Fatalf("expected pending attachment id for assets/new.png, got %q", got)
	}
}

func TestCollectReferencedAssetPaths_AllowsNonAssetsReferenceWithinSpace(t *testing.T) {
	spaceDir := t.TempDir()
	sourcePath := filepath.Join(spaceDir, "root.md")
	nonAssetPath := filepath.Join(spaceDir, "images", "outside.png")

	if err := os.MkdirAll(filepath.Dir(nonAssetPath), 0o750); err != nil {
		t.Fatalf("mkdir images dir: %v", err)
	}
	if err := os.WriteFile(nonAssetPath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	refs, err := CollectReferencedAssetPaths(spaceDir, sourcePath, "![asset](images/outside.png)\n")
	if err != nil {
		t.Fatalf("CollectReferencedAssetPaths() error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "images/outside.png" {
		t.Fatalf("referenced assets = %v, want [images/outside.png]", refs)
	}
}

func TestCollectReferencedAssetPaths_IncludesLocalFileLinks(t *testing.T) {
	spaceDir := t.TempDir()
	sourcePath := filepath.Join(spaceDir, "root.md")
	docPath := filepath.Join(spaceDir, "assets", "manual.pdf")

	if err := os.MkdirAll(filepath.Dir(docPath), 0o750); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}
	if err := os.WriteFile(docPath, []byte("pdf"), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	refs, err := CollectReferencedAssetPaths(spaceDir, sourcePath, "[Manual](assets/manual.pdf)\n")
	if err != nil {
		t.Fatalf("CollectReferencedAssetPaths() error: %v", err)
	}
	if len(refs) != 1 || refs[0] != "assets/manual.pdf" {
		t.Fatalf("referenced assets = %v, want [assets/manual.pdf]", refs)
	}
}

func TestCollectReferencedAssetPaths_FailsForOutsideSpaceReference(t *testing.T) {
	rootDir := t.TempDir()
	spaceDir := filepath.Join(rootDir, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	sourcePath := filepath.Join(spaceDir, "root.md")
	_, err := CollectReferencedAssetPaths(spaceDir, sourcePath, "![asset](../outside.png)\n")
	if err == nil {
		t.Fatal("expected outside-space media reference to fail")
	}
	if !strings.Contains(err.Error(), "outside the space directory") {
		t.Fatalf("expected actionable outside-space message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "assets/") {
		t.Fatalf("expected assets destination hint, got: %v", err)
	}
}

func TestPush_KeepOrphanAssetsPreservesUnreferencedAttachment(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "current",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:            "ENG",
		SpaceDir:            spaceDir,
		Domain:              "https://example.atlassian.net",
		KeepOrphanAssets:    true,
		ConflictPolicy:      PushConflictPolicyCancel,
		State:               fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}, AttachmentIndex: map[string]string{"assets/1/orphan.png": "att-1"}},
		Changes:             []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
		ArchiveTimeout:      confluence.DefaultArchiveTaskTimeout,
		ArchivePollInterval: confluence.DefaultArchiveTaskPollInterval,
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	if len(remote.deleteAttachmentCalls) != 0 {
		t.Fatalf("delete attachment calls = %d, want 0", len(remote.deleteAttachmentCalls))
	}
	if got := strings.TrimSpace(result.State.AttachmentIndex["assets/1/orphan.png"]); got != "att-1" {
		t.Fatalf("attachment index value = %q, want att-1", got)
	}

	hasPreservedDiagnostic := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ATTACHMENT_PRESERVED" {
			hasPreservedDiagnostic = true
			break
		}
	}
	if !hasPreservedDiagnostic {
		t.Fatalf("expected ATTACHMENT_PRESERVED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_MigratesLocalRelativeAssetIntoPageHierarchy(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")
	legacyAssetPath := filepath.Join(spaceDir, "diagram.png")

	if err := os.WriteFile(legacyAssetPath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
		},
		Body: "![diagram](./diagram.png)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "current",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		ConflictPolicy: PushConflictPolicyCancel,
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		Changes:        []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	targetAssetRelPath := "assets/1/diagram.png"
	targetAssetAbsPath := filepath.Join(spaceDir, filepath.FromSlash(targetAssetRelPath))
	if _, statErr := os.Stat(targetAssetAbsPath); statErr != nil {
		t.Fatalf("expected migrated asset %s to exist: %v", targetAssetRelPath, statErr)
	}
	if _, statErr := os.Stat(legacyAssetPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected original asset path to be removed, stat=%v", statErr)
	}

	updatedDoc, err := fs.ReadMarkdownDocument(mdPath)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	if !strings.Contains(updatedDoc.Body, "assets/1/diagram.png") {
		t.Fatalf("expected markdown body to reference migrated asset path, body=%q", updatedDoc.Body)
	}

	if got := strings.TrimSpace(result.State.AttachmentIndex[targetAssetRelPath]); got == "" {
		t.Fatalf("expected state attachment index to include %s", targetAssetRelPath)
	}
}

func TestPush_UploadsLocalFileLinksAsAttachments(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")
	assetPath := filepath.Join(spaceDir, "assets", "manual.pdf")

	if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("pdf"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
		},
		Body: "[Manual](assets/manual.pdf)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "current",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		ConflictPolicy: PushConflictPolicyCancel,
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		Changes:        []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	if remote.uploadAttachmentCalls != 1 {
		t.Fatalf("upload attachment calls = %d, want 1", remote.uploadAttachmentCalls)
	}

	payload, ok := remote.updateInputsByPageID["1"]
	if !ok {
		t.Fatalf("expected update payload for page 1")
	}
	body := string(payload.BodyADF)
	if !strings.Contains(body, `"type":"media"`) {
		t.Fatalf("expected update ADF to include media node for linked file, body=%s", body)
	}
	if !strings.Contains(body, `"id":"att-1"`) {
		t.Fatalf("expected linked file to resolve to uploaded attachment id, body=%s", body)
	}

	updatedDoc, err := fs.ReadMarkdownDocument(mdPath)
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	if !strings.Contains(updatedDoc.Body, "[Manual](assets/1/manual.pdf)") {
		t.Fatalf("expected markdown link to be normalized into per-page assets directory, body=%q", updatedDoc.Body)
	}

	if got := strings.TrimSpace(result.State.AttachmentIndex["assets/1/manual.pdf"]); got != "att-1" {
		t.Fatalf("attachment index value = %q, want att-1", got)
	}
}

func TestPush_PreflightStrictFailureSkipsRemoteMutations(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "new.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New",
			Space: "ENG",
		},
		Body: "[Broken](missing.md)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeAdd,
			Path: "new.md",
		}},
	})
	if err == nil {
		t.Fatal("expected strict conversion error")
	}
	if !strings.Contains(err.Error(), "strict conversion failed") {
		t.Fatalf("unexpected error: %v", err)
	}

	if remote.createPageCalls != 0 {
		t.Fatalf("create page calls = %d, want 0", remote.createPageCalls)
	}
	if remote.updatePageCalls != 0 {
		t.Fatalf("update page calls = %d, want 0", remote.updatePageCalls)
	}
	if remote.uploadAttachmentCalls != 0 {
		t.Fatalf("upload attachment calls = %d, want 0", remote.uploadAttachmentCalls)
	}
}

func TestPush_PreflightStrictResolvesCrossSpaceLinkWithGlobalIndex(t *testing.T) {
	repo := t.TempDir()
	engDir := filepath.Join(repo, "Engineering (ENG)")
	tdDir := filepath.Join(repo, "Technical Docs (TD)")
	if err := os.MkdirAll(engDir, 0o750); err != nil {
		t.Fatalf("mkdir eng dir: %v", err)
	}
	if err := os.MkdirAll(tdDir, 0o750); err != nil {
		t.Fatalf("mkdir td dir: %v", err)
	}

	mdPath := filepath.Join(engDir, "new.md")
	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New",
			Space: "ENG",
		},
		Body: "[Cross Space](../Technical%20Docs%20(TD)/target.md)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	targetPath := filepath.Join(tdDir, "target.md")
	if err := fs.WriteMarkdownDocument(targetPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Target",
			ID:      "200",
			Space:   "TD",
			Version: 1,
		},
		Body: "target\n",
	}); err != nil {
		t.Fatalf("write cross-space markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:            "ENG",
		SpaceDir:            engDir,
		Domain:              "https://example.atlassian.net",
		State:               fs.SpaceState{SpaceKey: "ENG"},
		GlobalPageIndex:     GlobalPageIndex{"200": targetPath},
		ConflictPolicy:      PushConflictPolicyCancel,
		Changes:             []PushFileChange{{Type: PushChangeAdd, Path: "new.md"}},
		ArchiveTimeout:      confluence.DefaultArchiveTaskTimeout,
		ArchivePollInterval: confluence.DefaultArchiveTaskPollInterval,
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}
	if len(result.Commits) != 1 {
		t.Fatalf("commit count = %d, want 1", len(result.Commits))
	}
}

func TestPush_ResolvesLinksBetweenSimultaneousNewPages(t *testing.T) {
	spaceDir := t.TempDir()

	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "Fancy-Extensions.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Fancy Extensions",
			Space: "ENG",
		},
		Body: "[New page](New-Page.md)\n",
	}); err != nil {
		t.Fatalf("write Fancy-Extensions.md: %v", err)
	}

	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "New-Page.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New Page",
			Space: "ENG",
		},
		Body: "new page body\n",
	}); err != nil {
		t.Fatalf("write New-Page.md: %v", err)
	}

	remote := newRollbackPushRemote()
	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{
			{Type: PushChangeAdd, Path: "Fancy-Extensions.md"},
			{Type: PushChangeAdd, Path: "New-Page.md"},
		},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	fancyID := strings.TrimSpace(result.State.PagePathIndex["Fancy-Extensions.md"])
	newPageID := strings.TrimSpace(result.State.PagePathIndex["New-Page.md"])
	if fancyID == "" || newPageID == "" {
		t.Fatalf("expected IDs for both new pages, got state index: %+v", result.State.PagePathIndex)
	}

	updateInput, ok := remote.updateInputsByPageID[fancyID]
	if !ok {
		t.Fatalf("expected update payload for Fancy-Extensions page ID %s", fancyID)
	}

	body := string(updateInput.BodyADF)
	if !strings.Contains(body, "pageId="+newPageID) {
		t.Fatalf("expected Fancy-Extensions link to resolve to new page ID %s, body=%s", newPageID, body)
	}
	if strings.Contains(body, "pending-page-") {
		t.Fatalf("expected final ADF to avoid pending page IDs, body=%s", body)
	}
}

func TestPush_NewPageFailsWhenTrackedPageWithSameTitleExistsInSameDirectory(t *testing.T) {
	spaceDir := t.TempDir()

	existingPath := filepath.Join(spaceDir, "Conflict-Test-Page.md")
	if err := fs.WriteMarkdownDocument(existingPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Conflict Test Page",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
		},
		Body: "existing\n",
	}); err != nil {
		t.Fatalf("write existing markdown: %v", err)
	}

	newPath := filepath.Join(spaceDir, "Conflict-Test.md")
	if err := fs.WriteMarkdownDocument(newPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Conflict Test Page",
			Space: "ENG",
		},
		Body: "new\n",
	}); err != nil {
		t.Fatalf("write new markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"Conflict-Test-Page.md": "1"}},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeAdd,
			Path: "Conflict-Test.md",
		}},
	})
	if err == nil {
		t.Fatal("expected duplicate title validation error")
	}
	if !strings.Contains(err.Error(), "duplicates tracked page") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPush_RollbackDeletesCreatedPageAndAttachmentsOnUpdateFailure(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "new.md")
	assetPath := filepath.Join(spaceDir, "assets", "new.png")

	if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New",
			Space: "ENG",
		},
		Body: "![asset](assets/new.png)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.failUpdate = true

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeAdd,
			Path: "new.md",
		}},
	})
	if err == nil {
		t.Fatal("expected update failure")
	}
	if !strings.Contains(err.Error(), "update page") {
		t.Fatalf("unexpected error: %v", err)
	}

	if remote.createPageCalls != 1 {
		t.Fatalf("create page calls = %d, want 1", remote.createPageCalls)
	}
	if remote.uploadAttachmentCalls != 1 {
		t.Fatalf("upload attachment calls = %d, want 1", remote.uploadAttachmentCalls)
	}
	if len(remote.deleteAttachmentCalls) != 1 {
		t.Fatalf("delete attachment calls = %d, want 1", len(remote.deleteAttachmentCalls))
	}
	if len(remote.deletePageCalls) != 1 {
		t.Fatalf("delete page calls = %d, want 1", len(remote.deletePageCalls))
	}

	hasAttachmentRollback := false
	hasPageRollback := false
	for _, diag := range result.Diagnostics {
		switch diag.Code {
		case "ROLLBACK_ATTACHMENT_DELETED":
			hasAttachmentRollback = true
		case "ROLLBACK_PAGE_DELETED":
			hasPageRollback = true
		}
	}
	if !hasAttachmentRollback {
		t.Fatalf("expected ROLLBACK_ATTACHMENT_DELETED diagnostic, got %+v", result.Diagnostics)
	}
	if !hasPageRollback {
		t.Fatalf("expected ROLLBACK_PAGE_DELETED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_RollbackRestoresMetadataOnSyncFailure(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
			Status:  "Ready",
			Labels:  []string{"team"},
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])
	remote.contentStatuses["1"] = ""
	remote.labelsByPage["1"] = []string{}
	remote.failAddLabels = true

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeModify,
			Path: "root.md",
		}},
	})
	if err == nil {
		t.Fatal("expected metadata sync failure")
	}
	if !strings.Contains(err.Error(), "sync metadata") {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := strings.TrimSpace(remote.contentStatuses["1"]); got != "" {
		t.Fatalf("content status after rollback = %q, want empty", got)
	}
	if len(remote.deleteContentStatusCalls) == 0 {
		t.Fatalf("expected rollback to delete content status")
	}

	hasMetadataRollback := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ROLLBACK_METADATA_RESTORED" {
			hasMetadataRollback = true
			break
		}
	}
	if !hasMetadataRollback {
		t.Fatalf("expected ROLLBACK_METADATA_RESTORED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_RollbackRestoresPageContentOnPostUpdateFailure(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Updated Title",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
			Labels:  []string{"team"},
		},
		Body: "new local content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	originalBody := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"remote baseline"}]}]}`)
	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:           "1",
		SpaceID:      "space-1",
		Title:        "Original Title",
		ParentPageID: "parent-1",
		Status:       "draft",
		Version:      1,
		BodyADF:      append([]byte(nil), originalBody...),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])
	remote.contentStatuses["1"] = ""
	remote.labelsByPage["1"] = []string{}
	remote.failAddLabels = true

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes: []PushFileChange{{
			Type: PushChangeModify,
			Path: "root.md",
		}},
	})
	if err == nil {
		t.Fatal("expected metadata sync failure")
	}
	if !strings.Contains(err.Error(), "sync metadata") {
		t.Fatalf("unexpected error: %v", err)
	}

	if remote.updatePageCalls < 2 {
		t.Fatalf("update page calls = %d, want at least 2 (apply + rollback)", remote.updatePageCalls)
	}

	restored := remote.pagesByID["1"]
	if restored.Title != "Original Title" {
		t.Fatalf("restored title = %q, want Original Title", restored.Title)
	}
	if restored.Status != "draft" {
		t.Fatalf("restored status = %q, want draft", restored.Status)
	}
	if string(restored.BodyADF) != string(originalBody) {
		t.Fatalf("restored body = %s, want %s", string(restored.BodyADF), string(originalBody))
	}

	hasContentRollback := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ROLLBACK_PAGE_CONTENT_RESTORED" {
			hasContentRollback = true
			break
		}
	}
	if !hasContentRollback {
		t.Fatalf("expected ROLLBACK_PAGE_CONTENT_RESTORED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_DryRunSkipsRollbackAttempts(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "new.md")
	assetPath := filepath.Join(spaceDir, "assets", "new.png")

	if err := os.MkdirAll(filepath.Dir(assetPath), 0o750); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "New",
			Space: "ENG",
		},
		Body: "![asset](assets/new.png)\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.failUpdate = true

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		DryRun:         true,
		Changes: []PushFileChange{{
			Type: PushChangeAdd,
			Path: "new.md",
		}},
	})
	if err == nil {
		t.Fatal("expected update failure")
	}
	if !strings.Contains(err.Error(), "update page") {
		t.Fatalf("unexpected error: %v", err)
	}

	if remote.createPageCalls != 1 {
		t.Fatalf("create page calls = %d, want 1", remote.createPageCalls)
	}
	if remote.uploadAttachmentCalls != 1 {
		t.Fatalf("upload attachment calls = %d, want 1", remote.uploadAttachmentCalls)
	}
	if len(remote.deleteAttachmentCalls) != 0 {
		t.Fatalf("delete attachment calls = %d, want 0 in dry-run", len(remote.deleteAttachmentCalls))
	}
	if len(remote.deletePageCalls) != 0 {
		t.Fatalf("delete page calls = %d, want 0 in dry-run", len(remote.deletePageCalls))
	}

	for _, diag := range result.Diagnostics {
		if strings.HasPrefix(diag.Code, "ROLLBACK_") {
			t.Fatalf("unexpected rollback diagnostic in dry-run: %+v", diag)
		}
	}
}

func TestSyncPageMetadata_EquivalentLabelSetsDoNotChurn(t *testing.T) {
	remote := newRollbackPushRemote()
	remote.labelsByPage["1"] = []string{"ops", "team"}

	doc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Labels: []string{" team ", "OPS", "team"},
		},
	}

	if err := syncPageMetadata(context.Background(), remote, "1", doc); err != nil {
		t.Fatalf("syncPageMetadata() error: %v", err)
	}

	if len(remote.addLabelsCalls) != 0 {
		t.Fatalf("add labels calls = %d, want 0", len(remote.addLabelsCalls))
	}
	if len(remote.removeLabelCalls) != 0 {
		t.Fatalf("remove label calls = %d, want 0", len(remote.removeLabelCalls))
	}
}

func TestPush_DeleteAlreadyArchivedPageTreatsArchiveAsNoOp(t *testing.T) {
	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Old",
		Version: 5,
		WebURL:  "https://example.atlassian.net/wiki/pages/1",
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])
	remote.archivePagesErr = confluence.ErrArchived

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: t.TempDir(),
		State: fs.SpaceState{
			SpaceKey: "ENG",
			PagePathIndex: map[string]string{
				"old.md": "1",
			},
			AttachmentIndex: map[string]string{
				"assets/1/att-1-file.png": "att-1",
			},
		},
		Changes: []PushFileChange{{Type: PushChangeDelete, Path: "old.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	if len(result.Commits) != 1 {
		t.Fatalf("commits = %d, want 1", len(result.Commits))
	}
	if _, exists := result.State.PagePathIndex["old.md"]; exists {
		t.Fatalf("page index should not contain old.md after successful archive no-op")
	}
	if len(remote.archiveTaskCalls) != 0 {
		t.Fatalf("archive task calls = %d, want 0 when archive is already applied", len(remote.archiveTaskCalls))
	}

	foundDiagnostic := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ARCHIVE_ALREADY_APPLIED" {
			foundDiagnostic = true
			break
		}
	}
	if !foundDiagnostic {
		t.Fatalf("expected ARCHIVE_ALREADY_APPLIED diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_ArchivedRemotePageReturnsActionableError(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")

	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Space:   "ENG",
			Version: 1,
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Root",
		Status:  "archived",
		Version: 1,
		BodyADF: []byte(`{"version":1,"type":"doc","content":[]}`),
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])

	_, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
		Domain:   "https://example.atlassian.net",
		State: fs.SpaceState{
			SpaceKey:      "ENG",
			PagePathIndex: map[string]string{"root.md": "1"},
		},
		Changes: []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err == nil {
		t.Fatal("expected archived page error")
	}
	if !strings.Contains(err.Error(), "is archived remotely") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPush_DeleteBlocksLocalStateWhenArchiveTaskDoesNotComplete(t *testing.T) {
	remote := newRollbackPushRemote()
	remote.pagesByID["1"] = confluence.Page{
		ID:      "1",
		SpaceID: "space-1",
		Title:   "Old",
		Version: 5,
		WebURL:  "https://example.atlassian.net/wiki/pages/1",
	}
	remote.pages = append(remote.pages, remote.pagesByID["1"])
	remote.archiveTaskStatus = confluence.ArchiveTaskStatus{TaskID: "task-1", State: confluence.ArchiveTaskStateInProgress, RawStatus: "RUNNING"}
	remote.archiveTaskWaitErr = confluence.ErrArchiveTaskTimeout

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey: "ENG",
		SpaceDir: t.TempDir(),
		State: fs.SpaceState{
			SpaceKey: "ENG",
			PagePathIndex: map[string]string{
				"old.md": "1",
			},
			AttachmentIndex: map[string]string{
				"assets/1/att-1-file.png": "att-1",
			},
		},
		Changes: []PushFileChange{{Type: PushChangeDelete, Path: "old.md"}},
	})
	if err == nil {
		t.Fatal("expected archive wait failure")
	}
	if !strings.Contains(err.Error(), "wait for archive task") {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Commits) != 0 {
		t.Fatalf("commits = %d, want 0", len(result.Commits))
	}
	if got := strings.TrimSpace(result.State.PagePathIndex["old.md"]); got != "1" {
		t.Fatalf("page index old.md = %q, want 1", got)
	}
	if got := strings.TrimSpace(result.State.AttachmentIndex["assets/1/att-1-file.png"]); got != "att-1" {
		t.Fatalf("attachment index was mutated on archive failure: %q", got)
	}
	if len(remote.deleteAttachmentCalls) != 0 {
		t.Fatalf("delete attachment calls = %d, want 0", len(remote.deleteAttachmentCalls))
	}

	hasTimeoutDiagnostic := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "ARCHIVE_TASK_TIMEOUT" {
			hasTimeoutDiagnostic = true
			break
		}
	}
	if !hasTimeoutDiagnostic {
		t.Fatalf("expected ARCHIVE_TASK_TIMEOUT diagnostic, got %+v", result.Diagnostics)
	}
}

type rollbackPushRemote struct {
	space                    confluence.Space
	pages                    []confluence.Page
	pagesByID                map[string]confluence.Page
	contentStatuses          map[string]string
	labelsByPage             map[string][]string
	nextPageID               int
	nextAttachmentID         int
	createPageCalls          int
	updatePageCalls          int
	uploadAttachmentCalls    int
	archiveTaskCalls         []string
	deletePageCalls          []string
	deleteAttachmentCalls    []string
	setContentStatusCalls    []string
	deleteContentStatusCalls []string
	addLabelsCalls           []string
	removeLabelCalls         []string
	archiveTaskStatus        confluence.ArchiveTaskStatus
	archivePagesErr          error
	archiveTaskWaitErr       error
	failUpdate               bool
	failAddLabels            bool
	updateInputsByPageID     map[string]confluence.PageUpsertInput
}

func newRollbackPushRemote() *rollbackPushRemote {
	return &rollbackPushRemote{
		space:                confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pagesByID:            map[string]confluence.Page{},
		contentStatuses:      map[string]string{},
		labelsByPage:         map[string][]string{},
		updateInputsByPageID: map[string]confluence.PageUpsertInput{},
		nextPageID:           1,
		nextAttachmentID:     1,
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

func (f *rollbackPushRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
	page, ok := f.pagesByID[pageID]
	if !ok {
		return confluence.Page{}, confluence.ErrNotFound
	}
	return page, nil
}

func (f *rollbackPushRemote) GetContentStatus(_ context.Context, pageID string) (string, error) {
	return f.contentStatuses[pageID], nil
}

func (f *rollbackPushRemote) SetContentStatus(_ context.Context, pageID string, statusName string) error {
	f.setContentStatusCalls = append(f.setContentStatusCalls, pageID)
	f.contentStatuses[pageID] = strings.TrimSpace(statusName)
	return nil
}

func (f *rollbackPushRemote) DeleteContentStatus(_ context.Context, pageID string) error {
	f.deleteContentStatusCalls = append(f.deleteContentStatusCalls, pageID)
	f.contentStatuses[pageID] = ""
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
	id := fmt.Sprintf("new-page-%d", f.nextPageID)
	f.nextPageID++
	page := confluence.Page{
		ID:           id,
		SpaceID:      input.SpaceID,
		ParentPageID: input.ParentPageID,
		Title:        input.Title,
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
	if f.failUpdate {
		return confluence.Page{}, errors.New("simulated update failure")
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

func (f *rollbackPushRemote) DeletePage(_ context.Context, pageID string, _ bool) error {
	f.deletePageCalls = append(f.deletePageCalls, pageID)
	delete(f.pagesByID, pageID)
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

func (f *rollbackPushRemote) UploadAttachment(_ context.Context, input confluence.AttachmentUploadInput) (confluence.Attachment, error) {
	f.uploadAttachmentCalls++
	id := fmt.Sprintf("att-%d", f.nextAttachmentID)
	f.nextAttachmentID++
	return confluence.Attachment{ID: id, PageID: input.PageID, Filename: input.Filename}, nil
}

func (f *rollbackPushRemote) DeleteAttachment(_ context.Context, attachmentID string, _ string) error {
	f.deleteAttachmentCalls = append(f.deleteAttachmentCalls, attachmentID)
	return nil
}

func (f *rollbackPushRemote) CreateFolder(_ context.Context, input confluence.FolderCreateInput) (confluence.Folder, error) {
	return confluence.Folder{ID: "folder-1", SpaceID: input.SpaceID, Title: input.Title, ParentID: input.ParentID}, nil
}

func (f *rollbackPushRemote) MovePage(_ context.Context, pageID string, targetID string) error {
	return nil
}
