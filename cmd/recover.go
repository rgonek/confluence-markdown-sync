package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
	"github.com/spf13/cobra"
)

var (
	flagRecoverDiscard    string
	flagRecoverDiscardAll bool
)

func newRecoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "inspect or discard retained failed push recovery artifacts",
		Long: `recover inspects retained failed push recovery artifacts without changing them by default.

It can list retained sync branches, snapshot refs, linked worktrees, and any recorded
failure reason. With --discard or --discard-all it safely removes abandoned recovery
artifacts while preserving the current recovery branch and active linked worktrees.`,
		Args: cobra.NoArgs,
		RunE: runRecover,
	}

	cmd.Flags().StringVar(&flagRecoverDiscard, "discard", "", "Discard a specific recovery run (sync branch, snapshot ref, SPACE_KEY/TIMESTAMP, or TIMESTAMP)")
	cmd.Flags().BoolVar(&flagRecoverDiscardAll, "discard-all", false, "Discard all safe retained recovery artifacts")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false, "Auto-approve discard actions")
	cmd.Flags().BoolVar(&flagNonInteractive, "non-interactive", false, "Disable prompts; fail fast when confirmation is required")
	return cmd
}

func runRecover(cmd *cobra.Command, _ []string) error {
	out := ensureSynchronizedCmdOutput(cmd)

	if strings.TrimSpace(flagRecoverDiscard) != "" && flagRecoverDiscardAll {
		return fmt.Errorf("recover accepts either --discard or --discard-all, not both")
	}

	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("init git client: %w", err)
	}

	currentBranch, err := client.CurrentBranch()
	if err != nil {
		return err
	}
	currentBranch = strings.TrimSpace(currentBranch)

	runs, warnings, err := listRecoveryRuns(client, currentBranch)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(out, "Repository: %s\n", client.RootDir)
	_, _ = fmt.Fprintf(out, "Branch: %s\n", currentBranch)
	for _, warning := range warnings {
		_, _ = fmt.Fprintf(out, "warning: %s\n", warning)
	}

	if len(runs) == 0 {
		_, _ = fmt.Fprintln(out, "recover: no retained failed push artifacts found")
		return nil
	}

	selectedRuns := runs
	if selector := strings.TrimSpace(flagRecoverDiscard); selector != "" {
		selectedRuns = selectRecoveryRuns(runs, selector)
		if len(selectedRuns) == 0 {
			return fmt.Errorf("recover: no retained recovery run matches %q", selector)
		}
	}

	if !flagRecoverDiscardAll && strings.TrimSpace(flagRecoverDiscard) == "" {
		renderRecoveryRuns(out, runs)
		_, _ = fmt.Fprintf(out, "recover: %d retained recovery run(s) found; rerun with --discard or --discard-all to remove abandoned artifacts\n", len(runs))
		return nil
	}

	if err := confirmRecoverDiscard(cmd.InOrStdin(), out, len(selectedRuns)); err != nil {
		return err
	}

	discarded := 0
	skipped := 0
	for _, run := range selectedRuns {
		if run.CurrentBranch {
			skipped++
			_, _ = fmt.Fprintf(out, "Retained recovery run %s: current HEAD is on this sync branch\n", run.SyncBranch)
			continue
		}
		if run.WorktreeBlockReason != "" {
			skipped++
			_, _ = fmt.Fprintf(out, "Retained recovery run %s: %s\n", run.SyncBranch, run.WorktreeBlockReason)
			continue
		}

		if run.SnapshotRef != "" {
			if err := client.DeleteRef(run.SnapshotRef); err != nil {
				return err
			}
		}
		if run.SyncBranch != "" {
			if err := client.DeleteBranch(run.SyncBranch); err != nil {
				return err
			}
		}
		if err := deleteRecoveryMetadata(client.RootDir, run.SpaceKey, run.Timestamp); err != nil {
			return err
		}

		discarded++
		_, _ = fmt.Fprintf(out, "Discarded recovery run: %s\n", run.SyncBranch)
	}

	_, _ = fmt.Fprintf(out, "recover completed: discarded %d recovery run(s), retained %d recovery run(s)\n", discarded, skipped)
	return nil
}

type recoveryRun struct {
	SpaceKey            string
	Timestamp           string
	SyncBranch          string
	SnapshotRef         string
	FailureReason       string
	OriginalBranch      string
	WorktreePaths       []string
	WorktreeBlockReason string
	CurrentBranch       bool
}

func listRecoveryRuns(client *git.Client, currentBranch string) ([]recoveryRun, []string, error) {
	snapshotRefs, err := listCleanSnapshotRefs(client)
	if err != nil {
		return nil, nil, err
	}
	syncBranches, err := listCleanSyncBranches(client)
	if err != nil {
		return nil, nil, err
	}
	worktreeBranches, err := listCleanWorktreeBranches(client)
	if err != nil {
		return nil, nil, err
	}
	metadataByKey, warnings, err := listRecoveryMetadata(client.RootDir)
	if err != nil {
		return nil, nil, err
	}

	snapshotSet := make(map[string]struct{}, len(snapshotRefs))
	for _, ref := range snapshotRefs {
		snapshotSet[ref] = struct{}{}
	}

	runMap := make(map[string]recoveryRun)
	for _, branch := range syncBranches {
		spaceKey, timestamp, ok := parseManagedSyncBranch(branch)
		if !ok {
			continue
		}
		key := recoveryRunKey(spaceKey, timestamp)
		run := runMap[key]
		run.SpaceKey = spaceKey
		run.Timestamp = timestamp
		run.SyncBranch = branch
		run.SnapshotRef = fmt.Sprintf("refs/confluence-sync/snapshots/%s/%s", spaceKey, timestamp)
		run.WorktreePaths = append([]string(nil), worktreeBranches[branch]...)
		run.WorktreeBlockReason = cleanSyncBranchWorktreeBlockReason(worktreeBranches[branch], map[string]struct{}{})
		run.CurrentBranch = branch == currentBranch
		if metadata, ok := metadataByKey[key]; ok {
			run.FailureReason = strings.TrimSpace(metadata.FailureReason)
			run.OriginalBranch = strings.TrimSpace(metadata.OriginalBranch)
		}
		if _, ok := snapshotSet[run.SnapshotRef]; !ok {
			run.SnapshotRef = ""
		}
		runMap[key] = run
	}

	for _, ref := range snapshotRefs {
		spaceKey, timestamp, ok := parseSnapshotRef(ref)
		if !ok {
			continue
		}
		key := recoveryRunKey(spaceKey, timestamp)
		run := runMap[key]
		run.SpaceKey = spaceKey
		run.Timestamp = timestamp
		run.SnapshotRef = ref
		if run.SyncBranch == "" {
			run.SyncBranch = fmt.Sprintf("sync/%s/%s", spaceKey, timestamp)
		}
		if metadata, ok := metadataByKey[key]; ok {
			run.FailureReason = strings.TrimSpace(metadata.FailureReason)
			run.OriginalBranch = strings.TrimSpace(metadata.OriginalBranch)
		}
		runMap[key] = run
	}

	for key, metadata := range metadataByKey {
		run := runMap[key]
		if run.SpaceKey == "" {
			run.SpaceKey = metadata.SpaceKey
		}
		if run.Timestamp == "" {
			run.Timestamp = metadata.Timestamp
		}
		if run.SyncBranch == "" {
			run.SyncBranch = strings.TrimSpace(metadata.SyncBranch)
		}
		if run.SnapshotRef == "" {
			run.SnapshotRef = strings.TrimSpace(metadata.SnapshotRef)
		}
		if run.FailureReason == "" {
			run.FailureReason = strings.TrimSpace(metadata.FailureReason)
		}
		if run.OriginalBranch == "" {
			run.OriginalBranch = strings.TrimSpace(metadata.OriginalBranch)
		}
		runMap[key] = run
	}

	runs := make([]recoveryRun, 0, len(runMap))
	for _, run := range runMap {
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].Timestamp == runs[j].Timestamp {
			return runs[i].SpaceKey < runs[j].SpaceKey
		}
		return runs[i].Timestamp < runs[j].Timestamp
	})
	return runs, warnings, nil
}

func selectRecoveryRuns(runs []recoveryRun, selector string) []recoveryRun {
	selector = strings.TrimSpace(selector)
	selected := make([]recoveryRun, 0)
	for _, run := range runs {
		if selector == run.SyncBranch ||
			selector == run.SnapshotRef ||
			selector == run.Timestamp ||
			selector == fmt.Sprintf("%s/%s", run.SpaceKey, run.Timestamp) {
			selected = append(selected, run)
		}
	}
	return selected
}

func renderRecoveryRuns(out io.Writer, runs []recoveryRun) {
	_, _ = fmt.Fprintln(out, "Recovery artifacts:")

	_, _ = fmt.Fprintln(out, "\nSnapshot refs:")
	snapshotCount := 0
	for _, run := range runs {
		if run.SnapshotRef != "" {
			_, _ = fmt.Fprintf(out, "  %s\n", run.SnapshotRef)
			snapshotCount++
		}
	}
	if snapshotCount == 0 {
		_, _ = fmt.Fprintln(out, "  (none)")
	}

	_, _ = fmt.Fprintln(out, "\nSync branches:")
	branchCount := 0
	for _, run := range runs {
		if run.SyncBranch != "" {
			_, _ = fmt.Fprintf(out, "  %s\n", run.SyncBranch)
			branchCount++
		}
	}
	if branchCount == 0 {
		_, _ = fmt.Fprintln(out, "  (none)")
	}

	_, _ = fmt.Fprintln(out, "\nFailed runs:")
	for _, run := range runs {
		_, _ = fmt.Fprintf(out, "  %s %s\n", run.SpaceKey, run.Timestamp)
		if run.SyncBranch != "" {
			_, _ = fmt.Fprintf(out, "    Branch: %s\n", run.SyncBranch)
		}
		if run.SnapshotRef != "" {
			_, _ = fmt.Fprintf(out, "    Snapshot: %s\n", run.SnapshotRef)
		}
		if run.OriginalBranch != "" {
			_, _ = fmt.Fprintf(out, "    Original branch: %s\n", run.OriginalBranch)
		}
		if strings.TrimSpace(run.FailureReason) == "" {
			_, _ = fmt.Fprintln(out, "    Failure: unavailable")
		} else {
			_, _ = fmt.Fprintf(out, "    Failure: %s\n", run.FailureReason)
		}
		if run.CurrentBranch {
			_, _ = fmt.Fprintln(out, "    Status: current HEAD is on this recovery branch")
		} else if run.WorktreeBlockReason != "" {
			_, _ = fmt.Fprintf(out, "    Status: %s\n", run.WorktreeBlockReason)
		} else {
			_, _ = fmt.Fprintln(out, "    Status: safe to discard")
		}
	}
}

func confirmRecoverDiscard(in io.Reader, out io.Writer, runCount int) error {
	if flagYes {
		return nil
	}
	if flagNonInteractive {
		return fmt.Errorf("recover discard requires confirmation; rerun with --yes")
	}

	title := fmt.Sprintf("Discard %d retained recovery run(s)?", runCount)
	if outputSupportsProgress(out) {
		var confirm bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(title).
					Description("This removes retained sync branches, snapshot refs, and stored recovery metadata when safe.").
					Value(&confirm),
			),
		).WithOutput(out)
		if err := form.Run(); err != nil {
			return err
		}
		if !confirm {
			return fmt.Errorf("recover discard cancelled")
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
		return fmt.Errorf("recover discard cancelled")
	}
	return nil
}

type recoveryMetadata struct {
	SpaceKey       string `json:"space_key"`
	Timestamp      string `json:"timestamp"`
	SyncBranch     string `json:"sync_branch"`
	SnapshotRef    string `json:"snapshot_ref"`
	OriginalBranch string `json:"original_branch,omitempty"`
	FailureReason  string `json:"failure_reason,omitempty"`
}

func listRecoveryMetadata(repoRoot string) (map[string]recoveryMetadata, []string, error) {
	root := filepath.Join(repoRoot, ".git", "confluence-recovery")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]recoveryMetadata{}, nil, nil
		}
		return nil, nil, fmt.Errorf("read recovery metadata root: %w", err)
	}

	result := make(map[string]recoveryMetadata)
	warnings := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		spaceKey := entry.Name()
		spaceDir := filepath.Join(root, spaceKey)
		files, err := os.ReadDir(spaceDir)
		if err != nil {
			return nil, nil, fmt.Errorf("read recovery metadata %s: %w", spaceDir, err)
		}
		for _, file := range files {
			if file.IsDir() || filepath.Ext(file.Name()) != ".json" {
				continue
			}
			metadataPath := filepath.Join(spaceDir, file.Name())
			raw, err := os.ReadFile(metadataPath)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("skipping unreadable recovery metadata %s: %v", file.Name(), err))
				continue
			}
			var metadata recoveryMetadata
			if err := json.Unmarshal(raw, &metadata); err != nil {
				warnings = append(warnings, fmt.Sprintf("skipping unreadable recovery metadata %s: %v", file.Name(), err))
				continue
			}
			if metadata.SpaceKey == "" {
				metadata.SpaceKey = spaceKey
			}
			if metadata.Timestamp == "" {
				metadata.Timestamp = strings.TrimSuffix(file.Name(), filepath.Ext(file.Name()))
			}
			result[recoveryRunKey(metadata.SpaceKey, metadata.Timestamp)] = metadata
		}
	}
	return result, warnings, nil
}

func writeRecoveryMetadata(repoRoot string, metadata recoveryMetadata) error {
	path := recoveryMetadataPath(repoRoot, metadata.SpaceKey, metadata.Timestamp)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create recovery metadata dir: %w", err)
	}
	raw, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("encode recovery metadata: %w", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return fmt.Errorf("write recovery metadata: %w", err)
	}
	return nil
}

func deleteRecoveryMetadata(repoRoot, spaceKey, timestamp string) error {
	path := recoveryMetadataPath(repoRoot, spaceKey, timestamp)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete recovery metadata: %w", err)
	}
	_ = os.Remove(filepath.Dir(path))
	return nil
}

func recoveryMetadataPath(repoRoot, spaceKey, timestamp string) string {
	return filepath.Join(repoRoot, ".git", "confluence-recovery", spaceKey, timestamp+".json")
}

func parseManagedSyncBranch(branch string) (spaceKey, timestamp string, ok bool) {
	parts := strings.Split(strings.TrimSpace(branch), "/")
	if len(parts) != 3 || parts[0] != "sync" || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func parseSnapshotRef(ref string) (spaceKey, timestamp string, ok bool) {
	ref = strings.TrimSpace(ref)
	prefix := "refs/confluence-sync/snapshots/"
	if !strings.HasPrefix(ref, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(ref, prefix), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func recoveryRunKey(spaceKey, timestamp string) string {
	return strings.TrimSpace(spaceKey) + "@" + strings.TrimSpace(timestamp)
}
