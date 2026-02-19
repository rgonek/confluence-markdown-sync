package git

import (
	"fmt"
)

// RebaseOnto rebases a branch onto a new base, skipping upstream.
// git rebase --onto <newbase> <upstream> <branch>
func (c *Client) RebaseOnto(newBase, upstream, branch string) error {
	_, err := c.Run("rebase", "--onto", newBase, upstream, branch)
	if err != nil {
		return fmt.Errorf("rebase %s onto %s (upstream %s): %w", branch, newBase, upstream, err)
	}
	return nil
}
