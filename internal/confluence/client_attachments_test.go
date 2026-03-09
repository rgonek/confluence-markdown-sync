//nolint:errcheck // test handlers intentionally ignore best-effort response write errors
package confluence

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

func TestListAttachments_PaginatesAndMapsFields(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")

		switch callCount {
		case 1:
			if r.URL.Path != "/wiki/api/v2/pages/123/attachments" {
				t.Fatalf("first call path = %s", r.URL.Path)
			}
			if _, err := io.WriteString(w, `{
				"results":[{"id":"att-1","fileId":"file-1","title":"diagram.png","mediaType":"image/png"}],
				"_links":{"next":"/wiki/api/v2/pages/123/attachments?cursor=next-token"}
			}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		case 2:
			if !strings.Contains(r.URL.RawQuery, "cursor=next-token") {
				t.Fatalf("second call query = %s", r.URL.RawQuery)
			}
			if _, err := io.WriteString(w, `{"results":[{"id":"att-2","fileId":"file-2","filename":"spec.pdf","mediaType":"application/pdf"}]}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		default:
			t.Fatalf("unexpected call %d", callCount)
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

	attachments, err := client.ListAttachments(context.Background(), "123")
	if err != nil {
		t.Fatalf("ListAttachments() error: %v", err)
	}
	if len(attachments) != 2 {
		t.Fatalf("attachment count = %d, want 2", len(attachments))
	}
	if attachments[0].ID != "att-1" || attachments[0].Filename != "diagram.png" {
		t.Fatalf("first attachment = %+v", attachments[0])
	}
	if attachments[0].FileID != "file-1" {
		t.Fatalf("first attachment file id = %q, want file-1", attachments[0].FileID)
	}
	if attachments[1].ID != "att-2" || attachments[1].Filename != "spec.pdf" {
		t.Fatalf("second attachment = %+v", attachments[1])
	}
	if attachments[1].FileID != "file-2" {
		t.Fatalf("second attachment file id = %q, want file-2", attachments[1].FileID)
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
			if _, err := io.WriteString(w, `{"results":[{"id":"att-9","fileId":"file-9","title":"diagram.png","_links":{"webui":"/wiki/pages/viewpage.action?pageId=42"}}]}`); err != nil {
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
	if attachment.FileID != "file-9" {
		t.Fatalf("file ID = %q, want file-9", attachment.FileID)
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
