package git

import (
	"fmt"
	"strings"
)

// CreateBranch creates a new branch from a start point.
func (c *Client) CreateBranch(name, startPoint string) error {
	_, err := c.Run("branch", name, startPoint)
	if err != nil {
		return fmt.Errorf("create branch %s from %s: %w", name, startPoint, err)
	}
	return nil
}

// DeleteBranch deletes a branch (force).
func (c *Client) DeleteBranch(name string) error {
	_, err := c.Run("branch", "-D", name)
	if err != nil {
		// If branch doesn't exist, it's fine
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return fmt.Errorf("delete branch %s: %w", name, err)
	}
	return nil
}

// CurrentBranch returns the current branch name.
func (c *Client) CurrentBranch() (string, error) {
	out, err := c.Run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("current branch: %w", err)
	}
	return strings.TrimSpace(out), nil
}

// Merge merges a branch into the current branch.
func (c *Client) Merge(branch, message string) error {
	args := []string{"merge", "--no-ff", branch}
	if message != "" {
		args = append(args, "-m", message)
	}
	_, err := c.Run(args...)
	if err != nil {
		return fmt.Errorf("merge %s: %w", branch, err)
	}
	return nil
}
