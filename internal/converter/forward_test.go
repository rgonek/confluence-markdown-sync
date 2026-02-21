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

func TestForwardWithPlaceholder(t *testing.T) {
	ctx := context.Background()
	// ADF with placeholder
	adfJSON := []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Start "},{"type":"placeholder","attrs":{"text":"instructional text"}},{"type":"text","text":" End"}]}]}`)

	res, err := Forward(ctx, adfJSON, ForwardConfig{}, "test.md")
	if err != nil {
		t.Fatalf("Forward failed: %v", err)
	}

	expected := "Start  End\n"
	if res.Markdown != expected {
		t.Errorf("Expected markdown %q, got %q", expected, res.Markdown)
	}

	if len(res.Warnings) > 0 {
		t.Errorf("Expected no warnings, got %v", res.Warnings)
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
