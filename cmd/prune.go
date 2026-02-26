package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rgonek/confluence-markdown-sync/internal/config"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

func newPruneCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "prune [TARGET]",
		Short: "Delete orphaned local assets",
		Long: `Prune scans assets/ inside a managed space and deletes files that are no longer referenced
by any markdown page in that space.

TARGET follows the standard rule:
- .md suffix => file mode (space inferred from file)
- otherwise => space mode (SPACE_KEY or space directory).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			return runPrune(cmd, config.ParseTarget(raw))
		},
	}

	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Auto-approve orphan asset deletion")
	cmd.Flags().BoolVar(&flagNonInteractive, "non-interactive", false, "Disable prompts; fail fast when confirmation is required")
	return cmd
}

func runPrune(cmd *cobra.Command, target config.Target) error {
	if err := ensureWorkspaceSyncReady("prune"); err != nil {
		return err
	}

	out := ensureSynchronizedCmdOutput(cmd)
	initialCtx, err := resolveInitialPullContext(target)
	if err != nil {
		return err
	}

	spaceDir := initialCtx.spaceDir
	if !dirExists(spaceDir) {
		return fmt.Errorf("space directory not found: %s", spaceDir)
	}

	orphans, err := syncflow.FindOrphanAssets(spaceDir)
	if err != nil {
		return fmt.Errorf("scan orphan assets: %w", err)
	}

	if len(orphans) == 0 {
		_, _ = fmt.Fprintf(out, "No orphan assets found in %s\n", spaceDir)
		return nil
	}

	_, _ = fmt.Fprintf(out, "Found %d orphan asset(s) in %s:\n", len(orphans), spaceDir)
	for _, relPath := range orphans {
		_, _ = fmt.Fprintf(out, "  - %s\n", relPath)
	}

	if err := confirmPruneDeletion(cmd.InOrStdin(), out, len(orphans)); err != nil {
		return err
	}

	assetsRoot := filepath.Join(spaceDir, "assets")
	deleted := 0
	for _, relPath := range orphans {
		absPath := filepath.Join(spaceDir, filepath.FromSlash(relPath))
		if err := os.Remove(absPath); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("delete orphan asset %s: %w", relPath, err)
		}
		deleted++
		_ = removeEmptyAssetParents(filepath.Dir(absPath), assetsRoot)
	}

	_, _ = fmt.Fprintf(out, "prune completed: deleted %d orphan asset(s)\n", deleted)
	return nil
}

func confirmPruneDeletion(in io.Reader, out io.Writer, orphanCount int) error {
	if orphanCount <= 0 || flagYes {
		return nil
	}
	if flagNonInteractive {
		return fmt.Errorf("prune requires confirmation to delete %d orphan asset(s); rerun with --yes", orphanCount)
	}

	title := fmt.Sprintf("Delete %d orphan asset(s)?", orphanCount)

	if outputSupportsProgress(out) {
		var confirm bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(title).
					Description("These local files are not referenced by any markdown page in this space.").
					Value(&confirm),
			),
		).WithOutput(out)
		if err := form.Run(); err != nil {
			return err
		}
		if !confirm {
			return fmt.Errorf("prune cancelled")
		}
		return nil
	}

	if _, err := fmt.Fprintf(out, "%s [y/N]: ", title); err != nil {
		return fmt.Errorf("write prompt: %w", err)
	}
	choice, err := readPromptLine(in)
	if err != nil {
		return err
	}
	choice = strings.ToLower(strings.TrimSpace(choice))
	if choice != "y" && choice != "yes" {
		return fmt.Errorf("prune cancelled")
	}
	return nil
}

func removeEmptyAssetParents(startDir, stopDir string) error {
	startDir = filepath.Clean(startDir)
	stopDir = filepath.Clean(stopDir)

	for {
		if startDir == "" || startDir == "." {
			return nil
		}
		rel, err := filepath.Rel(stopDir, startDir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}

		entries, err := os.ReadDir(startDir)
		if err != nil {
			if os.IsNotExist(err) {
				startDir = filepath.Dir(startDir)
				continue
			}
			return err
		}
		if len(entries) > 0 {
			return nil
		}
		if err := os.Remove(startDir); err != nil && !os.IsNotExist(err) {
			return err
		}

		if startDir == stopDir {
			return nil
		}
		startDir = filepath.Dir(startDir)
	}
}
