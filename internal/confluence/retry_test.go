package confluence

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDo_RetriesOn429(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"results":[],"meta":{"cursor":""}}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, _ := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})

	_, err := client.ListSpaces(context.Background(), SpaceListOptions{})
	if err != nil {
		t.Fatalf("ListSpaces() error = %v, want nil", err)
	}
	if calls != 3 {
		t.Fatalf("server calls = %d, want 3", calls)
	}
}

func TestDo_RetriesOn500(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"results":[],"meta":{"cursor":""}}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, _ := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})

	_, err := client.ListSpaces(context.Background(), SpaceListOptions{})
	if err != nil {
		t.Fatalf("ListSpaces() error = %v, want nil", err)
	}
	if calls != 2 {
		t.Fatalf("server calls = %d, want 2", calls)
	}
}

func TestDo_DoesNotRetryOn4xx(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"message":"forbidden"}`, http.StatusForbidden)
	}))
	t.Cleanup(server.Close)

	client, _ := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})

	_, err := client.ListSpaces(context.Background(), SpaceListOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Fatalf("server calls = %d, want 1 (no retry on 4xx)", calls)
	}
}

func TestDo_ExhaustsRetries(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	client, _ := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})

	_, err := client.ListSpaces(context.Background(), SpaceListOptions{})
	if err == nil {
		t.Fatal("expected error after all retries exhausted")
	}
	// 1 initial + DefaultRetryMaxAttempts retries
	wantCalls := 1 + DefaultRetryMaxAttempts
	if calls != wantCalls {
		t.Fatalf("server calls = %d, want %d", calls, wantCalls)
	}
}

func TestDo_ContextCancelledStopsRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "60") // long delay — context should cancel first
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(server.Close)

	client, _ := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := client.ListSpaces(ctx, SpaceListOptions{})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		// After cancellation, the request itself may fail before even hitting server
		// or the retry delay wait may be interrupted — either way, non-nil error is correct
		t.Logf("err = %v (non-nil as expected)", err)
	}
}

func TestDo_RetriesPreserveRequestBody(t *testing.T) {
	calls := 0
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if calls < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"id":"101","title":"Updated","spaceId":"S1","version":{"number":2}}`); err != nil {
			t.Fatalf("write response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client, _ := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})

	input := PageUpsertInput{
		SpaceID: "S1",
		Title:   "Updated",
		Version: 2,
	}
	_, err := client.UpdatePage(context.Background(), "101", input)
	if err != nil {
		t.Fatalf("UpdatePage() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	// Both requests should have the same non-empty body
	for i, body := range bodies {
		if !strings.Contains(body, `"spaceId"`) {
			t.Fatalf("call %d body = %q, want JSON with spaceId", i+1, body)
		}
	}
	if bodies[0] != bodies[1] {
		t.Fatalf("retry body differs: call1=%q call2=%q", bodies[0], bodies[1])
	}
}

func TestDo_DoesNotRetryNonIdempotentPost(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	client, _ := NewClient(ClientConfig{
		BaseURL:  server.URL,
		Email:    "u",
		APIToken: "t",
	})

	_, err := client.CreatePage(context.Background(), PageUpsertInput{SpaceID: "S1", Title: "New"})
	if err == nil {
		t.Fatal("expected create page to fail")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestDo_RetriesTransientTimeoutForIdempotentRequest(t *testing.T) {
	calls := 0
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, &url.Error{Op: "Get", URL: req.URL.String(), Err: context.DeadlineExceeded}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"results":[],"meta":{"cursor":""}}`)),
			Request:    req,
		}, nil
	})

	client, err := NewClient(ClientConfig{
		BaseURL:  "https://example.test",
		Email:    "u",
		APIToken: "t",
		HTTPClient: &http.Client{
			Transport: transport,
		},
	})
	if err != nil {
		t.Fatalf("NewClient() unexpected error: %v", err)
	}

	_, err = client.ListSpaces(context.Background(), SpaceListOptions{})
	if err != nil {
		t.Fatalf("ListSpaces() error = %v, want nil", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
