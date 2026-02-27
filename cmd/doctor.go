package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	"github.com/spf13/cobra"
)

// DoctorIssue describes a single consistency problem found by the doctor command.
type DoctorIssue struct {
	// Kind identifies the category of issue.
	Kind    string
	Path    string
	Message string
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

	if len(report.Issues) == 0 {
		_, _ = fmt.Fprintln(out, "No issues found.")
		return nil
	}

	_, _ = fmt.Fprintf(out, "\nFound %d issue(s):\n", len(report.Issues))
	for _, issue := range report.Issues {
		_, _ = fmt.Fprintf(out, "  [%s] %s: %s\n", issue.Kind, issue.Path, issue.Message)
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
func buildDoctorReport(_ context.Context, spaceDir, _ string, state fs.SpaceState) (DoctorReport, error) {
	report := DoctorReport{SpaceDir: spaceDir}

	// 1. Check every state entry: file must exist and its id frontmatter must match.
	for relPath, pageID := range state.PagePathIndex {
		relPath = normalizeRepoRelPath(relPath)
		pageID = strings.TrimSpace(pageID)
		if relPath == "" || pageID == "" {
			report.Issues = append(report.Issues, DoctorIssue{
				Kind:    "empty-index-entry",
				Path:    relPath,
				Message: "state index contains an empty path or ID; entry can be removed",
			})
			continue
		}

		absPath := filepath.Join(spaceDir, filepath.FromSlash(relPath))
		doc, readErr := fs.ReadMarkdownDocument(absPath)
		if os.IsNotExist(readErr) || (readErr != nil && strings.Contains(readErr.Error(), "no such file")) {
			report.Issues = append(report.Issues, DoctorIssue{
				Kind:    "missing-file",
				Path:    relPath,
				Message: fmt.Sprintf("state tracks page %s but file does not exist on disk", pageID),
			})
			continue
		}
		if readErr != nil {
			report.Issues = append(report.Issues, DoctorIssue{
				Kind:    "unreadable-file",
				Path:    relPath,
				Message: fmt.Sprintf("cannot read file: %v", readErr),
			})
			continue
		}

		frontmatterID := strings.TrimSpace(doc.Frontmatter.ID)
		if frontmatterID != pageID {
			report.Issues = append(report.Issues, DoctorIssue{
				Kind:    "id-mismatch",
				Path:    relPath,
				Message: fmt.Sprintf("state has id=%s but file frontmatter has id=%s", pageID, frontmatterID),
			})
		}

		// Check for git conflict markers in the file.
		if containsConflictMarkers(doc.Body) {
			report.Issues = append(report.Issues, DoctorIssue{
				Kind:    "conflict-markers",
				Path:    relPath,
				Message: "file contains unresolved git conflict markers",
			})
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
			report.Issues = append(report.Issues, DoctorIssue{
				Kind:    "untracked-id",
				Path:    relPath,
				Message: fmt.Sprintf("file has id=%s in frontmatter but is not tracked in state index", pageID),
			})
		}
	}

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
