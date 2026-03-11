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

func TestForward_UsesDoubleSpaceHardBreaks(t *testing.T) {
	ctx := context.Background()
	adfJSON := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Line 1"},{"type":"hardBreak"},{"type":"text","text":"Line 2"}]}]}`)

	res, err := Forward(ctx, adfJSON, ForwardConfig{}, "test.md")
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	expected := "Line 1  \nLine 2\n"
	if res.Markdown != expected {
		t.Fatalf("expected double-space hard break markdown %q, got %q", expected, res.Markdown)
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

func TestNormalizeForwardMarkdown_UnescapesPlainParentheses(t *testing.T) {
	input := "(User 1) and \\(User 2\\).\n"
	want := "(User 1) and (User 2).\n"

	if got := normalizeForwardMarkdown(input); got != want {
		t.Fatalf("normalizeForwardMarkdown() = %q, want %q", got, want)
	}
}

func TestNormalizeForwardMarkdown_StripsInvisibleDateGuards(t *testing.T) {
	input := "Release date: 2026\u201103\u201109\n"
	want := "Release date: 2026-03-09\n"

	if got := normalizeForwardMarkdown(input); got != want {
		t.Fatalf("normalizeForwardMarkdown() = %q, want %q", got, want)
	}
}

func TestNormalizeForwardMarkdown_KeepsEscapedParenthesesInLinkDestinations(t *testing.T) {
	input := "[Spec](https://example.com/a\\(b\\)) and \\(User\\).\n"
	want := "[Spec](https://example.com/a\\(b\\)) and (User).\n"

	if got := normalizeForwardMarkdown(input); got != want {
		t.Fatalf("normalizeForwardMarkdown() = %q, want %q", got, want)
	}
}

func TestNormalizeForwardMarkdown_KeepsEscapedParenthesesInCode(t *testing.T) {
	input := "Use `\\(x\\)` in code and \\(y\\) in prose.\n"
	want := "Use `\\(x\\)` in code and (y) in prose.\n"

	if got := normalizeForwardMarkdown(input); got != want {
		t.Fatalf("normalizeForwardMarkdown() = %q, want %q", got, want)
	}
}

func TestForwardWithMediaHook(t *testing.T) {
	ctx := context.Background()
	// ADF with media
	adfJSON := []byte(`{"version":1,"type":"doc","content":[{"type":"mediaGroup","content":[{"type":"media","attrs":{"id":"media-1","type":"file","collection":"col"}}]}]}`)

	mediaHook := func(ctx context.Context, in adfconv.MediaRenderInput) (adfconv.MediaRenderOutput, error) {
		return adfconv.MediaRenderOutput{
			Markdown: "![Alt Text](assets/image.png)",
			Handled:  true,
		}, nil
	}

	res, err := Forward(ctx, adfJSON, ForwardConfig{MediaHook: mediaHook}, "test.md")
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	expected := "![Alt Text](assets/image.png)\n"
	if res.Markdown != expected {
		t.Errorf("Expected markdown %q, got %q", expected, res.Markdown)
	}
}

func TestNormalizeForwardMarkdown_Complex(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "mixed links and brackets",
			input:    "Intro \\[Page A\\]\\(./Page-A.md\\) and \\[Other\\].\n",
			expected: "Intro [Page A](./Page-A.md) and \\[Other\\].\n",
		},
		{
			name:     "multiple links",
			input:    "\\[L1\\]\\(P1.md\\) and \\[L2\\]\\(P2.md\\)\n",
			expected: "[L1](P1.md) and [L2](P2.md)\n",
		},
		{
			name:     "link with anchor",
			input:    "\\[Text\\]\\(path.md#anchor\\)\n",
			expected: "[Text](path.md#anchor)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeForwardMarkdown(tt.input); got != tt.expected {
				t.Errorf("normalizeForwardMarkdown() = %q, want %q", got, tt.expected)
			}
		})
	}
}
