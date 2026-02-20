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
		out, err = c.Run("diff", "--name-status", "-z", from, "--", scopePath)
	} else {
		rangeExpr := fmt.Sprintf("%s..%s", from, to)
		out, err = c.Run("diff", "--name-status", "-z", rangeExpr, "--", scopePath)
	}

	if err != nil {
		return nil, err
	}

	var statuses []FileStatus
	tokens := strings.Split(out, "\x00")
	for i := 0; i < len(tokens); {
		token := tokens[i]
		if token == "" {
			i++
			continue
		}

		status := ""
		firstPath := ""
		if strings.Contains(token, "\t") {
			parts := strings.SplitN(token, "\t", 2)
			if len(parts) == 2 {
				status = parts[0]
				firstPath = parts[1]
			}
		} else {
			status = token
		}
		if status == "" {
			i++
			continue
		}

		if strings.HasPrefix(status, "R") {
			if firstPath == "" {
				if i+2 >= len(tokens) || tokens[i+1] == "" || tokens[i+2] == "" {
					i++
					continue
				}
				statuses = append(statuses, FileStatus{Code: "D", Path: filepath.ToSlash(tokens[i+1])})
				statuses = append(statuses, FileStatus{Code: "A", Path: filepath.ToSlash(tokens[i+2])})
				i += 3
				continue
			}

			if i+1 >= len(tokens) || tokens[i+1] == "" {
				i++
				continue
			}
			statuses = append(statuses, FileStatus{Code: "D", Path: filepath.ToSlash(firstPath)})
			statuses = append(statuses, FileStatus{Code: "A", Path: filepath.ToSlash(tokens[i+1])})
			i += 2
			continue
		}

		if firstPath == "" {
			if i+1 >= len(tokens) || tokens[i+1] == "" {
				i++
				continue
			}
			firstPath = tokens[i+1]
			statuses = append(statuses, FileStatus{Code: string(status[0]), Path: filepath.ToSlash(firstPath)})
			i += 2
			continue
		}

		statuses = append(statuses, FileStatus{Code: string(status[0]), Path: filepath.ToSlash(firstPath)})
		i++
	}

	sort.Slice(statuses, func(i, j int) bool {
		if statuses[i].Path == statuses[j].Path {
			return statuses[i].Code < statuses[j].Code
		}
		return statuses[i].Path < statuses[j].Path
	})

	return statuses, nil
}
