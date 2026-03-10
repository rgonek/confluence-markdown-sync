package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/git"
)

const workspaceLockFilename = "confluence-sync.lock.json"
const workspaceLockStaleAfter = 15 * time.Minute

type workspaceLockMetadata struct {
	Command   string `json:"command"`
	PID       int    `json:"pid"`
	Hostname  string `json:"hostname,omitempty"`
	CreatedAt string `json:"created_at"`
}

type workspaceLock struct {
	path      string
	reentrant bool
}

var (
	workspaceLockMu        sync.Mutex
	workspaceLockRefByPath = map[string]int{}
)

func acquireWorkspaceLock(command string) (*workspaceLock, error) {
	client, err := git.NewClient()
	if err != nil {
		return nil, err
	}

	lockPath := filepath.Join(client.RootDir, ".git", workspaceLockFilename)
	hostname, _ := os.Hostname()
	payload := workspaceLockMetadata{
		Command:   strings.TrimSpace(command),
		PID:       os.Getpid(),
		Hostname:  strings.TrimSpace(hostname),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode workspace lock: %w", err)
	}

	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			meta, _ := readWorkspaceLock(lockPath)
			if meta != nil && meta.PID == os.Getpid() {
				workspaceLockMu.Lock()
				workspaceLockRefByPath[lockPath]++
				workspaceLockMu.Unlock()
				return &workspaceLock{path: lockPath, reentrant: true}, nil
			}
			if meta != nil {
				return nil, fmt.Errorf(
					"another sync command is already mutating this repository (%s pid=%d started=%s); wait for it to finish or inspect/remove %s if it is stale",
					strings.TrimSpace(meta.Command),
					meta.PID,
					strings.TrimSpace(meta.CreatedAt),
					lockPath,
				)
			}
			return nil, fmt.Errorf("another sync command is already mutating this repository; inspect/remove %s if it is stale", lockPath)
		}
		return nil, fmt.Errorf("create workspace lock: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(raw); err != nil {
		_ = os.Remove(lockPath)
		return nil, fmt.Errorf("write workspace lock: %w", err)
	}
	workspaceLockMu.Lock()
	workspaceLockRefByPath[lockPath] = 1
	workspaceLockMu.Unlock()
	return &workspaceLock{path: lockPath}, nil
}

func (l *workspaceLock) Release() error {
	if l == nil || strings.TrimSpace(l.path) == "" {
		return nil
	}
	workspaceLockMu.Lock()
	refs := workspaceLockRefByPath[l.path]
	if refs > 1 {
		workspaceLockRefByPath[l.path] = refs - 1
		workspaceLockMu.Unlock()
		return nil
	}
	delete(workspaceLockRefByPath, l.path)
	workspaceLockMu.Unlock()
	if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove workspace lock: %w", err)
	}
	return nil
}

func workspaceLockInfo() (string, *workspaceLockMetadata, error) {
	client, err := git.NewClient()
	if err != nil {
		return "", nil, err
	}
	lockPath := filepath.Join(client.RootDir, ".git", workspaceLockFilename)
	meta, err := readWorkspaceLock(lockPath)
	if err != nil {
		return lockPath, nil, err
	}
	return lockPath, meta, nil
}

func readWorkspaceLock(path string) (*workspaceLockMetadata, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // lock path is fixed under .git
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var meta workspaceLockMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return &workspaceLockMetadata{}, nil
	}
	return &meta, nil
}

func workspaceLockAge(meta *workspaceLockMetadata) time.Duration {
	if meta == nil {
		return 0
	}
	createdAt, err := time.Parse(time.RFC3339, strings.TrimSpace(meta.CreatedAt))
	if err != nil {
		return 0
	}
	return time.Since(createdAt)
}
