package fs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadState_MissingReturnsInitializedState(t *testing.T) {
	spaceDir := filepath.Join(t.TempDir(), "ENG")

	state, err := LoadState(spaceDir)
	if err != nil {
		t.Fatalf("LoadState() unexpected error: %v", err)
	}
	if state.PagePathIndex == nil {
		t.Fatal("PagePathIndex must be initialized")
	}
	if state.AttachmentIndex == nil {
		t.Fatal("AttachmentIndex must be initialized")
	}
}

func TestSaveAndLoadState_PersistsData(t *testing.T) {
	spaceDir := filepath.Join(t.TempDir(), "ENG")
	want := SpaceState{
		LastPullHighWatermark: "2026-02-18T09:30:00Z",
		PagePathIndex: map[string]string{
			"ENG/Home.md": "1001",
		},
		AttachmentIndex: map[string]string{
			"ENG/assets/1001/200-diagram.png": "200",
		},
	}

	if err := SaveState(spaceDir, want); err != nil {
		t.Fatalf("SaveState() unexpected error: %v", err)
	}

	got, err := LoadState(spaceDir)
	if err != nil {
		t.Fatalf("LoadState() unexpected error: %v", err)
	}

	if got.LastPullHighWatermark != want.LastPullHighWatermark {
		t.Fatalf("LastPullHighWatermark = %q, want %q", got.LastPullHighWatermark, want.LastPullHighWatermark)
	}
	if got.PagePathIndex["ENG/Home.md"] != "1001" {
		t.Fatalf("PagePathIndex[ENG/Home.md] = %q, want 1001", got.PagePathIndex["ENG/Home.md"])
	}
	if got.AttachmentIndex["ENG/assets/1001/200-diagram.png"] != "200" {
		t.Fatalf(
			"AttachmentIndex[...] = %q, want 200",
			got.AttachmentIndex["ENG/assets/1001/200-diagram.png"],
		)
	}
}

func TestSaveState_InvalidWatermarkFails(t *testing.T) {
	spaceDir := filepath.Join(t.TempDir(), "ENG")

	err := SaveState(spaceDir, SpaceState{
		LastPullHighWatermark: "bad-time",
	})
	if err == nil {
		t.Fatal("SaveState() expected error for invalid watermark, got nil")
	}
}

func TestLoadState_InvalidWatermarkFails(t *testing.T) {
	spaceDir := filepath.Join(t.TempDir(), "ENG")

	if err := SaveState(spaceDir, SpaceState{
		LastPullHighWatermark: "2026-02-18T09:30:00Z",
	}); err != nil {
		t.Fatalf("SaveState() unexpected error: %v", err)
	}

	path := StatePath(spaceDir)
	raw := []byte(`{"last_pull_high_watermark":"not-rfc3339"}`)
	if err := os.WriteFile(path, raw, 0o644); err != nil { //nolint:gosec // test fixture writes non-sensitive temp file
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}

	if _, err := LoadState(spaceDir); err == nil {
		t.Fatal("LoadState() expected error for invalid watermark, got nil")
	}
}

func TestLoadState_GitConflictMarkersReturnsTypedError(t *testing.T) {
	spaceDir := filepath.Join(t.TempDir(), "ENG")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("MkdirAll() unexpected error: %v", err)
	}

	raw := []byte(`<<<<<<< HEAD
{"space_key":"ENG","page_path_index":{"a.md":"1"}}
=======
{"space_key":"ENG","page_path_index":{"b.md":"2"}}
>>>>>>> sync/ENG/20260226T120000Z
`)
	if err := os.WriteFile(StatePath(spaceDir), raw, 0o644); err != nil { //nolint:gosec // test fixture writes non-sensitive temp file
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}

	_, err := LoadState(spaceDir)
	if err == nil {
		t.Fatal("LoadState() expected conflict marker error, got nil")
	}
	if !errors.Is(err, ErrStateConflictMarkers) {
		t.Fatalf("LoadState() error = %v, want ErrStateConflictMarkers", err)
	}
	if !IsStateConflictError(err) {
		t.Fatalf("IsStateConflictError(%v) = false, want true", err)
	}
}
