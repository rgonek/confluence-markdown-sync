package fs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAndLoadState_NormalizesPathSeparators(t *testing.T) {
	spaceDir := filepath.Join(t.TempDir(), "ENG")

	if err := SaveState(spaceDir, SpaceState{
		PagePathIndex: map[string]string{
			`Root\Root.md`: "1",
		},
		AttachmentIndex: map[string]string{
			`assets\1\diagram.png`: "att-1",
		},
		FolderPathIndex: map[string]string{
			`Root\Section`: "folder-1",
		},
	}); err != nil {
		t.Fatalf("SaveState() unexpected error: %v", err)
	}

	raw, err := os.ReadFile(StatePath(spaceDir))
	if err != nil {
		t.Fatalf("ReadFile() unexpected error: %v", err)
	}
	if strings.Contains(string(raw), `\\`) {
		t.Fatalf("state file should persist slash-normalized paths, got:\n%s", string(raw))
	}

	state, err := LoadState(spaceDir)
	if err != nil {
		t.Fatalf("LoadState() unexpected error: %v", err)
	}

	if got := state.PagePathIndex["Root/Root.md"]; got != "1" {
		t.Fatalf("PagePathIndex[Root/Root.md] = %q, want 1", got)
	}
	if got := state.AttachmentIndex["assets/1/diagram.png"]; got != "att-1" {
		t.Fatalf("AttachmentIndex[assets/1/diagram.png] = %q, want att-1", got)
	}
	if got := state.FolderPathIndex["Root/Section"]; got != "folder-1" {
		t.Fatalf("FolderPathIndex[Root/Section] = %q, want folder-1", got)
	}
}
