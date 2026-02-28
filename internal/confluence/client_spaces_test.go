//nolint:errcheck // test handlers intentionally ignore best-effort response write errors
package confluence

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
