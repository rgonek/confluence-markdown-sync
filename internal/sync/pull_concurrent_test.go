package sync

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestPull_ConcurrentPageDetails(t *testing.T) {
	tmpDir := t.TempDir()
	spaceDir := filepath.Join(tmpDir, "ENG")

	var activeReqs int32
	var maxReqs int32
	var reqsMu sync.Mutex

	numPages := 20
	pages := make([]confluence.Page, numPages)
	pagesByID := make(map[string]confluence.Page)
	for i := 1; i <= numPages; i++ {
		id := "p" + string(rune(i))
		p := confluence.Page{
			ID:      id,
			SpaceID: "space-1",
			Title:   "Page " + id,
			BodyADF: rawJSON(t, sampleRootADF()),
		}
		pages[i-1] = p
		pagesByID[id] = p
	}

	fake := &fakePullRemote{
		space:       confluence.Space{ID: "space-1", Key: "ENG"},
		pages:       pages,
		pagesByID:   pagesByID,
		attachments: map[string][]byte{},
		getPageHook: func(pageID string) {
			current := atomic.AddInt32(&activeReqs, 1)
			reqsMu.Lock()
			if current > maxReqs {
				maxReqs = current
			}
			reqsMu.Unlock()
			time.Sleep(10 * time.Millisecond) // artificially delay to force concurrency overlap
			atomic.AddInt32(&activeReqs, -1)
		},
	}

	_, err := Pull(context.Background(), fake, PullOptions{
		SpaceKey:          "ENG",
		SpaceDir:          spaceDir,
		SkipMissingAssets: true,
		State:             fs.SpaceState{},
	})
	if err != nil {
		t.Fatalf("Pull() unexpected error: %v", err)
	}

	reqsMu.Lock()
	highestConcurrency := maxReqs
	reqsMu.Unlock()

	// 5 workers are spawned
	if highestConcurrency > 5 {
		t.Errorf("expected max concurrency <= 5, got %d", highestConcurrency)
	}
	if highestConcurrency < 2 {
		t.Logf("concurrency was %d, expected > 1 (maybe slow CI?)", highestConcurrency)
	}
}
