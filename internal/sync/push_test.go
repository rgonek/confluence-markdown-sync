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

func TestResolveParentIDFromHierarchy_PrefersFolderOverPage(t *testing.T) {
	pageIndex := PageIndex{
		"Root/Root.md": "page-root",
	}
	folderIndex := map[string]string{
		"Root": "folder-123",
	}

	if got := resolveParentIDFromHierarchy("Root/Child.md", "page-child", "", pageIndex, folderIndex); got != "folder-123" {
		t.Fatalf("parent for Root/Child.md = %q, want folder-123 (folder takes precedence)", got)
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

func TestPush_BlocksImmutableSpaceTampering(t *testing.T) {
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
		t.Fatal("expected immutable space validation error")
	}
	if !strings.Contains(err.Error(), "changed immutable space") {
		t.Fatalf("unexpected error: %v", err)
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

func TestCollectReferencedAssetPaths_FailsForNonAssetsReference(t *testing.T) {
	spaceDir := t.TempDir()
	sourcePath := filepath.Join(spaceDir, "root.md")
	nonAssetPath := filepath.Join(spaceDir, "images", "outside.png")

	if err := os.MkdirAll(filepath.Dir(nonAssetPath), 0o750); err != nil {
		t.Fatalf("mkdir images dir: %v", err)
	}
	if err := os.WriteFile(nonAssetPath, []byte("png"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	_, err := CollectReferencedAssetPaths(spaceDir, sourcePath, "![asset](images/outside.png)\n")
	if err == nil {
		t.Fatal("expected non-assets media reference to fail")
	}
	if !strings.Contains(err.Error(), "assets/") {
		t.Fatalf("unexpected error: %v", err)
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
	deletePageCalls          []string
	deleteAttachmentCalls    []string
	setContentStatusCalls    []string
	deleteContentStatusCalls []string
	addLabelsCalls           []string
	removeLabelCalls         []string
	failUpdate               bool
	failAddLabels            bool
}

func newRollbackPushRemote() *rollbackPushRemote {
	return &rollbackPushRemote{
		space:            confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pagesByID:        map[string]confluence.Page{},
		contentStatuses:  map[string]string{},
		labelsByPage:     map[string][]string{},
		nextPageID:       1,
		nextAttachmentID: 1,
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
	if f.failUpdate {
		return confluence.Page{}, errors.New("simulated update failure")
	}
	updated := confluence.Page{
		ID:           pageID,
		SpaceID:      input.SpaceID,
		ParentPageID: input.ParentPageID,
		Title:        input.Title,
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
	return confluence.ArchiveResult{TaskID: "task-1"}, nil
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
