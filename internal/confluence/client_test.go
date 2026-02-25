//nolint:errcheck // test handlers intentionally ignore best-effort response write errors
package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewClient_RequiresCoreConfig(t *testing.T) {
	_, err := NewClient(ClientConfig{})
	if err == nil {
		t.Fatal("NewClient() expected error, got nil")
	}
}

func TestNewClient_AppliesRateAndRetryPolicyConfig(t *testing.T) {
	client, err := NewClient(ClientConfig{
		BaseURL:          "https://example.test",
		Email:            "user@example.com",
		APIToken:         "token-123",
		RateLimitRPS:     9,
		RetryMaxAttempts: 7,
		RetryBaseDelay:   200 * time.Millisecond,
		RetryMaxDelay:    3 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	if got := cap(client.limiter.tokens); got != 9 {
		t.Fatalf("rate limiter capacity = %d, want 9", got)
	}
	if client.retry.maxAttempts != 7 {
		t.Fatalf("retry max attempts = %d, want 7", client.retry.maxAttempts)
	}
	if client.retry.baseDelay != 200*time.Millisecond {
		t.Fatalf("retry base delay = %v, want 200ms", client.retry.baseDelay)
	}
	if client.retry.maxDelay != 3*time.Second {
		t.Fatalf("retry max delay = %v, want 3s", client.retry.maxDelay)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close() unexpected error: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() should be idempotent, got error: %v", err)
	}
}

func TestListSpaces_UsesExpectedEndpointAndAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/wiki/api/v2/spaces" {
			t.Fatalf("path = %s, want /wiki/api/v2/spaces", r.URL.Path)
		}

		user, pass, ok := r.BasicAuth()
		if !ok {
			t.Fatal("request missing basic auth")
		}
		if user != "user@example.com" || pass != "token-123" {
			t.Fatalf("auth = %q/%q, want user@example.com/token-123", user, pass)
		}

		if got := r.URL.Query().Get("keys"); got != "ENG,OPS" {
			t.Fatalf("keys query = %q, want ENG,OPS", got)
		}
		if got := r.URL.Query().Get("limit"); got != "50" {
			t.Fatalf("limit query = %q, want 50", got)
		}

		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"results":[{"id":"100","key":"ENG","name":"Engineering","type":"global"}],"meta":{"cursor":"next-cursor"}}`); err != nil {
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

	result, err := client.ListSpaces(context.Background(), SpaceListOptions{
		Keys:  []string{"ENG", "OPS"},
		Limit: 50,
	})
	if err != nil {
		t.Fatalf("ListSpaces() unexpected error: %v", err)
	}
	if len(result.Spaces) != 1 {
		t.Fatalf("spaces length = %d, want 1", len(result.Spaces))
	}
	if result.Spaces[0].Key != "ENG" {
		t.Fatalf("space key = %q, want ENG", result.Spaces[0].Key)
	}
	if result.NextCursor != "next-cursor" {
		t.Fatalf("next cursor = %q, want next-cursor", result.NextCursor)
	}
}

func TestListSpaces_UsesConfiguredUserAgent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != "conf/1.2.3" {
			t.Fatalf("User-Agent = %q, want conf/1.2.3", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"results":[]}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:   server.URL,
		Email:     "user@example.com",
		APIToken:  "token-123",
		UserAgent: "conf/1.2.3",
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	if _, err := client.ListSpaces(context.Background(), SpaceListOptions{Limit: 1}); err != nil {
		t.Fatalf("ListSpaces() unexpected error: %v", err)
	}
}

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

func TestDownloadAttachment_ResolvesUUID(t *testing.T) {
	uuid := "e2cabb2e-4df7-49bb-84e0-c76ae83f6f9b"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wiki/api/v2/pages/123/attachments":
			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, `{"results":[{"id":"att-uuid-123", "fileId":"`+uuid+`"}]}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		case "/wiki/api/v2/attachments/att-uuid-123":
			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, `{"id":"att-uuid-123","downloadLink":"/download/uuid.png"}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		case "/download/uuid.png":
			w.WriteHeader(http.StatusOK)
			if _, err := io.WriteString(w, "uuid-data"); err != nil {
				t.Fatalf("write response: %v", err)
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})
	if err != nil {
		t.Fatalf("NewClient() failed: %v", err)
	}

	var buf strings.Builder
	err = client.DownloadAttachment(context.Background(), uuid, "123", &buf)
	if err != nil {
		t.Fatalf("DownloadAttachment() error: %v", err)
	}
	if buf.String() != "uuid-data" {
		t.Fatalf("data = %q, want uuid-data", buf.String())
	}

}

func TestResolveAttachmentIDByFileID_Pagination(t *testing.T) {
	uuid := "e2cabb2e-4df7-49bb-84e0-c76ae83f6f9b"
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if callCount == 1 {
			if !strings.Contains(r.URL.Path, "/attachments") {
				t.Fatalf("call 1 path = %s", r.URL.Path)
			}
			// First page, doesn't contain our UUID
			if _, err := io.WriteString(w, `{
				"results":[{"id":"att-other", "fileId":"other-uuid"}],
				"_links":{"next":"/wiki/api/v2/pages/123/attachments?cursor=next-page-token"}
			}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		} else {
			if !strings.Contains(r.URL.RawQuery, "cursor=next-page-token") {
				t.Fatalf("call 2 query = %s, missing cursor", r.URL.RawQuery)
			}
			// Second page contains our UUID
			if _, err := io.WriteString(w, `{"results":[{"id":"att-uuid-123", "fileId":"`+uuid+`"}]}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		}
	}))
	t.Cleanup(server.Close)

	client, _ := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})

	id, err := client.resolveAttachmentIDByFileID(context.Background(), uuid, "123")
	if err != nil {
		t.Fatalf("resolveAttachmentIDByFileID() error: %v", err)
	}
	if id != "att-uuid-123" {
		t.Fatalf("id = %q, want att-uuid-123", id)
	}
	if callCount != 2 {
		t.Fatalf("callCount = %d, want 2", callCount)
	}
}

func TestDownloadAttachment_ResolvesAndDownloadsBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wiki/api/v2/attachments/att-1":
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, `{"id":"att-1","downloadLink":"/download/attachments/1/diagram.png"}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		case "/download/attachments/1/diagram.png":
			if r.Method != http.MethodGet {
				t.Fatalf("download method = %s, want GET", r.Method)
			}
			w.WriteHeader(http.StatusOK)
			if _, err := io.WriteString(w, "binary-data"); err != nil {
				t.Fatalf("write response: %v", err)
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
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

	var buf strings.Builder
	err = client.DownloadAttachment(context.Background(), "att-1", "123", &buf)
	if err != nil {
		t.Fatalf("DownloadAttachment() unexpected error: %v", err)
	}
	if buf.String() != "binary-data" {
		t.Fatalf("attachment bytes = %q, want %q", buf.String(), "binary-data")
	}
}

func TestUploadAndDeleteAttachmentEndpoints(t *testing.T) {
	uploadCalls := 0
	deleteCalls := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/wiki/rest/api/content/42/child/attachment":
			uploadCalls++
			if got := r.Header.Get("X-Atlassian-Token"); got != "no-check" {
				t.Fatalf("X-Atlassian-Token = %q, want no-check", got)
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data;") {
				t.Fatalf("content type = %q, want multipart/form-data", r.Header.Get("Content-Type"))
			}

			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatalf("MultipartReader() error: %v", err)
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Fatalf("NextPart() error: %v", err)
			}
			if part.FormName() != "file" {
				t.Fatalf("form field = %q, want file", part.FormName())
			}
			if part.FileName() != "diagram.png" {
				t.Fatalf("filename = %q, want diagram.png", part.FileName())
			}
			data, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("read multipart part: %v", err)
			}
			if string(data) != "asset-bytes" {
				t.Fatalf("uploaded bytes = %q, want asset-bytes", string(data))
			}

			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, `{"results":[{"id":"att-9","title":"diagram.png","_links":{"webui":"/wiki/pages/viewpage.action?pageId=42"}}]}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		case r.Method == http.MethodDelete && r.URL.Path == "/wiki/api/v2/attachments/att-9":
			deleteCalls++
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

	attachment, err := client.UploadAttachment(context.Background(), AttachmentUploadInput{
		PageID:   "42",
		Filename: "diagram.png",
		Data:     []byte("asset-bytes"),
	})
	if err != nil {
		t.Fatalf("UploadAttachment() unexpected error: %v", err)
	}
	if attachment.ID != "att-9" {
		t.Fatalf("attachment ID = %q, want att-9", attachment.ID)
	}
	if attachment.PageID != "42" {
		t.Fatalf("page ID = %q, want 42", attachment.PageID)
	}

	if err := client.DeleteAttachment(context.Background(), "att-9", "42"); err != nil {
		t.Fatalf("DeleteAttachment() unexpected error: %v", err)
	}

	if uploadCalls != 1 {
		t.Fatalf("upload calls = %d, want 1", uploadCalls)
	}
	if deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", deleteCalls)
	}
}

func TestDeleteAttachment_InvalidLegacyIDReturnsNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// Resolve UUID first
			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, `{"results":[]}`); err != nil { // Doesn't matter for this test as we want it to fall through or fail
				t.Fatalf("write response: %v", err)
			}
			return
		}

		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/wiki/api/v2/attachments/ffd70a27-0a48-48db-9662-24252c884152" {
			t.Fatalf("path = %s, want legacy attachment delete path", r.URL.Path)
		}

		w.WriteHeader(http.StatusBadRequest)
		if _, err := io.WriteString(w, `{"errors":[{"status":400,"code":"INVALID_REQUEST_PARAMETER","title":"Provided value {ffd70a27-0a48-48db-9662-24252c884152} for 'id' is not the correct type. Expected type is ContentId","detail":""}]}`); err != nil {
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

	err = client.DeleteAttachment(context.Background(), "ffd70a27-0a48-48db-9662-24252c884152", "123")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteAttachment() error = %v, want ErrNotFound", err)
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

func TestClient_VerboseDoesNotLeakToken(t *testing.T) {
	const apiToken = "super-secret-token-12345"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"results":[]}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	// Install a capturing slog handler at Debug level for this test.
	// slog.Debug is called by the client for every HTTP request.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	original := slog.Default()
	slog.SetDefault(slog.New(handler))
	t.Cleanup(func() { slog.SetDefault(original) })

	client, err := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "user@example.com",
		APIToken: apiToken,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, _ = client.ListSpaces(context.Background(), SpaceListOptions{Limit: 1})

	output := buf.String()

	if strings.Contains(output, apiToken) {
		t.Fatalf("verbose output leaks API token: %q", output)
	}
	if strings.Contains(output, "Authorization") {
		t.Fatalf("verbose output leaks Authorization header: %q", output)
	}
	// Should log the method and URL
	if !strings.Contains(output, "GET") {
		t.Errorf("verbose output missing HTTP method: %q", output)
	}
}
