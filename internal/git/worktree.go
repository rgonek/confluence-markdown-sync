package git

import (
	"fmt"
	"os"
)

// AddWorktree creates a new worktree at the specified path for the given branch.
// If the branch doesn't exist, it creates a new orphan branch if specified or checkouts an existing one.
// Actually, usually we want `git worktree add <path> <branch>`.
func (c *Client) AddWorktree(path, branch string) error {
	_, err := c.Run("worktree", "add", path, branch)
	if err != nil {
		return fmt.Errorf("worktree add %s %s: %w", path, branch, err)
	}
	return nil
}

// RemoveWorktree removes a worktree at the specified path.
func (c *Client) RemoveWorktree(path string) error {
	// "git worktree remove" is available in newer git versions.
	// We should probably force remove if it's dirty, but for sync worktrees usually they are clean.
	// Let's use --force to be safe as we don't care about the worktree state after we are done.
	_, err := c.Run("worktree", "remove", "--force", path)
	if err != nil {
		// Fallback: if the directory is gone, maybe prune?
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			c.Run("worktree", "prune")
			return nil
		}
		return fmt.Errorf("worktree remove %s: %w", path, err)
	}
	return nil
}

// PruneWorktrees prunes stale worktrees.
func (c *Client) PruneWorktrees() error {
	_, err := c.Run("worktree", "prune")
	return err
}
