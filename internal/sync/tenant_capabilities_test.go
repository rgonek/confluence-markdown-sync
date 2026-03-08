package sync

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestPull_FolderCapabilityFallbackSelectedBeforeHierarchyWalk(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "existing.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Existing", ID: "existing", Version: 1},
		Body:        "existing\n",
	}); err != nil {
		t.Fatalf("write existing markdown: %v", err)
	}

	remote := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Start Here",
				ParentPageID: "folder-1",
				ParentType:   "folder",
				Version:      2,
				LastModified: time.Date(2026, time.March, 5, 12, 0, 0, 0, time.UTC),
			},
			{
				ID:           "2",
				SpaceID:      "space-1",
				Title:        "Child",
				ParentPageID: "folder-2",
				ParentType:   "folder",
				Version:      2,
				LastModified: time.Date(2026, time.March, 5, 12, 5, 0, 0, time.UTC),
			},
		},
		pagesByID: map[string]confluence.Page{
			"1": {ID: "1", SpaceID: "space-1", Title: "Start Here", ParentPageID: "folder-1", ParentType: "folder", Version: 2, BodyADF: rawJSON(t, map[string]any{"version": 1, "type": "doc", "content": []any{}})},
			"2": {ID: "2", SpaceID: "space-1", Title: "Child", ParentPageID: "folder-2", ParentType: "folder", Version: 2, BodyADF: rawJSON(t, map[string]any{"version": 1, "type": "doc", "content": []any{}})},
		},
		folderErr: &confluence.APIError{
			StatusCode: 500,
			Method:     "GET",
			URL:        "/wiki/api/v2/folders/folder-1",
			Message:    "Internal Server Error",
		},
	}

	result, err := Pull(context.Background(), remote, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
	})
	if err != nil {
		t.Fatalf("Pull() unexpected error: %v", err)
	}

	if len(remote.getFolderCalls) != 1 {
		t.Fatalf("get folder calls = %d, want 1 capability probe before fallback", len(remote.getFolderCalls))
	}

	foundMode := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "FOLDER_LOOKUP_UNAVAILABLE" && strings.Contains(diag.Message, "compatibility mode") {
			foundMode = true
			break
		}
	}
	if !foundMode {
		t.Fatalf("expected concise folder compatibility diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPull_ContentStatusCapabilityFallbackEmitsCompatibilityDiagnostic(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")
	if err := fs.WriteMarkdownDocument(filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Version: 1,
			Status:  "Keep existing",
		},
		Body: "existing\n",
	}); err != nil {
		t.Fatalf("write existing markdown: %v", err)
	}

	remote := &fakePullRemote{
		space: confluence.Space{ID: "space-1", Key: "ENG", Name: "Engineering"},
		pages: []confluence.Page{
			{
				ID:           "1",
				SpaceID:      "space-1",
				Title:        "Root",
				Status:       "current",
				Version:      2,
				LastModified: time.Date(2026, time.March, 6, 12, 0, 0, 0, time.UTC),
			},
		},
		pagesByID: map[string]confluence.Page{
			"1": {
				ID:      "1",
				SpaceID: "space-1",
				Title:   "Root",
				Status:  "current",
				Version: 2,
				BodyADF: rawJSON(t, map[string]any{"version": 1, "type": "doc", "content": []any{}}),
			},
		},
		contentStatusErr: &confluence.APIError{
			StatusCode: 501,
			Method:     "GET",
			URL:        "/wiki/rest/api/content/1/state",
			Message:    "Not Implemented",
		},
	}

	result, err := Pull(context.Background(), remote, PullOptions{
		SpaceKey: "ENG",
		SpaceDir: spaceDir,
	})
	if err != nil {
		t.Fatalf("Pull() unexpected error: %v", err)
	}

	if got := len(remote.getStatusCalls); got != 1 {
		t.Fatalf("get content status calls = %d, want 1 capability probe", got)
	}

	foundMode := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "CONTENT_STATUS_COMPATIBILITY_MODE" && strings.Contains(diag.Message, "disabled for this pull") {
			foundMode = true
			break
		}
	}
	if !foundMode {
		t.Fatalf("expected concise content-status compatibility diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_ContentStatusCapabilityFallbackSkipsMetadataWrites(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")
	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
			Version: 1,
			Status:  "Ready to review",
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
	remote.getContentStatusErr = &confluence.APIError{
		StatusCode: 501,
		Method:     "GET",
		URL:        "/wiki/rest/api/content/1/state",
		Message:    "Not Implemented",
	}

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes:        []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	if got := len(remote.getContentStatusCalls); got != 1 {
		t.Fatalf("get content status calls = %d, want 1 capability probe", got)
	}
	if len(remote.setContentStatusArgs) != 0 {
		t.Fatalf("set content status args = %d, want 0 in compatibility mode", len(remote.setContentStatusArgs))
	}
	if len(remote.deleteContentStatusArgs) != 0 {
		t.Fatalf("delete content status args = %d, want 0 in compatibility mode", len(remote.deleteContentStatusArgs))
	}
	if remote.updatePageCalls == 0 {
		t.Fatal("expected page content update to continue in compatibility mode")
	}

	foundMode := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "CONTENT_STATUS_COMPATIBILITY_MODE" && strings.Contains(diag.Message, "metadata sync disabled") {
			foundMode = true
			break
		}
	}
	if !foundMode {
		t.Fatalf("expected content-status compatibility diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_ContentStatusProbeRealErrorsSurface(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		message    string
	}{
		{name: "forbidden", statusCode: 403, message: "Forbidden"},
		{name: "rate_limited", statusCode: 429, message: "Too Many Requests"},
		{name: "server_error", statusCode: 500, message: "Internal Server Error"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			spaceDir := t.TempDir()
			mdPath := filepath.Join(spaceDir, "root.md")
			if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
				Frontmatter: fs.Frontmatter{
					Title:   "Root",
					ID:      "1",
					Version: 1,
					Status:  "Ready to review",
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
			remote.getContentStatusErr = &confluence.APIError{
				StatusCode: tc.statusCode,
				Method:     "GET",
				URL:        "/wiki/rest/api/content/1/state",
				Message:    tc.message,
			}

			result, err := Push(context.Background(), remote, PushOptions{
				SpaceKey:       "ENG",
				SpaceDir:       spaceDir,
				Domain:         "https://example.atlassian.net",
				State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
				ConflictPolicy: PushConflictPolicyCancel,
				Changes:        []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
			})
			if err == nil {
				t.Fatal("Push() error = nil, want content-status error to surface")
			}
			if !strings.Contains(err.Error(), "get content status") {
				t.Fatalf("Push() error = %v, want get content status failure", err)
			}
			for _, diag := range result.Diagnostics {
				if diag.Code == "CONTENT_STATUS_COMPATIBILITY_MODE" {
					t.Fatalf("unexpected compatibility diagnostic: %+v", diag)
				}
			}
		})
	}
}

func TestPush_ContentStatusCompatibilityProbeCoversExistingPageClearOperations(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "root.md")
	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   "Root",
			ID:      "1",
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
	remote.getContentStatusErr = &confluence.APIError{
		StatusCode: 501,
		Method:     "GET",
		URL:        "/wiki/rest/api/content/1/state",
		Message:    "Not Implemented",
	}

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes:        []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	if got := len(remote.getContentStatusCalls); got != 1 {
		t.Fatalf("get content status calls = %d, want 1 compatibility probe for clear-only update", got)
	}
	if len(remote.setContentStatusArgs) != 0 {
		t.Fatalf("set content status args = %d, want 0 in compatibility mode", len(remote.setContentStatusArgs))
	}
	if len(remote.deleteContentStatusArgs) != 0 {
		t.Fatalf("delete content status args = %d, want 0 in compatibility mode", len(remote.deleteContentStatusArgs))
	}
	if remote.updatePageCalls == 0 {
		t.Fatal("expected page content update to continue in compatibility mode")
	}

	foundMode := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "CONTENT_STATUS_COMPATIBILITY_MODE" && strings.Contains(diag.Message, "metadata sync disabled") {
			foundMode = true
			break
		}
	}
	if !foundMode {
		t.Fatalf("expected content-status compatibility diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_ContentStatusProbeRealErrorsSurfaceForExistingPageClearOperations(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		message    string
	}{
		{name: "forbidden", statusCode: 403, message: "Forbidden"},
		{name: "rate_limited", statusCode: 429, message: "Too Many Requests"},
		{name: "server_error", statusCode: 500, message: "Internal Server Error"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			spaceDir := t.TempDir()
			mdPath := filepath.Join(spaceDir, "root.md")
			if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
				Frontmatter: fs.Frontmatter{
					Title:   "Root",
					ID:      "1",
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
			remote.contentStatuses["1"] = "Ready to review"
			remote.getContentStatusErr = &confluence.APIError{
				StatusCode: tc.statusCode,
				Method:     "GET",
				URL:        "/wiki/rest/api/content/1/state",
				Message:    tc.message,
			}

			result, err := Push(context.Background(), remote, PushOptions{
				SpaceKey:       "ENG",
				SpaceDir:       spaceDir,
				Domain:         "https://example.atlassian.net",
				State:          fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}},
				ConflictPolicy: PushConflictPolicyCancel,
				Changes:        []PushFileChange{{Type: PushChangeModify, Path: "root.md"}},
			})
			if err == nil {
				t.Fatal("Push() error = nil, want content-status error to surface")
			}
			if !strings.Contains(err.Error(), "get content status") {
				t.Fatalf("Push() error = %v, want get content status failure", err)
			}
			if remote.updatePageCalls != 0 {
				t.Fatalf("update page calls = %d, want 0 when capability probe fails", remote.updatePageCalls)
			}
			for _, diag := range result.Diagnostics {
				if diag.Code == "CONTENT_STATUS_COMPATIBILITY_MODE" {
					t.Fatalf("unexpected compatibility diagnostic: %+v", diag)
				}
			}
		})
	}
}

func TestPush_ContentStatusUnsupportedEndpointInEmptySpaceEmitsCompatibilityDiagnostic(t *testing.T) {
	spaceDir := t.TempDir()
	mdPath := filepath.Join(spaceDir, "new.md")
	if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:  "New Page",
			Status: "Ready to review",
		},
		Body: "content\n",
	}); err != nil {
		t.Fatalf("write markdown: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.getContentStatusErr = &confluence.APIError{
		StatusCode: 501,
		Method:     "GET",
		URL:        "/wiki/rest/api/content/new-page-1/state",
		Message:    "Not Implemented",
	}

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes:        []PushFileChange{{Type: PushChangeAdd, Path: "new.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	if remote.createPageCalls != 1 {
		t.Fatalf("create page calls = %d, want 1", remote.createPageCalls)
	}
	if got := len(remote.getContentStatusCalls); got != 1 {
		t.Fatalf("get content status calls = %d, want 1 compatibility probe after page creation", got)
	}
	if len(remote.setContentStatusArgs) != 0 {
		t.Fatalf("set content status args = %d, want 0 after compatibility fallback", len(remote.setContentStatusArgs))
	}
	if len(remote.deleteContentStatusArgs) != 0 {
		t.Fatalf("delete content status args = %d, want 0 after compatibility fallback", len(remote.deleteContentStatusArgs))
	}
	if result.State.PagePathIndex["new.md"] == "" {
		t.Fatalf("expected pushed page to be tracked, got state %+v", result.State.PagePathIndex)
	}

	foundMode := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "CONTENT_STATUS_COMPATIBILITY_MODE" && strings.Contains(diag.Message, "metadata sync disabled") {
			foundMode = true
			break
		}
	}
	if !foundMode {
		t.Fatalf("expected content-status compatibility diagnostic, got %+v", result.Diagnostics)
	}
}

func TestPush_ContentStatusRealErrorsSurfaceForNewPageInEmptySpace(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		message    string
	}{
		{name: "forbidden", statusCode: 403, message: "Forbidden"},
		{name: "rate_limited", statusCode: 429, message: "Too Many Requests"},
		{name: "server_error", statusCode: 500, message: "Internal Server Error"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			spaceDir := t.TempDir()
			mdPath := filepath.Join(spaceDir, "new.md")
			if err := fs.WriteMarkdownDocument(mdPath, fs.MarkdownDocument{
				Frontmatter: fs.Frontmatter{
					Title:  "New Page",
					Status: "Ready to review",
				},
				Body: "content\n",
			}); err != nil {
				t.Fatalf("write markdown: %v", err)
			}

			remote := newRollbackPushRemote()
			remote.getContentStatusErr = &confluence.APIError{
				StatusCode: tc.statusCode,
				Method:     "GET",
				URL:        "/wiki/rest/api/content/new-page-1/state",
				Message:    tc.message,
			}

			result, err := Push(context.Background(), remote, PushOptions{
				SpaceKey:       "ENG",
				SpaceDir:       spaceDir,
				Domain:         "https://example.atlassian.net",
				State:          fs.SpaceState{SpaceKey: "ENG"},
				ConflictPolicy: PushConflictPolicyCancel,
				Changes:        []PushFileChange{{Type: PushChangeAdd, Path: "new.md"}},
			})
			if err == nil {
				t.Fatal("Push() error = nil, want content-status error to surface")
			}
			if !strings.Contains(err.Error(), "get content status") {
				t.Fatalf("Push() error = %v, want get content status failure", err)
			}
			if got := len(remote.getContentStatusCalls); got != 1 {
				t.Fatalf("get content status calls = %d, want 1 runtime probe", got)
			}
			for _, diag := range result.Diagnostics {
				if diag.Code == "CONTENT_STATUS_COMPATIBILITY_MODE" {
					t.Fatalf("unexpected compatibility diagnostic: %+v", diag)
				}
			}
		})
	}
}

func TestPush_FolderCapabilityFallbackUsesPageHierarchyMode(t *testing.T) {
	spaceDir := t.TempDir()
	nestedDir := filepath.Join(spaceDir, "Parent")
	if err := fs.WriteMarkdownDocument(filepath.Join(nestedDir, "Child.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Child"},
		Body:        "child\n",
	}); err != nil {
		t.Fatalf("write Child.md: %v", err)
	}

	remote := newRollbackPushRemote()
	remote.listFoldersErr = &confluence.APIError{
		StatusCode: 500,
		Method:     "GET",
		URL:        "/wiki/api/v2/folders",
		Message:    "Internal Server Error",
	}

	result, err := Push(context.Background(), remote, PushOptions{
		SpaceKey:       "ENG",
		SpaceDir:       spaceDir,
		Domain:         "https://example.atlassian.net",
		State:          fs.SpaceState{SpaceKey: "ENG"},
		ConflictPolicy: PushConflictPolicyCancel,
		Changes:        []PushFileChange{{Type: PushChangeAdd, Path: "Parent/Child.md"}},
	})
	if err != nil {
		t.Fatalf("Push() unexpected error: %v", err)
	}

	if remote.createFolderCalls != 0 {
		t.Fatalf("create folder calls = %d, want 0 after compatibility mode selection", remote.createFolderCalls)
	}
	if remote.createPageCalls < 2 {
		t.Fatalf("create page calls = %d, want at least 2 for parent compatibility page + child", remote.createPageCalls)
	}

	foundMode := false
	for _, diag := range result.Diagnostics {
		if diag.Code == "FOLDER_COMPATIBILITY_MODE" && strings.Contains(diag.Message, "page-based hierarchy mode") {
			foundMode = true
			break
		}
	}
	if !foundMode {
		t.Fatalf("expected folder compatibility diagnostic, got %+v", result.Diagnostics)
	}
}
