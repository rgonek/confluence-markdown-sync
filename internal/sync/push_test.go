package sync

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
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
}

func (f *fakeFolderPushRemote) GetSpace(_ context.Context, spaceKey string) (confluence.Space, error) {
	return confluence.Space{ID: "space-1", Key: spaceKey}, nil
}

func (f *fakeFolderPushRemote) ListPages(_ context.Context, _ confluence.PageListOptions) (confluence.PageListResult, error) {
	return confluence.PageListResult{}, nil
}

func (f *fakeFolderPushRemote) GetPage(_ context.Context, pageID string) (confluence.Page, error) {
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
