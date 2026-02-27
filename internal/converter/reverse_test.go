package converter

import (
	"context"
	"strings"
	"testing"

	mdconv "github.com/rgonek/jira-adf-converter/mdconverter"
)

func TestReverse(t *testing.T) {
	ctx := context.Background()
	markdown := []byte("Hello World\n")

	res, err := Reverse(ctx, markdown, ReverseConfig{}, "test.md")
	if err != nil {
		t.Fatalf("Reverse failed: %v", err)
	}

	if len(res.ADF) == 0 {
		t.Error("Expected non-empty ADF")
	}
}

func TestReverseWithHook(t *testing.T) {
	ctx := context.Background()
	markdown := []byte("[Link](http://example.com)\n")

	linkHook := func(ctx context.Context, in mdconv.LinkParseInput) (mdconv.LinkParseOutput, error) {
		return mdconv.LinkParseOutput{
			Destination: "http://hooked.com",
			Handled:     true,
		}, nil
	}

	res, err := Reverse(ctx, markdown, ReverseConfig{LinkHook: linkHook}, "test.md")
	if err != nil {
		t.Fatalf("Reverse failed: %v", err)
	}

	// Simple check for changed URL in ADF
	adfStr := string(res.ADF)
	if !strings.Contains(adfStr, "http://hooked.com") {
		t.Errorf("Expected ADF to contain 'http://hooked.com', got %s", adfStr)
	}
}

func TestReverseWithMediaHook(t *testing.T) {
	ctx := context.Background()
	markdown := []byte("![Alt](assets/image.png)\n")

	mediaHook := func(ctx context.Context, in mdconv.MediaParseInput) (mdconv.MediaParseOutput, error) {
		return mdconv.MediaParseOutput{
			MediaType: "image",
			ID:        "media-1",
			Handled:   true,
			Alt:       in.Alt,
		}, nil
	}

	res, err := Reverse(ctx, markdown, ReverseConfig{MediaHook: mediaHook}, "test.md")
	if err != nil {
		t.Fatalf("Reverse failed: %v", err)
	}

	// Simple check for ID in ADF
	adfStr := string(res.ADF)
	if !strings.Contains(adfStr, "media-1") {
		t.Errorf("Expected ADF to contain 'media-1', got %s", adfStr)
	}
}

func TestReverseStrict(t *testing.T) {
	ctx := context.Background()
	markdown := []byte("[Broken Link](broken.md)\n")

	// In strict mode, unresolved links should cause an error if the hook returns ErrUnresolved
	linkHook := func(ctx context.Context, in mdconv.LinkParseInput) (mdconv.LinkParseOutput, error) {
		return mdconv.LinkParseOutput{}, mdconv.ErrUnresolved
	}

	_, err := Reverse(ctx, markdown, ReverseConfig{LinkHook: linkHook, Strict: true}, "test.md")
	if err == nil {
		t.Error("Expected error in strict mode for unresolved link, got nil")
	}
}
