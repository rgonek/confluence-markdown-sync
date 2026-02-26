package converter

import (
	"context"
	"testing"

	adfconv "github.com/rgonek/jira-adf-converter/converter"
)

func TestForward(t *testing.T) {
	ctx := context.Background()
	// Minimal ADF for "Hello World"
	adfJSON := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Hello World"}]}]}`)

	res, err := Forward(ctx, adfJSON, ForwardConfig{}, "test.md")
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	expected := "Hello World\n"
	if res.Markdown != expected {
		t.Errorf("Expected markdown %q, got %q", expected, res.Markdown)
	}
}

func TestForwardWithHook(t *testing.T) {
	ctx := context.Background()
	// ADF with link
	adfJSON := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Link","marks":[{"type":"link","attrs":{"href":"http://example.com"}}]}]}]}`)

	linkHook := func(ctx context.Context, in adfconv.LinkRenderInput) (adfconv.LinkRenderOutput, error) {
		return adfconv.LinkRenderOutput{
			Href:    "http://hooked.com",
			Title:   in.Title,
			Handled: true,
		}, nil
	}

	res, err := Forward(ctx, adfJSON, ForwardConfig{LinkHook: linkHook}, "test.md")
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	expected := "[Link](http://hooked.com)\n"
	if res.Markdown != expected {
		t.Errorf("Expected markdown %q, got %q", expected, res.Markdown)
	}
}

func TestNormalizeForwardMarkdown_UnescapesInlineLinks(t *testing.T) {
	input := "Intro \\[Page A\\]\\(./Page-A.md#overview\\) and \\[External\\]\\(https://example.com/docs\\).\n"
	want := "Intro [Page A](./Page-A.md#overview) and [External](https://example.com/docs).\n"

	if got := normalizeForwardMarkdown(input); got != want {
		t.Fatalf("normalizeForwardMarkdown() = %q, want %q", got, want)
	}
}

func TestNormalizeForwardMarkdown_LeavesNonLinksEscaped(t *testing.T) {
	input := "Use \\[brackets\\] for plain text.\n"

	if got := normalizeForwardMarkdown(input); got != input {
		t.Fatalf("normalizeForwardMarkdown() unexpectedly changed input: %q", got)
	}
}

func TestNormalizeForwardMarkdown_LeavesUnknownDestinationEscaped(t *testing.T) {
	input := "Keep \\[label\\]\\(not-a-link\\) as plain text.\n"

	if got := normalizeForwardMarkdown(input); got != input {
		t.Fatalf("normalizeForwardMarkdown() unexpectedly changed input: %q", got)
	}
}
