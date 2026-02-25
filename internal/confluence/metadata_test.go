package confluence

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClient_ContentStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/wiki/rest/api/content/123/state", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, `{"name": "Ready to review", "color": "yellow", "id": 80}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		case http.MethodPut:
			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, `{"name": "Ready to review"}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client, err := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "test@example.com",
		APIToken: "token",
	})
	if err != nil {
		t.Fatalf("NewClient() failed: %v", err)
	}

	ctx := context.Background()

	// Test Get
	status, err := client.GetContentStatus(ctx, "123")
	if err != nil {
		t.Fatalf("GetContentStatus() failed: %v", err)
	}
	if status != "Ready to review" {
		t.Errorf("got %q, want %q", status, "Ready to review")
	}

	// Test Set
	err = client.SetContentStatus(ctx, "123", "Ready to review")
	if err != nil {
		t.Fatalf("SetContentStatus() failed: %v", err)
	}

	// Test Delete
	err = client.DeleteContentStatus(ctx, "123")
	if err != nil {
		t.Fatalf("DeleteContentStatus() failed: %v", err)
	}
}

func TestClient_Labels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/wiki/rest/api/content/123/label", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, `{"results": [{"prefix": "global", "name": "arch"}, {"prefix": "global", "name": "api"}]}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			if _, err := io.WriteString(w, `{"results": []}`); err != nil {
				t.Fatalf("write response: %v", err)
			}
		case http.MethodDelete:
			if r.URL.Query().Get("name") != "arch" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	client, err := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "test@example.com",
		APIToken: "token",
	})
	if err != nil {
		t.Fatalf("NewClient() failed: %v", err)
	}

	ctx := context.Background()

	// Test Get
	labels, err := client.GetLabels(ctx, "123")
	if err != nil {
		t.Fatalf("GetLabels() failed: %v", err)
	}
	if len(labels) != 2 || labels[0] != "arch" || labels[1] != "api" {
		t.Errorf("got labels %v, want [arch api]", labels)
	}

	// Test Add
	err = client.AddLabels(ctx, "123", []string{"arch", "api"})
	if err != nil {
		t.Fatalf("AddLabels() failed: %v", err)
	}

	// Test Remove
	err = client.RemoveLabel(ctx, "123", "arch")
	if err != nil {
		t.Fatalf("RemoveLabel() failed: %v", err)
	}
}
