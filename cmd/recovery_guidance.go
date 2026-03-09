package cmd

import (
	"fmt"
	"io"
	"strings"
)

func recoveryRunSelector(spaceKey, timestamp string) string {
	spaceKey = strings.TrimSpace(spaceKey)
	timestamp = strings.TrimSpace(timestamp)
	if spaceKey == "" {
		return timestamp
	}
	if timestamp == "" {
		return spaceKey
	}
	return spaceKey + "/" + timestamp
}

func recoveryInspectBranchCommand(syncBranch string) string {
	syncBranch = strings.TrimSpace(syncBranch)
	if syncBranch == "" {
		return ""
	}
	return fmt.Sprintf("git switch %s", syncBranch)
}

func recoveryInspectDiffCommand(snapshotRef, syncBranch string) string {
	snapshotRef = strings.TrimSpace(snapshotRef)
	syncBranch = strings.TrimSpace(syncBranch)
	if snapshotRef == "" || syncBranch == "" {
		return ""
	}
	return fmt.Sprintf("git diff %s..%s", snapshotRef, syncBranch)
}

func recoveryDiscardCommand(spaceKey, timestamp string) string {
	selector := recoveryRunSelector(spaceKey, timestamp)
	if selector == "" {
		return ""
	}
	return fmt.Sprintf("conf recover --discard %s --yes", selector)
}

func printPushRecoveryGuidance(out io.Writer, spaceKey, timestamp, syncBranch, snapshotRef string) {
	selector := recoveryRunSelector(spaceKey, timestamp)
	_, _ = fmt.Fprintln(out, "Next steps:")
	_, _ = fmt.Fprintln(out, "  conf recover")
	if inspect := recoveryInspectBranchCommand(syncBranch); inspect != "" {
		_, _ = fmt.Fprintf(out, "  %s\n", inspect)
	}
	if inspectDiff := recoveryInspectDiffCommand(snapshotRef, syncBranch); inspectDiff != "" {
		_, _ = fmt.Fprintf(out, "  %s\n", inspectDiff)
	}
	if discard := recoveryDiscardCommand(spaceKey, timestamp); discard != "" {
		_, _ = fmt.Fprintf(out, "  %s\n", discard)
	}
	if selector != "" {
		_, _ = fmt.Fprintf(out, "Recovery run selector: %s\n", selector)
	}
}
