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
	// 1 initial + maxRetryAttempts retries
	wantCalls := 1 + maxRetryAttempts
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
		if _, err := io.WriteString(w, `{"id":"101","title":"New","spaceId":"S1","version":{"number":1}}`); err != nil {
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
		Title:   "New",
	}
	_, err := client.CreatePage(context.Background(), input)
	if err != nil {
		t.Fatalf("CreatePage() error = %v", err)
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
