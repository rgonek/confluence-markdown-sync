//nolint:errcheck // test handlers intentionally ignore best-effort response write errors
package confluence

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeAPIErrorMessage_UsesErrorsObjectTitle(t *testing.T) {
	body := []byte(`{"errors":[{"status":400,"code":"INVALID_REQUEST_PARAMETER","title":"Provided value for 'id' is not the correct type. Expected type is ContentId","detail":""}]}`)

	got := decodeAPIErrorMessage(body)
	if !strings.Contains(got, "Expected type is ContentId") {
		t.Fatalf("decodeAPIErrorMessage() = %q, want error title", got)
	}
}

func TestDecodeAPIErrorMessage_UsesNestedDataErrors(t *testing.T) {
	body := []byte(`{"data":{"errors":[{"message":"ADF payload invalid"}]}}`)

	got := decodeAPIErrorMessage(body)
	if got != "ADF payload invalid" {
		t.Fatalf("decodeAPIErrorMessage() = %q, want %q", got, "ADF payload invalid")
	}
}

func TestDecodeAPIErrorMessage_ErrorCodeKey(t *testing.T) {
	// Body with a known "code" key should return the enriched hint.
	body := []byte(`{"code": "INVALID_IMAGE", "message": ""}`)
	got := decodeAPIErrorMessage(body)
	if !strings.Contains(strings.ToLower(got), "image") {
		t.Errorf("decodeAPIErrorMessage with code=INVALID_IMAGE = %q, want to contain 'image'", got)
	}
}

func TestDecodeAPIErrorMessage_TitleAlreadyExists(t *testing.T) {
	body := []byte(`{"message": "TITLE_ALREADY_EXISTS"}`)
	got := decodeAPIErrorMessage(body)
	if !strings.Contains(strings.ToLower(got), "title") {
		t.Errorf("decodeAPIErrorMessage TITLE_ALREADY_EXISTS = %q, want to contain 'title'", got)
	}
}

func TestConfluenceStatusHint(t *testing.T) {
	cases := []struct {
		code int
		want string // empty means no hint expected
	}{
		{http.StatusUnauthorized, "authentication failed"},
		{http.StatusForbidden, "permission denied"},
		{http.StatusConflict, "version conflict"},
		{http.StatusUnprocessableEntity, "rejected by confluence"},
		{http.StatusTooManyRequests, "rate limited"},
		{http.StatusServiceUnavailable, "temporarily unavailable"},
		{http.StatusRequestEntityTooLarge, "too large"},
		{http.StatusOK, ""},
		{http.StatusInternalServerError, ""},
	}
	for _, tc := range cases {
		hint := confluenceStatusHint(tc.code)
		if tc.want == "" {
			if hint != "" {
				t.Errorf("confluenceStatusHint(%d) = %q, want empty", tc.code, hint)
			}
			continue
		}
		if !strings.Contains(strings.ToLower(hint), tc.want) {
			t.Errorf("confluenceStatusHint(%d) = %q, want to contain %q", tc.code, hint, tc.want)
		}
	}
}

func TestMapConfluenceErrorCode(t *testing.T) {
	cases := []struct {
		input string
		want  string // substring expected in result
	}{
		{"INVALID_IMAGE", "image"},
		{"invalid_image", "image"}, // case-insensitive
		{"MACRO_NOT_FOUND", "macro"},
		{"MACRONOTFOUND", "macro"},
		{"TITLE_ALREADY_EXISTS", "title"},
		{"PERMISSION_DENIED", "permission"},
		{"CONTENT_STALE", "pull"},
		{"PARENT_PAGE_NOT_FOUND", "parent"},
		{"INVALID_REQUEST_PARAMETER", "invalid"},
		{"UNKNOWN_CODE_XYZ", ""},
		{"", ""},
	}
	for _, tc := range cases {
		got := mapConfluenceErrorCode(tc.input)
		if tc.want == "" {
			if got != "" {
				t.Errorf("mapConfluenceErrorCode(%q) = %q, want empty", tc.input, got)
			}
			continue
		}
		if !strings.Contains(strings.ToLower(got), tc.want) {
			t.Errorf("mapConfluenceErrorCode(%q) = %q, want to contain %q", tc.input, got, tc.want)
		}
	}
}

func TestAPIError_FallsBackToStatusHint(t *testing.T) {
	err := &APIError{
		StatusCode: http.StatusForbidden,
		Method:     "PUT",
		URL:        "https://example.test/page/1",
		Message:    "",
		Body:       "",
	}
	msg := err.Error()
	if !strings.Contains(strings.ToLower(msg), "permission") {
		t.Errorf("APIError.Error() = %q, want to contain 'permission'", msg)
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
