package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/rgonek/confluence-markdown-sync/internal/git"
	"github.com/spf13/cobra"
)

// DoctorIssue describes a single consistency problem found by the doctor command.
type DoctorIssue struct {
	// Kind identifies the category of issue.
	Kind       string
	Path       string
	Message    string
	Severity   string
	Repairable bool
}

// DoctorReport is the full set of issues found for a space.
type DoctorReport struct {
	SpaceDir string
	SpaceKey string
	Issues   []DoctorIssue
}

func newDoctorCmd() *cobra.Command {
	var repair bool

	cmd := &cobra.Command{
		Use:   "doctor [TARGET]",
		Short: "Check local sync state consistency",
		Long: `doctor inspects the local workspace for consistency issues between
.confluence-state.json, the actual Markdown files on disk, and the git index.

TARGET follows the standard rule:
- .md suffix  => file mode (space inferred from file)
- otherwise   => space mode (SPACE_KEY or space directory).

Use --repair to automatically fix detected issues.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var raw string
			if len(args) > 0 {
				raw = args[0]
			}
			return runDoctor(cmd, config.ParseTarget(raw), repair)
		},
	}

	cmd.Flags().BoolVar(&repair, "repair", false, "Automatically repair detected issues where possible")

	return cmd
}

func runDoctor(cmd *cobra.Command, target config.Target, repair bool) error {
	out := ensureSynchronizedCmdOutput(cmd)

	initialCtx, err := resolveInitialPullContext(target)
	if err != nil {
		return err
	}
	if !dirExists(initialCtx.spaceDir) {
		return fmt.Errorf("space directory not found: %s", initialCtx.spaceDir)
	}

	state, err := fs.LoadState(initialCtx.spaceDir)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	spaceKey := strings.TrimSpace(initialCtx.spaceKey)
	if spaceKey == "" {
		spaceKey = strings.TrimSpace(state.SpaceKey)
	}

	_, _ = fmt.Fprintf(out, "Doctor: %s (%s)\n", initialCtx.spaceDir, spaceKey)

	report, err := buildDoctorReport(context.Background(), initialCtx.spaceDir, spaceKey, state)
	if err != nil {
		return err
	}
	appendDoctorGitIssues(&report)

	if len(report.Issues) == 0 {
		_, _ = fmt.Fprintln(out, "No issues found.")
		return nil
	}

	_, _ = fmt.Fprintf(out, "\nFound %d issue(s):\n", len(report.Issues))
	for _, issue := range report.Issues {
		repairability := "manual"
		if issue.Repairable {
			repairability = "repairable"
		}
		_, _ = fmt.Fprintf(out, "  [%s][%s] %s: %s: %s\n", issue.Severity, repairability, issue.Kind, issue.Path, issue.Message)
	}

	if !repair {
		_, _ = fmt.Fprintln(out, "\nRun with --repair to automatically fix repairable issues.")
		return nil
	}

	repaired, repairErrors := repairDoctorIssues(out, initialCtx.spaceDir, state, report.Issues)
	if repaired > 0 {
		// Save the potentially updated state.
		if saveErr := fs.SaveState(initialCtx.spaceDir, state); saveErr != nil {
			repairErrors = append(repairErrors, fmt.Sprintf("save state: %v", saveErr))
		}
	}

	_, _ = fmt.Fprintf(out, "\nRepaired %d issue(s).\n", repaired)
	if len(repairErrors) > 0 {
		_, _ = fmt.Fprintf(out, "%d issue(s) could not be repaired automatically:\n", len(repairErrors))
		for _, e := range repairErrors {
			_, _ = fmt.Fprintf(out, "  - %s\n", e)
		}
	}
	return nil
}

// buildDoctorReport scans the space directory and state for consistency issues.
func buildDoctorReport(_ context.Context, spaceDir, spaceKey string, state fs.SpaceState) (DoctorReport, error) {
	report := DoctorReport{
		SpaceDir: spaceDir,
		SpaceKey: strings.TrimSpace(spaceKey),
	}

	// 1. Check every state entry: file must exist and its id frontmatter must match.
	for relPath, pageID := range state.PagePathIndex {
		relPath = normalizeRepoRelPath(relPath)
		pageID = strings.TrimSpace(pageID)
		if relPath == "" || pageID == "" {
			report.Issues = append(report.Issues, newDoctorIssue(
				"empty-index-entry",
				relPath,
				"state index contains an empty path or ID; entry can be removed",
				"error",
				true,
			))
			continue
		}

		absPath := filepath.Join(spaceDir, filepath.FromSlash(relPath))
		doc, readErr := fs.ReadMarkdownDocument(absPath)
		if os.IsNotExist(readErr) || (readErr != nil && strings.Contains(readErr.Error(), "no such file")) {
			report.Issues = append(report.Issues, newDoctorIssue(
				"missing-file",
				relPath,
				fmt.Sprintf("state tracks page %s but file does not exist on disk", pageID),
				"error",
				true,
			))
			continue
		}
		if readErr != nil {
			report.Issues = append(report.Issues, newDoctorIssue(
				"unreadable-file",
				relPath,
				fmt.Sprintf("cannot read file: %v", readErr),
				"error",
				false,
			))
			continue
		}

		frontmatterID := strings.TrimSpace(doc.Frontmatter.ID)
		if frontmatterID != pageID {
			report.Issues = append(report.Issues, newDoctorIssue(
				"id-mismatch",
				relPath,
				fmt.Sprintf("state has id=%s but file frontmatter has id=%s", pageID, frontmatterID),
				"error",
				false,
			))
		}

		// Check for git conflict markers in the file.
		if containsConflictMarkers(doc.Body) {
			report.Issues = append(report.Issues, newDoctorIssue(
				"conflict-markers",
				relPath,
				"file contains unresolved git conflict markers",
				"error",
				false,
			))
		}

		if containsUnknownMediaPlaceholder(doc.Body) {
			report.Issues = append(report.Issues, newDoctorIssue(
				"unknown-media-placeholder",
				relPath,
				"file contains unresolved UNKNOWN_MEDIA_ID placeholder content from best-effort sync fallback",
				"warning",
				false,
			))
		} else if containsEmbeddedContentPlaceholder(doc.Body) {
			report.Issues = append(report.Issues, newDoctorIssue(
				"embedded-content-placeholder",
				relPath,
				"file contains unresolved embedded-content placeholder text from degraded round-trip output",
				"warning",
				false,
			))
		}
	}

	// 2. Check for .md files whose id frontmatter is NOT tracked in state.
	localIDs, err := scanLocalMarkdownIDs(spaceDir)
	if err != nil {
		return report, fmt.Errorf("scan local markdown: %w", err)
	}

	// Build reverse index: pageID -> relPath from state.
	stateIDSet := make(map[string]struct{}, len(state.PagePathIndex))
	for _, pageID := range state.PagePathIndex {
		if id := strings.TrimSpace(pageID); id != "" {
			stateIDSet[id] = struct{}{}
		}
	}

	for pageID, relPath := range localIDs {
		if _, tracked := stateIDSet[pageID]; !tracked {
			report.Issues = append(report.Issues, newDoctorIssue(
				"untracked-id",
				relPath,
				fmt.Sprintf("file has id=%s in frontmatter but is not tracked in state index", pageID),
				"warning",
				true,
			))
		}
	}

	report.Issues = append(report.Issues, detectHierarchyLayoutIssues(spaceDir)...)
	sortDoctorIssues(report.Issues)
	return report, nil
}

// repairDoctorIssues attempts to fix repairable issues in-place.
// It mutates state and writes files as needed. Returns count of repaired issues.
func repairDoctorIssues(out io.Writer, spaceDir string, state fs.SpaceState, issues []DoctorIssue) (int, []string) {
	repaired := 0
	var errs []string

	for _, issue := range issues {
		switch issue.Kind {
		case "missing-file":
			// Remove the stale state entry.
			for relPath, pageID := range state.PagePathIndex {
				if normalizeRepoRelPath(relPath) == issue.Path {
					delete(state.PagePathIndex, relPath)
					_, _ = fmt.Fprintf(out, "  repaired [missing-file]: removed stale state entry for %s (id=%s)\n", issue.Path, pageID)
					repaired++
					break
				}
			}

		case "empty-index-entry":
			// Remove entries with empty path or ID.
			for relPath, pageID := range state.PagePathIndex {
				if strings.TrimSpace(normalizeRepoRelPath(relPath)) == "" || strings.TrimSpace(pageID) == "" {
					delete(state.PagePathIndex, relPath)
					_, _ = fmt.Fprintf(out, "  repaired [empty-index-entry]: removed blank state entry\n")
					repaired++
				}
			}

		case "untracked-id":
			// Add the file's id to the state index.
			absPath := filepath.Join(spaceDir, filepath.FromSlash(issue.Path))
			doc, readErr := fs.ReadMarkdownDocument(absPath)
			if readErr != nil {
				errs = append(errs, fmt.Sprintf("[untracked-id] %s: cannot read: %v", issue.Path, readErr))
				continue
			}
			pageID := strings.TrimSpace(doc.Frontmatter.ID)
			if pageID == "" {
				errs = append(errs, fmt.Sprintf("[untracked-id] %s: frontmatter id is empty", issue.Path))
				continue
			}
			state.PagePathIndex[issue.Path] = pageID
			_, _ = fmt.Fprintf(out, "  repaired [untracked-id]: added %s -> %s to state index\n", issue.Path, pageID)
			repaired++

		case "stale-sync-branch":
			client, err := git.NewClient()
			if err != nil {
				errs = append(errs, fmt.Sprintf("[stale-sync-branch] %s: initialize git client: %v", issue.Path, err))
				continue
			}
			if err := client.DeleteBranch(issue.Path); err != nil {
				errs = append(errs, fmt.Sprintf("[stale-sync-branch] %s: delete branch: %v", issue.Path, err))
				continue
			}
			_, _ = fmt.Fprintf(out, "  repaired [stale-sync-branch]: deleted stale recovery branch %s\n", issue.Path)
			repaired++

		default:
			errs = append(errs, fmt.Sprintf("[%s] %s: %s — manual resolution required", issue.Kind, issue.Path, issue.Message))
		}
	}

	return repaired, errs
}

// containsConflictMarkers returns true if the text contains git conflict marker lines.
func containsConflictMarkers(text string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<<<<<<<") ||
			strings.HasPrefix(trimmed, "=======") ||
			strings.HasPrefix(trimmed, ">>>>>>>") {
			return true
		}
	}
	return false
}

func newDoctorIssue(kind, path, message, severity string, repairable bool) DoctorIssue {
	return DoctorIssue{
		Kind:       kind,
		Path:       normalizeRepoRelPath(path),
		Message:    message,
		Severity:   strings.TrimSpace(strings.ToLower(severity)),
		Repairable: repairable,
	}
}

func sortDoctorIssues(issues []DoctorIssue) {
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Severity != issues[j].Severity {
			return issues[i].Severity < issues[j].Severity
		}
		if issues[i].Path != issues[j].Path {
			return issues[i].Path < issues[j].Path
		}
		return issues[i].Kind < issues[j].Kind
	})
}

func containsUnknownMediaPlaceholder(text string) bool {
	return strings.Contains(text, "UNKNOWN_MEDIA_ID")
}

func containsEmbeddedContentPlaceholder(text string) bool {
	return strings.Contains(text, "[Embedded content]")
}

func detectHierarchyLayoutIssues(spaceDir string) []DoctorIssue {
	paths, err := listDoctorMarkdownPaths(spaceDir)
	if err != nil {
		return []DoctorIssue{newDoctorIssue(
			"hierarchy-layout-scan",
			spaceDir,
			fmt.Sprintf("cannot scan markdown hierarchy: %v", err),
			"error",
			false,
		)}
	}

	pathSet := make(map[string]struct{}, len(paths))
	for _, relPath := range paths {
		pathSet[normalizeRepoRelPath(relPath)] = struct{}{}
	}

	issues := make([]DoctorIssue, 0)
	for _, relPath := range paths {
		normalized := normalizeRepoRelPath(relPath)
		if normalized == "" {
			continue
		}
		stem := strings.TrimSuffix(filepath.Base(normalized), filepath.Ext(normalized))
		if stem == "" {
			continue
		}
		dir := normalizeRepoRelPath(filepath.Dir(normalized))
		expectedIndex := normalizeRepoRelPath(filepath.Join(dir, stem, stem+".md"))
		if expectedIndex == normalized {
			continue
		}
		hasChildMarkdown := false
		childPrefix := normalizeRepoRelPath(filepath.Join(dir, stem)) + "/"
		for candidate := range pathSet {
			if strings.HasPrefix(candidate, childPrefix) && candidate != expectedIndex {
				hasChildMarkdown = true
				break
			}
		}
		if !hasChildMarkdown {
			continue
		}
		issues = append(issues, newDoctorIssue(
			"hierarchy-layout",
			normalized,
			fmt.Sprintf("page has nested child markdown under %s but parent pages with children must live at %s", childPrefix[:len(childPrefix)-1], expectedIndex),
			"warning",
			false,
		))
	}
	return issues
}

func listDoctorMarkdownPaths(spaceDir string) ([]string, error) {
	paths := make([]string, 0)
	err := filepath.WalkDir(spaceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() != "." && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(spaceDir, path)
		if relErr != nil {
			return relErr
		}
		paths = append(paths, normalizeRepoRelPath(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

func appendDoctorGitIssues(report *DoctorReport) {
	targetSpaceKey := fs.SanitizePathSegment(report.SpaceKey)
	if targetSpaceKey == "" {
		return
	}

	client, err := git.NewClient()
	if err != nil {
		return
	}

	currentBranch, err := client.CurrentBranch()
	if err != nil {
		return
	}
	syncBranches, err := listCleanSyncBranches(client)
	if err != nil {
		return
	}
	worktreeBranches, err := listCleanWorktreeBranches(client)
	if err != nil {
		return
	}

	for _, branch := range syncBranches {
		if branch == currentBranch {
			continue
		}
		if !doctorSyncBranchMatchesSpace(branch, targetSpaceKey) {
			continue
		}
		if _, ok := managedSnapshotRefForSyncBranch(branch); !ok {
			continue
		}
		if len(worktreeBranches[branch]) > 0 {
			continue
		}
		report.Issues = append(report.Issues, newDoctorIssue(
			"stale-sync-branch",
			branch,
			"managed recovery branch has no linked worktree and appears to be abandoned",
			"warning",
			true,
		))
	}
	sortDoctorIssues(report.Issues)
}

func doctorSyncBranchMatchesSpace(branch, targetSpaceKey string) bool {
	parts := strings.Split(strings.TrimSpace(branch), "/")
	return len(parts) == 3 && parts[0] == "sync" && parts[1] == targetSpaceKey
}
