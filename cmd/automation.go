package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
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
	fmt.Fprintf(out, "%s will affect %d markdown file(s)%s. Continue? [y/N]: ", action, changedCount, deleteNote)
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

func resolvePushConflictPolicy(in io.Reader, out io.Writer, onConflict string) (string, error) {
	if err := validateOnConflict(onConflict); err != nil {
		return "", err
	}
	if onConflict != "" {
		return onConflict, nil
	}

	if flagNonInteractive {
		return "", fmt.Errorf("--non-interactive requires --on-conflict=pull-merge|force|cancel")
	}

	fmt.Fprint(out, "Conflict policy for remote-ahead pages [pull-merge/force/cancel] (default cancel): ")
	choice, err := readPromptLine(in)
	if err != nil {
		return "", err
	}

	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "", "c", "cancel":
		return OnConflictCancel, nil
	case "p", "pull-merge", "pull_merge", "pullmerge":
		return OnConflictPullMerge, nil
	case "f", "force":
		return OnConflictForce, nil
	default:
		return "", fmt.Errorf("invalid conflict policy %q: expected pull-merge, force, or cancel", choice)
	}
}

func readPromptLine(in io.Reader) (string, error) {
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
