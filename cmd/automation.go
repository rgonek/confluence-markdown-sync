package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/charmbracelet/huh"
)

const safetyConfirmationThreshold = 10

func requireSafetyConfirmation(in io.Reader, out io.Writer, action string, changedCount int, hasDeletes bool) error {
	if changedCount <= safetyConfirmationThreshold && !hasDeletes {
		return nil
	}

	reasonParts := make([]string, 0, 2)
	if changedCount > safetyConfirmationThreshold {
		reasonParts = append(reasonParts, fmt.Sprintf("%d files", changedCount))
	}
	if hasDeletes {
		reasonParts = append(reasonParts, "deletions")
	}
	reason := strings.Join(reasonParts, " and ")

	if flagYes {
		return nil
	}
	if flagNonInteractive {
		return fmt.Errorf("%s requires confirmation (%s); rerun with --yes", action, reason)
	}

	deleteNote := ""
	if hasDeletes {
		deleteNote = " and includes delete operations"
	}
	title := fmt.Sprintf("%s will affect %d markdown file(s)%s. Continue?", action, changedCount, deleteNote)

	if outputSupportsProgress(out) {
		var confirm bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(title).
					Value(&confirm),
			),
		).WithOutput(out)
		if err := form.Run(); err != nil {
			return err
		}
		if !confirm {
			return fmt.Errorf("%s cancelled", action)
		}
		return nil
	}

	// Plain-text fallback for non-TTY environments.
	if _, err := fmt.Fprintf(out, "%s [y/N]: ", title); err != nil {
		return fmt.Errorf("write prompt: %w", err)
	}
	choice, err := readPromptLine(in)
	if err != nil {
		return err
	}
	choice = strings.ToLower(strings.TrimSpace(choice))
	if choice != "y" && choice != "yes" {
		return fmt.Errorf("%s cancelled", action)
	}
	return nil
}

func resolvePushConflictPolicy(in io.Reader, out io.Writer, onConflict string, isSpace bool) (string, error) {
	if err := validateOnConflict(onConflict); err != nil {
		return "", err
	}
	if onConflict != "" {
		slog.Info("push_conflict_policy_resolved", "policy", onConflict, "source", "flag")
		return onConflict, nil
	}

	if isSpace {
		slog.Info("push_conflict_policy_resolved", "policy", OnConflictPullMerge, "source", "default_space")
		return OnConflictPullMerge, nil
	}

	if flagNonInteractive {
		return "", fmt.Errorf("--non-interactive requires --on-conflict=pull-merge|force|cancel")
	}

	if outputSupportsProgress(out) {
		var choice string
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Conflict policy for remote-ahead pages").
					Options(
						huh.NewOption("cancel (default)", OnConflictCancel),
						huh.NewOption("pull-merge", OnConflictPullMerge),
						huh.NewOption("force", OnConflictForce),
					).
					Value(&choice),
			),
		).WithOutput(out)
		if err := form.Run(); err != nil {
			return "", err
		}
		if choice == "" {
			choice = OnConflictCancel
		}
		slog.Info("push_conflict_policy_resolved", "policy", choice, "source", "prompt")
		return choice, nil
	}

	// Plain-text fallback for non-TTY environments.
	if _, err := fmt.Fprint(out, "Conflict policy for remote-ahead pages [pull-merge/force/cancel] (default cancel): "); err != nil {
		return "", fmt.Errorf("write prompt: %w", err)
	}
	rawChoice, err := readPromptLine(in)
	if err != nil {
		return "", err
	}

	switch strings.ToLower(strings.TrimSpace(rawChoice)) {
	case "", "c", "cancel":
		slog.Info("push_conflict_policy_resolved", "policy", OnConflictCancel, "source", "prompt")
		return OnConflictCancel, nil
	case "p", "pull-merge", "pull_merge", "pullmerge":
		slog.Info("push_conflict_policy_resolved", "policy", OnConflictPullMerge, "source", "prompt")
		return OnConflictPullMerge, nil
	case "f", "force":
		slog.Info("push_conflict_policy_resolved", "policy", OnConflictForce, "source", "prompt")
		return OnConflictForce, nil
	default:
		return "", fmt.Errorf("invalid conflict policy %q: expected pull-merge, force, or cancel", rawChoice)
	}
}

func askToContinueOnDownloadError(in io.Reader, out io.Writer, attachmentID string, pageID string, err error) bool {
	if flagNonInteractive {
		return false
	}
	if flagYes {
		return true
	}

	title := warningStyle.Render(fmt.Sprintf("Error downloading attachment %s (page %s): %v", attachmentID, pageID, err)) + "\nContinue anyway?"

	if outputSupportsProgress(out) {
		var confirm bool
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(title).
					Value(&confirm),
			),
		).WithOutput(out)
		if runErr := form.Run(); runErr != nil {
			return false
		}
		return confirm
	}

	// Plain-text fallback for non-TTY environments.
	if _, writeErr := fmt.Fprintf(out, "\n%s [y/N]: ", title); writeErr != nil {
		return false
	}
	choice, rerr := readPromptLine(in)
	if rerr != nil {
		return false
	}
	choice = strings.ToLower(strings.TrimSpace(choice))
	return choice == "y" || choice == "yes"
}

func readPromptLine(in io.Reader) (string, error) {
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
