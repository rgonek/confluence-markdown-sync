package config

import "strings"

// TargetMode indicates whether a TARGET refers to a file or a space.
type TargetMode int

const (
	// TargetModeSpace means the target is a Confluence space key.
	TargetModeSpace TargetMode = iota
	// TargetModeFile means the target is a path to a Markdown file.
	TargetModeFile
)

// Target is the parsed representation of a [TARGET] argument.
type Target struct {
	Mode  TargetMode
	Value string // SpaceKey or file path
}

// ParseTarget parses a raw [TARGET] argument.
// Rules:
//   - Empty string returns space mode with empty value (caller resolves from CWD).
//   - Ends with ".md"  => file mode.
//   - Otherwise        => space mode (SPACE_KEY).
func ParseTarget(raw string) Target {
	if strings.HasSuffix(raw, ".md") {
		return Target{Mode: TargetModeFile, Value: raw}
	}
	return Target{Mode: TargetModeSpace, Value: raw}
}

// IsFile reports whether the target is a Markdown file.
func (t Target) IsFile() bool { return t.Mode == TargetModeFile }

// IsSpace reports whether the target is a space key.
func (t Target) IsSpace() bool { return t.Mode == TargetModeSpace }
