package fs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// StateFileName is the per-space sync state file.
const StateFileName = ".confluence-state.json"

// SpaceState stores per-space sync metadata used for pull/push planning.
type SpaceState struct {
	LastPullHighWatermark string            `json:"last_pull_high_watermark,omitempty"`
	PagePathIndex         map[string]string `json:"page_path_index,omitempty"`
	AttachmentIndex       map[string]string `json:"attachment_index,omitempty"`
}

// NewSpaceState returns an initialized empty state object.
func NewSpaceState() SpaceState {
	return SpaceState{
		PagePathIndex:   map[string]string{},
		AttachmentIndex: map[string]string{},
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
	raw, err := os.ReadFile(path)
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

	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(StatePath(spaceDir), raw, 0o644)
}

func (s *SpaceState) normalize() {
	if s.PagePathIndex == nil {
		s.PagePathIndex = map[string]string{}
	}
	if s.AttachmentIndex == nil {
		s.AttachmentIndex = map[string]string{}
	}
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
