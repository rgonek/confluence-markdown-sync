package git

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// FileStatus represents a file status in git.
type FileStatus struct {
	Code string
	Path string
}

// StatusPorcelain returns the status of files in the given scope.
func (c *Client) StatusPorcelain(scopePath string) (string, error) {
	args := []string{"status", "--porcelain"}
	if scopePath != "" {
		args = append(args, "--", scopePath)
	}
	return c.Run(args...)
}

// DiffNameStatus returns the status of changed files between two commits.
// If 'to' is empty, it returns the status of changed files between 'from' and the working tree.
func (c *Client) DiffNameStatus(from, to, scopePath string) ([]FileStatus, error) {
	var out string
	var err error

	if to == "" {
		out, err = c.Run("diff", "--name-status", from, "--", scopePath)
	} else {
		rangeExpr := fmt.Sprintf("%s..%s", from, to)
		out, err = c.Run("diff", "--name-status", rangeExpr, "--", scopePath)
	}

	if err != nil {
		return nil, err
	}

	var statuses []FileStatus
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		status := parts[0]
		// Handle renames (R100 old new)
		if strings.HasPrefix(status, "R") {
			if len(parts) < 3 {
				continue
			}
			statuses = append(statuses, FileStatus{Code: "D", Path: filepath.ToSlash(parts[1])})
			statuses = append(statuses, FileStatus{Code: "A", Path: filepath.ToSlash(parts[2])})
		} else {
			statuses = append(statuses, FileStatus{Code: string(status[0]), Path: filepath.ToSlash(parts[1])})
		}
	}

	sort.Slice(statuses, func(i, j int) bool {
		if statuses[i].Path == statuses[j].Path {
			return statuses[i].Code < statuses[j].Code
		}
		return statuses[i].Path < statuses[j].Path
	})

	return statuses, nil
}
