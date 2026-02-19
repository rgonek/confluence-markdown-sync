package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Client handles git operations.
type Client struct {
	// RootDir is the root of the git repository.
	RootDir string
}

// NewClient creates a new git client.
// It verifies that the current directory is within a git repository.
func NewClient() (*Client, error) {
	root, err := RunGit("", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("find git root: %w", err)
	}
	return &Client{RootDir: strings.TrimSpace(root)}, nil
}

// ScopePath resolves a path relative to the repository root.
// It returns an error if the path is outside the repository.
func (c *Client) ScopePath(absPath string) (string, error) {
	rel, err := filepath.Rel(c.RootDir, absPath)
	if err != nil {
		return "", err
	}
	rel = filepath.Clean(rel)
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %s is outside repository root %s", absPath, c.RootDir)
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return ".", nil
	}
	return rel, nil
}

// Run executes a git command in the repository root.
func (c *Client) Run(args ...string) (string, error) {
	return RunGit(c.RootDir, args...)
}

// RunGit executes a git command in a specific directory.
func RunGit(workdir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if strings.TrimSpace(workdir) != "" {
		cmd.Dir = workdir
	}
	// Set generic env vars if needed, e.g. LANG=C
	cmd.Env = append(os.Environ(), "LANG=C", "LC_ALL=C")

	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return "", fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s failed: %s", strings.Join(args, " "), msg)
	}
	return string(out), nil
}
