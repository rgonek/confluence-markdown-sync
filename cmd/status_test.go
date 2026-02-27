package cmd

import (
	"bytes"
	"context"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
)

func TestStatusCmd(t *testing.T) {
	t.Run("isNotFoundError", func(t *testing.T) {
		if isNotFoundError(nil) {
			t.Fatalf("expected false for nil error")
		}

		if !isNotFoundError(confluence.ErrNotFound) {
			t.Fatalf("expected true for ErrNotFound")
		}
	})

	t.Run("printStatusSection and printStatusList", func(t *testing.T) {
		out := new(bytes.Buffer)

		added := []string{"add1.md"}
		modified := []string{"mod1.md", "mod2.md"}
		deleted := []string{}

		printStatusSection(out, "Test Section", added, modified, deleted)

		expectedSnippet := "Test Section"
		if !bytes.Contains(out.Bytes(), []byte(expectedSnippet)) {
			t.Fatalf("output missing expected text %q: %s", expectedSnippet, out.String())
		}

		if !bytes.Contains(out.Bytes(), []byte("added (1)")) {
			t.Fatalf("output missing expected text 'added (1)': %s", out.String())
		}

		if !bytes.Contains(out.Bytes(), []byte("modified (2)")) {
			t.Fatalf("output missing expected text 'modified (2)': %s", out.String())
		}

		if !bytes.Contains(out.Bytes(), []byte("deleted (0)")) {
			t.Fatalf("output missing expected text 'deleted (0)': %s", out.String())
		}
	})
}

// mockStatusRemote implements the StatusRemote interface for testing
type mockStatusRemote struct {
	space confluence.Space
	pages confluence.PageListResult
	page  confluence.Page
	err   error
}

func (m *mockStatusRemote) GetSpace(_ context.Context, _ string) (confluence.Space, error) {
	return m.space, m.err
}

func (m *mockStatusRemote) ListPages(_ context.Context, _ confluence.PageListOptions) (confluence.PageListResult, error) {
	return m.pages, m.err
}

func (m *mockStatusRemote) GetPage(_ context.Context, _ string) (confluence.Page, error) {
	return m.page, m.err
}

func TestListAllPagesForStatus(t *testing.T) {
	mock := &mockStatusRemote{
		pages: confluence.PageListResult{
			Pages: []confluence.Page{
				{ID: "1", Title: "Page 1"},
				{ID: "2", Title: "Page 2"},
			},
			NextCursor: "",
		},
	}

	ctx := context.Background()
	opts := confluence.PageListOptions{SpaceID: "123"}

	pages, err := listAllPagesForStatus(ctx, mock, opts)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
}

func TestComputeConflictAhead(t *testing.T) {
	t.Run("no overlap", func(t *testing.T) {
		result := computeConflictAhead(
			[]string{"a.md", "b.md"},
			[]string{"c.md", "d.md"},
		)
		if len(result) != 0 {
			t.Fatalf("expected no conflicts, got %v", result)
		}
	})

	t.Run("full overlap", func(t *testing.T) {
		result := computeConflictAhead(
			[]string{"a.md", "b.md"},
			[]string{"a.md", "b.md"},
		)
		if len(result) != 2 {
			t.Fatalf("expected 2 conflicts, got %v", result)
		}
	})

	t.Run("partial overlap", func(t *testing.T) {
		result := computeConflictAhead(
			[]string{"a.md", "b.md", "c.md"},
			[]string{"b.md", "d.md"},
		)
		if len(result) != 1 || result[0] != "b.md" {
			t.Fatalf("expected [b.md], got %v", result)
		}
	})

	t.Run("empty local modified", func(t *testing.T) {
		result := computeConflictAhead(nil, []string{"a.md"})
		if len(result) != 0 {
			t.Fatalf("expected no conflicts with empty localModified, got %v", result)
		}
	})

	t.Run("empty remote modified", func(t *testing.T) {
		result := computeConflictAhead([]string{"a.md"}, nil)
		if len(result) != 0 {
			t.Fatalf("expected no conflicts with empty remoteModified, got %v", result)
		}
	})
}
