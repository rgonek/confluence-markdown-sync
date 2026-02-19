package git

import (
	"fmt"
	"strings"
)

// ResolveRef resolves a ref to a commit hash.
func (c *Client) ResolveRef(ref string) (string, error) {
	out, err := c.Run("rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("resolve ref %s: %w", ref, err)
	}
	return strings.TrimSpace(out), nil
}

// UpdateRef updates a ref to a specific commit.
func (c *Client) UpdateRef(ref, commit, reason string) error {
	args := []string{"update-ref", "-m", reason, ref, commit}
	_, err := c.Run(args...)
	if err != nil {
		return fmt.Errorf("update ref %s to %s: %w", ref, commit, err)
	}
	return nil
}

// DeleteRef deletes a ref.
func (c *Client) DeleteRef(ref string) error {
	_, err := c.Run("update-ref", "-d", ref)
	if err != nil {
		return fmt.Errorf("delete ref %s: %w", ref, err)
	}
	return nil
}
