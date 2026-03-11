package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
)

func stashScopeIfDirty(repoRoot, scopePath, spaceKey string, ts time.Time) (string, error) {
	status, err := runGit(repoRoot, "status", "--porcelain", "--", scopePath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(status) == "" {
		return "", nil
	}

	message := fmt.Sprintf("Auto-stash %s %s", spaceKey, ts.UTC().Format(time.RFC3339))
	if _, err := runGit(repoRoot, "stash", "push", "--include-untracked", "-m", message, "--", scopePath); err != nil {
		return "", err
	}

	ref, err := runGit(repoRoot, "stash", "list", "-1", "--format=%gd")
	if err != nil {
		return "", err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("failed to capture stash reference")
	}
	return ref, nil
}

func applyAndDropStash(repoRoot, stashRef, scopePath string, in io.Reader, out io.Writer) error {
	if stashRef == "" {
		return nil
	}
	outStr, err := runGit(repoRoot, "stash", "apply", "--index", stashRef)
	if err != nil {
		if isStashConflictError(err, outStr) {
			return handlePullConflict(repoRoot, stashRef, scopePath, in, out)
		}
		return fmt.Errorf(
			"your workspace is currently in a syncing state and could not restore local changes automatically. finish reconciling pending files, then run pull again",
		)
	}
	if err := preserveLostTrackedStashChanges(repoRoot, stashRef, scopePath, out); err != nil {
		return err
	}
	if _, err := runGit(repoRoot, "stash", "drop", stashRef); err != nil {
		return fmt.Errorf("local changes were restored, but cleanup could not complete automatically")
	}
	return nil
}

func preserveLostTrackedStashChanges(repoRoot, stashRef, scopePath string, out io.Writer) error {
	client := &git.Client{RootDir: repoRoot}
	stashPaths, err := listStashPaths(client, stashRef, scopePath)
	if err != nil {
		return nil
	}
	untrackedSet, err := listStashUntrackedPathSet(client, stashRef, scopePath)
	if err != nil {
		return nil
	}

	for _, repoPath := range stashPaths {
		if _, untracked := untrackedSet[repoPath]; untracked {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(repoPath), ".md") {
			continue
		}

		stashRaw, err := runGit(repoRoot, "show", fmt.Sprintf("%s:%s", stashRef, repoPath))
		if err != nil {
			continue
		}

		absPath := filepath.Join(repoRoot, filepath.FromSlash(repoPath))
		workingRaw, readErr := os.ReadFile(absPath) //nolint:gosec // repoPath is repo-relative and validated by git
		if readErr == nil && string(workingRaw) == stashRaw {
			continue
		}
		if gitPathHasWorkingTreeChanges(repoRoot, repoPath) {
			continue
		}

		backupPath, backupErr := writeStashBackupCopy(repoRoot, stashRef, repoPath, "My Local Changes")
		if backupErr != nil {
			return fmt.Errorf("preserve local backup for %s: %w", repoPath, backupErr)
		}
		_, _ = fmt.Fprintf(out, "Saved local edits for %q as %q because automatic pull-merge could not reapply them cleanly.\n", repoPath, backupPath)
	}

	return nil
}

func writeStashBackupCopy(repoRoot, stashRef, repoPath, label string) (string, error) {
	localRaw, err := runGit(repoRoot, "show", fmt.Sprintf("%s:%s", stashRef, repoPath))
	if err != nil {
		return "", err
	}

	backupRepoPath, err := makeConflictBackupPath(repoRoot, repoPath, label)
	if err != nil {
		return "", err
	}
	backupAbsPath := filepath.Join(repoRoot, filepath.FromSlash(backupRepoPath))
	if err := os.MkdirAll(filepath.Dir(backupAbsPath), 0o750); err != nil {
		return "", err
	}
	if err := os.WriteFile(backupAbsPath, []byte(localRaw), 0o600); err != nil {
		return "", err
	}
	return backupRepoPath, nil
}

func gitPathHasWorkingTreeChanges(repoRoot, repoPath string) bool {
	cmd := exec.Command("git", "diff", "--quiet", "--", repoPath) //nolint:gosec // repo path is git-controlled
	cmd.Dir = repoRoot
	err := cmd.Run()
	if err == nil {
		return false
	}
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 1
}

func handlePullConflict(repoRoot, stashRef, scopePath string, in io.Reader, out io.Writer) error {
	conflictedPaths, err := listUnmergedPaths(repoRoot, scopePath)
	if err != nil {
		return fmt.Errorf("identify conflicted files: %w", err)
	}
	if len(conflictedPaths) == 0 {
		return fmt.Errorf("the workspace is in a syncing state; finish reconciling pending files before running pull again")
	}

	if flagNonInteractive || flagYes {
		switch strings.TrimSpace(flagMergeResolution) {
		case "keep-local":
			return applyPullConflictChoice("local", repoRoot, stashRef, scopePath, conflictedPaths, out)
		case "keep-remote":
			return applyPullConflictChoice("remote", repoRoot, stashRef, scopePath, conflictedPaths, out)
		case "keep-both":
			return applyPullConflictChoice("both", repoRoot, stashRef, scopePath, conflictedPaths, out)
		default:
			return fmt.Errorf(
				"a sync conflict needs your choice (keep local, keep website, or keep both), but interactive input is disabled. rerun without --non-interactive or pass --merge-resolution=keep-local|keep-remote|keep-both",
			)
		}
	}

	const (
		choiceKeepBoth = "both"
		choiceRemote   = "remote"
		choiceLocal    = "local"
	)

	if outputSupportsProgress(out) {
		var choice string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("⚠️  CONFLICT DETECTED").
					Description("Your local edits and the latest pulled content conflict.\nChoose how to continue:").
					Options(
						huh.NewOption("[C] Keep both (save local backup)", choiceKeepBoth),
						huh.NewOption("[B] Take website version", choiceRemote),
						huh.NewOption("[A] Keep my local version", choiceLocal),
					).
					Value(&choice),
			),
		).WithOutput(out)
		if err := form.Run(); err != nil {
			return err
		}
		return applyPullConflictChoice(choice, repoRoot, stashRef, scopePath, conflictedPaths, out)
	}

	// Plain-text fallback for non-TTY environments.
	_, _ = fmt.Fprintln(out, "\n"+warningStyle.Render("⚠️  CONFLICT DETECTED"))
	_, _ = fmt.Fprintln(out, "Your local edits and the latest pulled content conflict.")
	_, _ = fmt.Fprintln(out, " [A] Keep my local version (overwrite website on next push)")
	_, _ = fmt.Fprintln(out, " [B] Take the website version (discard my local edits for conflicted files)")
	_, _ = fmt.Fprintln(out, " [C] Keep both (save my local edits as separate backup files)")
	_, _ = fmt.Fprint(out, "\nChoice [A/B/C] (default C): ")

	rawChoice, err := readPromptLine(in)
	if err != nil {
		return err
	}

	var choice string
	switch strings.ToLower(strings.TrimSpace(rawChoice)) {
	case "a", "local", "keep-local":
		choice = choiceLocal
	case "b", "remote", "website", "take-website":
		choice = choiceRemote
	default:
		choice = choiceKeepBoth
	}
	return applyPullConflictChoice(choice, repoRoot, stashRef, scopePath, conflictedPaths, out)
}

func applyPullConflictChoice(choice, repoRoot, stashRef, scopePath string, conflictedPaths []string, out io.Writer) error {
	resolveWithSide := func(side string) error {
		for _, repoPath := range conflictedPaths {
			if _, err := runGit(repoRoot, "checkout", "--"+side, "--", repoPath); err != nil {
				return err
			}
			if _, err := runGit(repoRoot, "add", "--", repoPath); err != nil {
				return err
			}
		}

		if _, err := runGit(repoRoot, "reset", "--", scopePath); err != nil {
			return err
		}

		remaining, err := listUnmergedPaths(repoRoot, scopePath)
		if err != nil {
			return err
		}
		if len(remaining) > 0 {
			return fmt.Errorf("some conflicted files still need manual reconciliation")
		}
		return nil
	}

	createBackupCopy := func(repoPath string) (string, error) {
		localRaw, err := runGit(repoRoot, "show", fmt.Sprintf("%s:%s", stashRef, repoPath))
		if err != nil {
			return "", err
		}

		backupRepoPath, err := makeConflictBackupPath(repoRoot, repoPath, "My Local Changes")
		if err != nil {
			return "", err
		}
		backupAbsPath := filepath.Join(repoRoot, filepath.FromSlash(backupRepoPath))
		if err := os.MkdirAll(filepath.Dir(backupAbsPath), 0o750); err != nil {
			return "", err
		}
		if err := os.WriteFile(backupAbsPath, []byte(localRaw), 0o600); err != nil {
			return "", err
		}

		return backupRepoPath, nil
	}

	switch choice {
	case "remote":
		_, _ = fmt.Fprintln(out, "Keeping website versions for conflicted files...")
		if err := resolveWithSide("ours"); err != nil {
			return fmt.Errorf("could not keep website versions: %w", err)
		}
		if _, err := runGit(repoRoot, "stash", "drop", stashRef); err != nil {
			return fmt.Errorf("kept website versions, but cleanup could not finish automatically")
		}
		_, _ = fmt.Fprintln(out, successStyle.Render("Website version kept for conflicted files."))
		return nil
	case "local":
		_, _ = fmt.Fprintln(out, "Keeping local versions for conflicted files...")
		if err := resolveWithSide("theirs"); err != nil {
			return fmt.Errorf("could not keep local versions: %w", err)
		}
		if _, err := runGit(repoRoot, "stash", "drop", stashRef); err != nil {
			return fmt.Errorf("kept local versions, but cleanup could not finish automatically")
		}
		_, _ = fmt.Fprintln(out, successStyle.Render("Local version kept for conflicted files."))
		return nil
	default: // "both"
		for _, repoPath := range conflictedPaths {
			backupPath, backupErr := createBackupCopy(repoPath)
			if backupErr != nil {
				return fmt.Errorf("save local backup for %s: %w", repoPath, backupErr)
			}
			_, _ = fmt.Fprintf(out,
				"Conflict found in %q. Saved local edits as %q. Copy your changes into the main file when ready.\n",
				repoPath,
				backupPath,
			)
		}

		if err := resolveWithSide("ours"); err != nil {
			return fmt.Errorf("restore website versions for keep-both flow: %w", err)
		}
		if _, err := runGit(repoRoot, "stash", "drop", stashRef); err != nil {
			return fmt.Errorf("created backup files, but cleanup could not finish automatically")
		}

		_, _ = fmt.Fprintln(out, successStyle.Render("Kept both versions: website file remains primary, local edits were saved separately."))
		return nil
	}
}

func isStashConflictError(err error, output string) bool {
	if err == nil {
		return false
	}
	combined := strings.ToLower(strings.TrimSpace(err.Error() + "\n" + output))
	return strings.Contains(combined, "conflict") ||
		strings.Contains(combined, "unmerged") ||
		strings.Contains(combined, "needs merge")
}

func listUnmergedPaths(repoRoot, scopePath string) ([]string, error) {
	raw, err := runGit(repoRoot, "diff", "--name-only", "--diff-filter=U", "--", scopePath)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		paths = append(paths, filepath.ToSlash(line))
	}
	return paths, nil
}

func makeConflictBackupPath(repoRoot, repoPath, label string) (string, error) {
	repoPath = filepath.ToSlash(filepath.Clean(strings.TrimSpace(repoPath)))
	if repoPath == "" || repoPath == "." {
		return "", fmt.Errorf("invalid conflicted path")
	}

	dir := filepath.ToSlash(filepath.Dir(repoPath))
	base := filepath.Base(repoPath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if stem == "" {
		stem = "file"
	}

	suffix := strings.TrimSpace(label)
	if suffix == "" {
		suffix = "Conflict"
	}

	for i := 1; i <= 1000; i++ {
		candidateStem := fmt.Sprintf("%s (%s)", stem, suffix)
		if i > 1 {
			candidateStem = fmt.Sprintf("%s (%s %d)", stem, suffix, i)
		}

		candidate := candidateStem + ext
		if dir != "." && dir != "" {
			candidate = filepath.ToSlash(filepath.Join(dir, candidate))
		}

		if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(candidate))); os.IsNotExist(err) {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("unable to allocate conflict backup path for %s", repoPath)
}

func gitHasScopedStagedChanges(repoRoot, scopePath string) (bool, error) {
	cmd := exec.Command("git", "diff", "--cached", "--quiet", "--", scopePath) //nolint:gosec // Intentionally running git
	cmd.Dir = repoRoot
	err := cmd.Run()
	if err == nil {
		return false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("check staged changes: %w", err)
}

// fixPulledVersionsAfterStashRestore ensures the `version` frontmatter field
// in each updated-by-pull file matches the version that was committed by pull,
// even if the stash restore reintroduced the older local version.
// Any file that cannot be read or written is silently skipped — this is
// best-effort and must not fail the overall pull operation.
func fixPulledVersionsAfterStashRestore(repoRoot, spaceDir string, updatedRelPaths []string, out io.Writer) {
	if len(updatedRelPaths) == 0 {
		return
	}
	scopeRelPath, err := filepath.Rel(repoRoot, spaceDir)
	if err != nil {
		return
	}
	scopeRelPath = filepath.ToSlash(filepath.Clean(scopeRelPath))

	fixed := 0
	for _, relPath := range updatedRelPaths {
		relPath = normalizeRepoRelPath(relPath)
		if relPath == "" {
			continue
		}

		// The committed (pulled) version lives at HEAD in the repo-relative path.
		repoRelPath := relPath
		if scopeRelPath != "" && scopeRelPath != "." {
			repoRelPath = scopeRelPath + "/" + relPath
		}

		raw, gitErr := runGit(repoRoot, "show", "HEAD:"+repoRelPath)
		if gitErr != nil {
			continue
		}

		committedDoc, parseErr := fs.ParseMarkdownDocument([]byte(raw))
		if parseErr != nil {
			continue
		}
		pulledVersion := committedDoc.Frontmatter.Version
		if pulledVersion <= 0 {
			continue
		}

		absPath := filepath.Join(spaceDir, filepath.FromSlash(relPath))
		diskDoc, readErr := fs.ReadMarkdownDocument(absPath)
		if readErr != nil {
			continue
		}

		if diskDoc.Frontmatter.Version == pulledVersion {
			continue // already correct
		}

		diskDoc.Frontmatter.Version = pulledVersion
		if writeErr := fs.WriteMarkdownDocument(absPath, diskDoc); writeErr != nil {
			continue
		}
		fixed++
	}

	if fixed > 0 {
		_, _ = fmt.Fprintf(out, "Auto-updated version field in %d file(s) to match pulled remote version.\n", fixed)
	}
}
