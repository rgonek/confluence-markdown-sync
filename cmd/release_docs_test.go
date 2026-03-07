package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseDocsMarkBetaStatus(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join("..", "README.md"))
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	usage, err := os.ReadFile(filepath.Join("..", "docs", "usage.md"))
	if err != nil {
		t.Fatalf("read usage guide: %v", err)
	}
	automation, err := os.ReadFile(filepath.Join("..", "docs", "automation.md"))
	if err != nil {
		t.Fatalf("read automation guide: %v", err)
	}

	for path, content := range map[string]string{
		"README.md":          string(readme),
		"docs/usage.md":      string(usage),
		"docs/automation.md": string(automation),
	} {
		if !strings.Contains(strings.ToLower(content), "beta") {
			t.Fatalf("expected %s to clearly label the product as beta", path)
		}
	}
}
