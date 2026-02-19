package git

// Add stages files.
func (c *Client) Add(path ...string) error {
	if len(path) == 0 {
		return nil
	}
	args := append([]string{"add", "--"}, path...)
	_, err := c.Run(args...)
	return err
}

// Commit creates a commit with the given message.
func (c *Client) Commit(subject, body string) error {
	args := []string{"commit", "-m", subject}
	if body != "" {
		args = append(args, "-m", body)
	}
	_, err := c.Run(args...)
	return err
}

// Tag creates an annotated tag.
func (c *Client) Tag(name, message string) error {
	_, err := c.Run("tag", "-a", name, "-m", message)
	return err
}
