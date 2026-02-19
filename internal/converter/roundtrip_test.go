package converter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	adfconv "github.com/rgonek/jira-adf-converter/converter"
)

func TestRoundTripGolden(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("testdata", "roundtrip", "*.md"))
	if err != nil {
		t.Fatalf("glob fixtures: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no roundtrip fixtures found")
	}

	ctx := context.Background()

	for _, fixturePath := range fixtures {
		if strings.HasSuffix(fixturePath, ".golden.md") {
			continue
		}

		name := strings.TrimSuffix(filepath.Base(fixturePath), ".md")
		t.Run(name, func(t *testing.T) {
			inputMarkdown, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatalf("read fixture %s: %v", fixturePath, err)
			}

			reverse, err := Reverse(ctx, inputMarkdown, ReverseConfig{Strict: true}, filepath.ToSlash(filepath.Join("fixtures", name+".md")))
			if err != nil {
				t.Fatalf("reverse conversion failed: %v", err)
			}

			forward, err := Forward(ctx, reverse.ADF, ForwardConfig{}, filepath.ToSlash(filepath.Join("fixtures", name+".md")))
			if err != nil {
				t.Fatalf("forward conversion failed: %v", err)
			}

			expectedPath := filepath.Join("testdata", "roundtrip", name+".golden.md")
			expectedMarkdown, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Fatalf("read golden %s: %v", expectedPath, err)
			}

			got := strings.ReplaceAll(forward.Markdown, "\r\n", "\n")
			expected := strings.ReplaceAll(string(expectedMarkdown), "\r\n", "\n")
			if got != expected {
				t.Fatalf("round-trip markdown mismatch for %s\n--- got ---\n%s\n--- expected ---\n%s", name, got, expected)
			}

			if len(reverse.Warnings) > 0 {
				t.Fatalf("reverse warnings for %s: %s", name, formatWarningTypes(reverse.Warnings))
			}
			if len(forward.Warnings) > 0 {
				t.Fatalf("forward warnings for %s: %s", name, formatWarningTypes(forward.Warnings))
			}
		})
	}
}

func formatWarningTypes(warnings []adfconv.Warning) string {
	types := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		types = append(types, string(warning.Type))
	}
	return fmt.Sprintf("%v", types)
}
