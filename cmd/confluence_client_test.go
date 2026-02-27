package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

func TestBuildUserAgent(t *testing.T) {
	runParallelCommandTest(t)
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "release", version: "1.2.3", want: "conf/1.2.3"},
		{name: "trimmed", version: " 2.0.0 ", want: "conf/2.0.0"},
		{name: "empty falls back", version: "", want: "conf/dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := buildUserAgent(tt.version); got != tt.want {
				t.Fatalf("buildUserAgent(%q) = %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}

func TestNewConfluenceClientFromConfig_UsesVersionedUserAgent(t *testing.T) {
	runParallelCommandTest(t)
	var seenUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"results":[]}`)
	}))
	t.Cleanup(server.Close)

	previousVersion := Version
	Version = "9.9.9"
	t.Cleanup(func() { Version = previousVersion })

	client, err := newConfluenceClientFromConfig(&config.Config{
		Domain:   server.URL,
		Email:    "user@example.com",
		APIToken: "token-123",
	})
	if err != nil {
		t.Fatalf("newConfluenceClientFromConfig() error: %v", err)
	}
	t.Cleanup(func() {
		_ = client.Close()
	})

	if _, err := client.ListSpaces(context.Background(), confluence.SpaceListOptions{Limit: 1}); err != nil {
		t.Fatalf("ListSpaces() error: %v", err)
	}

	if seenUserAgent != "conf/9.9.9" {
		t.Fatalf("user agent = %q, want conf/9.9.9", seenUserAgent)
	}
}
