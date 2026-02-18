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

func TestNewClient_RequiresCoreConfig(t *testing.T) {
	_, err := NewClient(ClientConfig{})
	if err == nil {
		t.Fatal("NewClient() expected error, got nil")
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
		io.WriteString(w, `{"results":[{"id":"100","key":"ENG","name":"Engineering","type":"global"}],"meta":{"cursor":"next-cursor"}}`)
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
		io.WriteString(w, `{
			"results":[{"id":"77","title":"Roadmap","space":{"key":"ENG"},"version":{"number":8,"when":"2026-01-02T16:00:00Z"}}],
			"start":0,
			"limit":25,
			"size":1,
			"_links":{"next":"/wiki/rest/api/content/search?start=25"}
		}`)
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
			io.WriteString(w, `{"id":"task-9001"}`)
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

func TestDownloadAttachment_ResolvesAndDownloadsBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wiki/api/v2/attachments/att-1":
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET", r.Method)
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"id":"att-1","downloadLink":"/download/attachments/1/diagram.png"}`)
		case "/download/attachments/1/diagram.png":
			if r.Method != http.MethodGet {
				t.Fatalf("download method = %s, want GET", r.Method)
			}
			w.WriteHeader(http.StatusOK)
			io.WriteString(w, "binary-data")
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

	raw, err := client.DownloadAttachment(context.Background(), "att-1")
	if err != nil {
		t.Fatalf("DownloadAttachment() unexpected error: %v", err)
	}
	if string(raw) != "binary-data" {
		t.Fatalf("attachment bytes = %q, want %q", string(raw), "binary-data")
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
			io.WriteString(w, `{"results":[{"id":"att-9","title":"diagram.png","_links":{"webui":"/wiki/pages/viewpage.action?pageId=42"}}]}`)
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

	if err := client.DeleteAttachment(context.Background(), "att-9"); err != nil {
		t.Fatalf("DeleteAttachment() unexpected error: %v", err)
	}

	if uploadCalls != 1 {
		t.Fatalf("upload calls = %d, want 1", uploadCalls)
	}
	if deleteCalls != 1 {
		t.Fatalf("delete calls = %d, want 1", deleteCalls)
	}
}
