package git

import (
	"fmt"
	"strings"
	"time"
)

// StashPush pushes changes to the stash.
// It includes untracked files.
func (c *Client) StashPush(scopePath, message string) (string, error) {
	args := []string{"stash", "push", "--include-untracked", "-m", message}
	if scopePath != "" {
		args = append(args, "--", scopePath)
	}

	if _, err := c.Run(args...); err != nil {
		return "", err
	}

	// Capture the stash ref we just created
	ref, err := c.Run("stash", "list", "-1", "--format=%gd")
	if err != nil {
		return "", err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		// It's possible stash push did nothing if there were no changes
		return "", nil
	}
	return ref, nil
}

// StashApply applies a stash.
func (c *Client) StashApply(stashRef string) error {
	if stashRef == "" {
		return nil
	}
	if _, err := c.Run("stash", "apply", "--index", stashRef); err != nil {
		return fmt.Errorf("stash apply %s: %w", stashRef, err)
	}
	return nil
}

// StashDrop drops a stash.
func (c *Client) StashDrop(stashRef string) error {
	if stashRef == "" {
		return nil
	}
	if _, err := c.Run("stash", "drop", stashRef); err != nil {
		return fmt.Errorf("stash drop %s: %w", stashRef, err)
	}
	return nil
}

// StashPop applies and drops a stash.
func (c *Client) StashPop(stashRef string) error {
	if stashRef == "" {
		return nil
	}
	// We use apply+drop instead of pop to match the plan's robustness recommendation
	// but standard git stash pop is also fine if we don't need to keep it on failure.
	// However, the plan says "Run git stash pop... If Frontmatter conflicts occur... leave standard Git conflict markers".
	// git stash pop does exactly that.
	if _, err := c.Run("stash", "pop", "--index", stashRef); err != nil {
		// If conflict occurs, pop fails but changes are applied (usually).
		// We might want to wrap this error or detect if it's a conflict.
		return fmt.Errorf("stash pop %s: %w", stashRef, err)
	}
	return nil
}

// StashScopeIfDirty stashes changes in a specific scope if there are any.
func (c *Client) StashScopeIfDirty(scopePath, spaceKey string, ts time.Time) (string, error) {
	status, err := c.Run("status", "--porcelain", "--", scopePath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(status) == "" {
		return "", nil
	}

	message := fmt.Sprintf("Auto-stash %s %s", spaceKey, ts.UTC().Format(time.RFC3339))
	return c.StashPush(scopePath, message)
}
