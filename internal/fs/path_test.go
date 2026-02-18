package fs

import "testing"

func TestSanitizePathSegment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "basic", input: "My Page", want: "My-Page"},
		{name: "invalid chars", input: `bad:/\name*`, want: "bad-name"},
		{name: "reserved windows name", input: "CON", want: "CON-item"},
		{name: "empty fallback", input: "   ", want: "untitled"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizePathSegment(tc.input)
			if got != tc.want {
				t.Fatalf("SanitizePathSegment(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSanitizeMarkdownFilename(t *testing.T) {
	if got := SanitizeMarkdownFilename("Roadmap"); got != "Roadmap.md" {
		t.Fatalf("SanitizeMarkdownFilename() = %q, want Roadmap.md", got)
	}
	if got := SanitizeMarkdownFilename("Roadmap.md"); got != "Roadmap.md" {
		t.Fatalf("SanitizeMarkdownFilename() should keep .md suffix, got %q", got)
	}
}

