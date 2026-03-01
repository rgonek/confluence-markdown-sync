//nolint:errcheck // test handlers intentionally ignore best-effort response write errors
package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGetPage_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"missing"}`, http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "user@example.com",
		APIToken: "token-123",
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	_, err = client.GetPage(context.Background(), "42")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPage() error = %v, want ErrNotFound", err)
	}
}

func TestGetFolder_ByID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/wiki/api/v2/folders/4623368196" {
			t.Fatalf("path = %s, want /wiki/api/v2/folders/4623368196", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"id":"4623368196","spaceId":"space-1","title":"Policies","parentId":"","parentType":"folder"}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "user@example.com",
		APIToken: "token-123",
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	folder, err := client.GetFolder(context.Background(), "4623368196")
	if err != nil {
		t.Fatalf("GetFolder() unexpected error: %v", err)
	}
	if folder.ID != "4623368196" {
		t.Fatalf("folder id = %q, want 4623368196", folder.ID)
	}
	if folder.Title != "Policies" {
		t.Fatalf("folder title = %q, want Policies", folder.Title)
	}
}

func TestListChanges_BuildsCQLFromSpaceAndSince(t *testing.T) {
	since := time.Date(2026, time.January, 2, 15, 4, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/rest/api/content/search" {
			t.Fatalf("path = %s, want /wiki/rest/api/content/search", r.URL.Path)
		}
		cql := r.URL.Query().Get("cql")
		if !strings.Contains(cql, `space="ENG"`) {
			t.Fatalf("cql = %q, missing space predicate", cql)
		}
		if !strings.Contains(cql, `lastmodified >= "2026-01-02 15:04"`) {
			t.Fatalf("cql = %q, missing since predicate", cql)
		}

		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{
			"results":[{"id":"77","title":"Roadmap","space":{"key":"ENG"},"version":{"number":8,"when":"2026-01-02T16:00:00Z"}}],
			"start":0,
			"limit":25,
			"size":1,
			"_links":{"next":"/wiki/rest/api/content/search?start=25"}
		}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "user@example.com",
		APIToken: "token-123",
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	result, err := client.ListChanges(context.Background(), ChangeListOptions{
		SpaceKey: "ENG",
		Since:    since,
		Limit:    25,
	})
	if err != nil {
		t.Fatalf("ListChanges() unexpected error: %v", err)
	}
	if len(result.Changes) != 1 {
		t.Fatalf("changes length = %d, want 1", len(result.Changes))
	}
	if result.Changes[0].PageID != "77" {
		t.Fatalf("change page ID = %q, want 77", result.Changes[0].PageID)
	}
	if result.NextStart != 25 {
		t.Fatalf("next start = %d, want 25", result.NextStart)
	}
	if !result.HasMore {
		t.Fatal("HasMore = false, want true")
	}
}

func TestArchiveAndDeleteEndpoints(t *testing.T) {
	var archiveCalls int
	var deleteCalls int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/wiki/rest/api/content/archive":
			archiveCalls++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode archive body: %v", err)
			}
			pages, ok := body["pages"].([]any)
			if !ok || len(pages) != 2 {
				t.Fatalf("archive pages payload = %#v, want 2 pages", body["pages"])
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, `{"id":"task-9001"}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		case r.Method == http.MethodDelete && r.URL.Path == "/wiki/api/v2/pages/42":
			deleteCalls++
			if got := r.URL.Query().Get("purge"); got != "true" {
				t.Fatalf("purge query = %q, want true", got)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "user@example.com",
		APIToken: "token-123",
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	archiveResult, err := client.ArchivePages(context.Background(), []string{"1", "2"})
	if err != nil {
		t.Fatalf("ArchivePages() unexpected error: %v", err)
	}
	if archiveResult.TaskID != "task-9001" {
		t.Fatalf("task ID = %q, want task-9001", archiveResult.TaskID)
	}

	if err := client.DeletePage(context.Background(), "42", true); err != nil {
		t.Fatalf("DeletePage() unexpected error: %v", err)
	}

	if archiveCalls != 1 {
		t.Fatalf("archive calls = %d, want 1", archiveCalls)
	}
	if deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", deleteCalls)
	}
}

func TestArchivePages_AlreadyArchivedReturnsErrArchived(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/wiki/rest/api/content/archive" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		w.WriteHeader(http.StatusBadRequest)
		if _, err := io.WriteString(w, `{"message":"Page 1 is already archived"}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "user@example.com",
		APIToken: "token-123",
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	_, err = client.ArchivePages(context.Background(), []string{"1"})
	if !errors.Is(err, ErrArchived) {
		t.Fatalf("ArchivePages() error = %v, want ErrArchived", err)
	}
}

func TestWaitForArchiveTask_CompletesAfterPolling(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/rest/api/longtask/task-42" {
			t.Fatalf("path = %s, want /wiki/rest/api/longtask/task-42", r.URL.Path)
		}
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			if _, err := io.WriteString(w, `{"id":"task-42","status":"RUNNING","finished":false,"percentageComplete":40}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
			return
		}
		if _, err := io.WriteString(w, `{"id":"task-42","status":"SUCCESS","finished":true,"successful":true,"percentageComplete":100}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "user@example.com",
		APIToken: "token-123",
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	status, err := client.WaitForArchiveTask(context.Background(), "task-42", ArchiveTaskWaitOptions{
		Timeout:      2 * time.Second,
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("WaitForArchiveTask() unexpected error: %v", err)
	}
	if status.State != ArchiveTaskStateSucceeded {
		t.Fatalf("status state = %s, want %s", status.State, ArchiveTaskStateSucceeded)
	}
	if callCount < 2 {
		t.Fatalf("long-task calls = %d, want at least 2", callCount)
	}
}

func TestWaitForArchiveTask_FailedTask(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/rest/api/longtask/task-7" {
			t.Fatalf("path = %s, want /wiki/rest/api/longtask/task-7", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"id":"task-7","status":"FAILED","finished":true,"successful":false,"errorMessage":"archive blocked"}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "user@example.com",
		APIToken: "token-123",
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	status, err := client.WaitForArchiveTask(context.Background(), "task-7", ArchiveTaskWaitOptions{
		Timeout:      time.Second,
		PollInterval: 10 * time.Millisecond,
	})
	if !errors.Is(err, ErrArchiveTaskFailed) {
		t.Fatalf("WaitForArchiveTask() error = %v, want ErrArchiveTaskFailed", err)
	}
	if status.State != ArchiveTaskStateFailed {
		t.Fatalf("status state = %s, want %s", status.State, ArchiveTaskStateFailed)
	}
}

func TestWaitForArchiveTask_TimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wiki/rest/api/longtask/task-99" {
			t.Fatalf("path = %s, want /wiki/rest/api/longtask/task-99", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"id":"task-99","status":"RUNNING","finished":false,"percentageComplete":10}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "user@example.com",
		APIToken: "token-123",
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	status, err := client.WaitForArchiveTask(context.Background(), "task-99", ArchiveTaskWaitOptions{
		Timeout:      30 * time.Millisecond,
		PollInterval: 10 * time.Millisecond,
	})
	if !errors.Is(err, ErrArchiveTaskTimeout) {
		t.Fatalf("WaitForArchiveTask() error = %v, want ErrArchiveTaskTimeout", err)
	}
	if status.TaskID != "task-99" {
		t.Fatalf("status task ID = %q, want task-99", status.TaskID)
	}
}

func TestCreateAndUpdatePage_Payloads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		switch r.Method {
		case http.MethodPost:
			if body["id"] != nil {
				t.Errorf("CreatePage payload should not have id, got %v", body["id"])
			}
			if body["spaceId"] != "S1" {
				t.Errorf("CreatePage spaceId = %v, want S1", body["spaceId"])
			}
			if _, err := io.WriteString(w, `{"id":"101","title":"New","spaceId":"S1","version":{"number":1}}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		case http.MethodPut:
			if body["id"] != "101" {
				t.Errorf("UpdatePage payload should have id=101, got %v", body["id"])
			}
			if body["spaceId"] != "S1" {
				t.Errorf("UpdatePage spaceId = %v, want S1", body["spaceId"])
			}
			if _, err := io.WriteString(w, `{"id":"101","title":"Updated","spaceId":"S1","version":{"number":2}}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	t.Cleanup(server.Close)

	client, _ := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})

	ctx := context.Background()
	input := PageUpsertInput{
		SpaceID: "S1",
		Title:   "Test",
		Version: 1,
		BodyADF: json.RawMessage(`{"version":1}`),
	}

	_, err := client.CreatePage(ctx, input)
	if err != nil {
		t.Fatalf("CreatePage failed: %v", err)
	}

	_, err = client.UpdatePage(ctx, "101", input)
	if err != nil {
		t.Fatalf("UpdatePage failed: %v", err)
	}
}

func TestUpdatePage_ArchivedReturnsErrArchived(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/wiki/api/v2/pages/101" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		w.WriteHeader(http.StatusConflict)
		if _, err := io.WriteString(w, `{"message":"Cannot update archived content"}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "user@example.com",
		APIToken: "token-123",
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	_, err = client.UpdatePage(context.Background(), "101", PageUpsertInput{
		SpaceID: "S1",
		Title:   "Archived",
		Version: 2,
		BodyADF: json.RawMessage(`{"version":1,"type":"doc","content":[]}`),
	})
	if !errors.Is(err, ErrArchived) {
		t.Fatalf("UpdatePage() error = %v, want ErrArchived", err)
	}
}

func TestCreateFolder_PostsCorrectPayload(t *testing.T) {
	var receivedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/wiki/api/v2/folders" {
			t.Fatalf("path = %s, want /wiki/api/v2/folders", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"id":"f-1","spaceId":"SP1","title":"Policies","parentId":"","parentType":"space"}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, _ := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})

	folder, err := client.CreateFolder(context.Background(), FolderCreateInput{
		SpaceID: "SP1",
		Title:   "Policies",
	})
	if err != nil {
		t.Fatalf("CreateFolder() error: %v", err)
	}
	if folder.ID != "f-1" {
		t.Fatalf("folder ID = %q, want f-1", folder.ID)
	}
	if receivedBody["spaceId"] != "SP1" {
		t.Fatalf("payload spaceId = %v, want SP1", receivedBody["spaceId"])
	}
	if receivedBody["title"] != "Policies" {
		t.Fatalf("payload title = %v, want Policies", receivedBody["title"])
	}
	if receivedBody["parentType"] != "space" {
		t.Fatalf("payload parentType = %v, want space", receivedBody["parentType"])
	}
}

func TestCreateFolder_WithParentID(t *testing.T) {
	var receivedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"id":"f-2","spaceId":"SP1","title":"Sub","parentId":"f-1","parentType":"folder"}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, _ := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})

	folder, err := client.CreateFolder(context.Background(), FolderCreateInput{
		SpaceID:  "SP1",
		ParentID: "f-1",
		Title:    "Sub",
	})
	if err != nil {
		t.Fatalf("CreateFolder() error: %v", err)
	}
	if folder.ID != "f-2" {
		t.Fatalf("folder ID = %q, want f-2", folder.ID)
	}
	if receivedBody["parentId"] != "f-1" {
		t.Fatalf("payload parentId = %v, want f-1", receivedBody["parentId"])
	}
	if receivedBody["parentType"] != "folder" {
		t.Fatalf("payload parentType = %v, want folder", receivedBody["parentType"])
	}
}

func TestMovePage_PutsToCorrectEndpoint(t *testing.T) {
	var calledPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s, want PUT", r.Method)
		}
		calledPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, `{}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, _ := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})

	err := client.MovePage(context.Background(), "page-42", "folder-7")
	if err != nil {
		t.Fatalf("MovePage() error: %v", err)
	}
	want := "/wiki/rest/api/content/page-42/move/append/folder-7"
	if calledPath != want {
		t.Fatalf("path = %q, want %q", calledPath, want)
	}
}

func TestMovePage_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	client, _ := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})

	err := client.MovePage(context.Background(), "p1", "t1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("MovePage() error = %v, want ErrNotFound", err)
	}
}
