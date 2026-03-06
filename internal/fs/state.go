package fs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StateFileName is the per-space sync state file.
const StateFileName = ".confluence-state.json"

var ErrStateConflictMarkers = errors.New("state file contains git conflict markers")

// SpaceState stores per-space sync metadata used for pull/push planning.
type SpaceState struct {
	LastPullHighWatermark string            `json:"last_pull_high_watermark,omitempty"`
	SpaceKey              string            `json:"space_key,omitempty"`
	PagePathIndex         map[string]string `json:"page_path_index,omitempty"`
	AttachmentIndex       map[string]string `json:"attachment_index,omitempty"`
	FolderPathIndex       map[string]string `json:"folder_path_index,omitempty"`
}

// NewSpaceState returns an initialized empty state object.
func NewSpaceState() SpaceState {
	return SpaceState{
		PagePathIndex:   map[string]string{},
		AttachmentIndex: map[string]string{},
		FolderPathIndex: map[string]string{},
	}
}

// StatePath returns the state file path for a space directory.
func StatePath(spaceDir string) string {
	return filepath.Join(spaceDir, StateFileName)
}

// LoadState reads .confluence-state.json from a space directory.
// Missing state files return an empty initialized state.
func LoadState(spaceDir string) (SpaceState, error) {
	path := StatePath(spaceDir)
	raw, err := os.ReadFile(path) //nolint:gosec // state path is derived from workspace spaceDir
	if err != nil {
		if os.IsNotExist(err) {
			return NewSpaceState(), nil
		}
		return SpaceState{}, err
	}
	if len(raw) == 0 {
		return NewSpaceState(), nil
	}

	var state SpaceState
	if err := json.Unmarshal(raw, &state); err != nil {
		if hasGitConflictMarkers(raw) {
			return SpaceState{}, fmt.Errorf("%w: %s", ErrStateConflictMarkers, path)
		}
		return SpaceState{}, fmt.Errorf("parse state file %s: %w", path, err)
	}
	state.normalize()
	if err := validateWatermark(state.LastPullHighWatermark); err != nil {
		return SpaceState{}, fmt.Errorf("invalid state watermark: %w", err)
	}
	return state, nil
}

// SaveState writes .confluence-state.json for a space directory.
func SaveState(spaceDir string, state SpaceState) error {
	state.normalize()
	if err := validateWatermark(state.LastPullHighWatermark); err != nil {
		return fmt.Errorf("invalid state watermark: %w", err)
	}

	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	raw = append(raw, '\n')

	if err := os.MkdirAll(spaceDir, 0o755); err != nil { //nolint:gosec // workspace directories intentionally use standard permissions
		return err
	}
	return os.WriteFile(StatePath(spaceDir), raw, 0o644) //nolint:gosec // state file is expected to be readable for local tooling
}

// FindAllStateFiles scans root for all .confluence-state.json files.
// It returns a map of space directory -> SpaceState.
func FindAllStateFiles(root string) (map[string]SpaceState, error) {
	states := make(map[string]SpaceState)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip hidden directories (like .git)
			if d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == StateFileName {
			dir := filepath.Dir(path)
			state, err := LoadState(dir)
			if err != nil {
				return err
			}
			states[dir] = state
		}
		return nil
	})
	return states, err
}

func (s *SpaceState) normalize() {
	s.SpaceKey = strings.TrimSpace(s.SpaceKey)
	s.PagePathIndex = normalizeStatePathMap(s.PagePathIndex)
	s.AttachmentIndex = normalizeStatePathMap(s.AttachmentIndex)
	s.FolderPathIndex = normalizeStatePathMap(s.FolderPathIndex)
}

func normalizeStatePathMap(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}

	out := make(map[string]string, len(in))
	for key, value := range in {
		normalizedKey := normalizeStatePath(key)
		if normalizedKey == "" {
			continue
		}
		out[normalizedKey] = strings.TrimSpace(value)
	}
	return out
}

func normalizeStatePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}

	path = filepath.ToSlash(filepath.Clean(path))
	path = strings.TrimPrefix(path, "./")
	if path == "." {
		return ""
	}
	return path
}

func validateWatermark(v string) error {
	if v == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		return err
	}
	return nil
}

func hasGitConflictMarkers(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	content := string(raw)
	return strings.Contains(content, "<<<<<<<") || strings.Contains(content, "=======") || strings.Contains(content, ">>>>>>>")
}

func IsStateConflictError(err error) bool {
	return errors.Is(err, ErrStateConflictMarkers)
}
