package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func printNoIndexDiff(out io.Writer, leftPath, rightPath string) error {
	workingDir, leftArg, rightArg := diffCommandPaths(leftPath, rightPath)

	cmd := exec.Command( //nolint:gosec // arguments are fixed git flags plus scoped local temp paths for display-only diff
		"git",
		"-c",
		"core.autocrlf=false",
		"diff",
		"--no-index",
		"--",
		leftArg,
		rightArg,
	)
	if strings.TrimSpace(workingDir) != "" {
		cmd.Dir = workingDir
	}

	raw, err := cmd.CombinedOutput()
	text := sanitizeNoIndexDiffOutput(string(raw))
	if text != "" {
		_, _ = io.WriteString(out, text)
	}

	if err == nil {
		if _, writeErr := fmt.Fprintln(out, "diff completed with no differences"); writeErr != nil {
			return fmt.Errorf("write diff output: %w", writeErr)
		}
		return nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil
	}

	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("git diff --no-index failed: %w", err)
	}
	return fmt.Errorf("git diff --no-index failed: %s", strings.TrimSpace(text))
}

func diffCommandPaths(leftPath, rightPath string) (workingDir, leftArg, rightArg string) {
	leftAbs, leftErr := filepath.Abs(leftPath)
	rightAbs, rightErr := filepath.Abs(rightPath)
	if leftErr != nil || rightErr != nil {
		return "", leftPath, rightPath
	}

	base := leftAbs
	if leftInfo, err := os.Stat(leftAbs); err == nil && !leftInfo.IsDir() {
		base = filepath.Dir(leftAbs)
	}

	for !isPathParentOrSame(base, rightAbs) {
		next := filepath.Dir(base)
		if next == base {
			return "", leftAbs, rightAbs
		}
		base = next
	}

	leftRel, err := filepath.Rel(base, leftAbs)
	if err != nil {
		return "", leftAbs, rightAbs
	}
	rightRel, err := filepath.Rel(base, rightAbs)
	if err != nil {
		return "", leftAbs, rightAbs
	}

	return base, filepath.ToSlash(leftRel), filepath.ToSlash(rightRel)
}

func isPathParentOrSame(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	rel = filepath.ToSlash(rel)
	return !strings.HasPrefix(rel, "../") && rel != ".."
}

func sanitizeNoIndexDiffOutput(text string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "warning: in the working copy of") {
			continue
		}
		kept = append(kept, line)
	}

	result := strings.Join(kept, "\n")
	if text != "" && !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result
}
