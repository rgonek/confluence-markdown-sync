package cmd

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/spf13/cobra"
)

// conflict policy values for --on-conflict.
const (
	OnConflictPullMerge = "pull-merge"
	OnConflictForce     = "force"
	OnConflictCancel    = "cancel"
)

func newPushCmd() *cobra.Command {
	var onConflict string

	cmd := &cobra.Command{
		Use:   "push [TARGET]",
		Short: "Push local Markdown changes to Confluence",
		Long: `Push converts local Markdown files to ADF and updates Confluence pages.

TARGET can be a SPACE_KEY (e.g. "MYSPACE") or a path to a .md file.
If omitted, the space is inferred from the current directory name.

push always runs validate before any remote write.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			return runPush(cmd, config.ParseTarget(raw), onConflict)
		},
	}
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Auto-approve safety confirmations")
	cmd.Flags().BoolVar(&flagNonInteractive, "non-interactive", false, "Disable prompts; fail fast when a decision is required")
	cmd.Flags().StringVar(&onConflict, "on-conflict", "", "Non-interactive conflict policy: pull-merge|force|cancel")
	return cmd
}

func validateOnConflict(v string) error {
	if v == "" {
		return nil
	}
	switch v {
	case OnConflictPullMerge, OnConflictForce, OnConflictCancel:
		return nil
	default:
		return fmt.Errorf("invalid --on-conflict value %q: must be pull-merge, force, or cancel", v)
	}
}

type pushFileChange struct {
	Status string
	Path   string
}

func runPush(cmd *cobra.Command, target config.Target, onConflict string) error {
	if err := validateOnConflict(onConflict); err != nil {
		return err
	}

	if err := runValidateTarget(cmd.OutOrStdout(), target); err != nil {
		return fmt.Errorf("pre-push validate failed: %w", err)
	}

	targetCtx, err := resolveValidateTargetContext(target)
	if err != nil {
		return err
	}
	spaceDir := targetCtx.spaceDir
	spaceKey := filepath.Base(spaceDir)

	repoRoot, err := gitRepoRoot()
	if err != nil {
		return err
	}

	scopePath, err := gitScopePath(repoRoot, spaceDir)
	if err != nil {
		return err
	}

	baselineRef, err := gitPushBaselineRef(repoRoot, spaceKey)
	if err != nil {
		return err
	}

	changes, err := gitChangedMarkdownSince(repoRoot, scopePath, baselineRef)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "push completed with no in-scope markdown changes (no-op)")
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "push: %d markdown change(s) detected since %s\n", len(changes), baselineRef)
	return fmt.Errorf("push sync loop not yet implemented")
}

func gitPushBaselineRef(repoRoot, spaceKey string) (string, error) {
	spaceKey = strings.TrimSpace(spaceKey)
	if spaceKey == "" {
		return "", fmt.Errorf("space key is required")
	}

	tagsRaw, err := runGit(
		repoRoot,
		"tag",
		"--list",
		fmt.Sprintf("confluence-sync/pull/%s/*", spaceKey),
		fmt.Sprintf("confluence-sync/push/%s/*", spaceKey),
	)
	if err != nil {
		return "", err
	}

	bestTag := ""
	bestStamp := ""
	for _, line := range strings.Split(strings.ReplaceAll(tagsRaw, "\r\n", "\n"), "\n") {
		tag := strings.TrimSpace(line)
		if tag == "" {
			continue
		}
		parts := strings.Split(tag, "/")
		if len(parts) < 4 {
			continue
		}
		timestamp := parts[len(parts)-1]
		if timestamp > bestStamp {
			bestStamp = timestamp
			bestTag = tag
		}
	}
	if bestTag != "" {
		return bestTag, nil
	}

	rootCommitRaw, err := runGit(repoRoot, "rev-list", "--max-parents=0", "HEAD")
	if err != nil {
		return "", err
	}
	lines := strings.Fields(rootCommitRaw)
	if len(lines) == 0 {
		return "", fmt.Errorf("unable to determine baseline commit")
	}
	return lines[0], nil
}

func gitChangedMarkdownSince(repoRoot, scopePath, baselineRef string) ([]pushFileChange, error) {
	rangeExpr := fmt.Sprintf("%s..HEAD", strings.TrimSpace(baselineRef))
	out, err := runGit(repoRoot, "diff", "--name-status", rangeExpr, "--", scopePath)
	if err != nil {
		return nil, err
	}

	changes := make([]pushFileChange, 0)
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
		switch {
		case strings.HasPrefix(status, "R"):
			if len(parts) < 3 {
				continue
			}
			oldPath := filepath.ToSlash(parts[1])
			newPath := filepath.ToSlash(parts[2])
			if strings.HasSuffix(strings.ToLower(oldPath), ".md") {
				changes = append(changes, pushFileChange{Status: "D", Path: oldPath})
			}
			if strings.HasSuffix(strings.ToLower(newPath), ".md") {
				changes = append(changes, pushFileChange{Status: "A", Path: newPath})
			}
		default:
			path := filepath.ToSlash(parts[1])
			if !strings.HasSuffix(strings.ToLower(path), ".md") {
				continue
			}
			changes = append(changes, pushFileChange{Status: string(status[0]), Path: path})
		}
	}

	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path == changes[j].Path {
			return changes[i].Status < changes[j].Status
		}
		return changes[i].Path < changes[j].Path
	})

	return changes, nil
}
