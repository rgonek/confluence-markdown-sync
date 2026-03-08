package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

func TestRunRecover_ListsRetainedFailedPushArtifacts(t *testing.T) {
	runParallelCommandTest(t)

	_, spaceDir, syncBranch, snapshotRef := createFailedPushRecoveryRun(t)
	chdirRepo(t, spaceDir)

	out, err := runRecoverForTest(t)
	if err != nil {
		t.Fatalf("recover inspection failed: %v\nOutput:\n%s", err, out)
	}

	if !strings.Contains(out, syncBranch) {
		t.Fatalf("expected recover output to include sync branch %q, got:\n%s", syncBranch, out)
	}
	if !strings.Contains(out, snapshotRef) {
		t.Fatalf("expected recover output to include snapshot ref %q, got:\n%s", snapshotRef, out)
	}
	if !strings.Contains(out, "simulated update failure") {
		t.Fatalf("expected recover output to include failure reason, got:\n%s", out)
	}
}

func TestRunRecover_DiscardAllRemovesAbandonedRecoveryArtifacts(t *testing.T) {
	runParallelCommandTest(t)

	repo, spaceDir, syncBranch, snapshotRef := createFailedPushRecoveryRun(t)
	chdirRepo(t, spaceDir)

	out, err := runRecoverForTest(t, "--discard-all", "--yes", "--non-interactive")
	if err != nil {
		t.Fatalf("recover discard failed: %v\nOutput:\n%s", err, out)
	}

	if branchList := strings.TrimSpace(runGitForTest(t, repo, "branch", "--list", syncBranch)); branchList != "" {
		t.Fatalf("expected sync branch to be deleted, got %q", branchList)
	}
	if refs := strings.TrimSpace(runGitForTest(t, repo, "for-each-ref", "--format=%(refname)", snapshotRef)); refs != "" {
		t.Fatalf("expected snapshot ref to be deleted, got %q", refs)
	}
	if !strings.Contains(out, "Discarded recovery run: "+syncBranch) {
		t.Fatalf("expected discarded recovery output, got:\n%s", out)
	}
}

func TestRunRecover_DiscardAllPreservesCurrentRecoveryBranch(t *testing.T) {
	runParallelCommandTest(t)

	repo, spaceDir, syncBranch, snapshotRef := createFailedPushRecoveryRun(t)
	chdirRepo(t, repo)
	runGitForTest(t, repo, "checkout", syncBranch)

	out, err := runRecoverForTest(t, "--discard-all", "--yes", "--non-interactive")
	if err != nil {
		t.Fatalf("recover discard on current branch failed: %v\nOutput:\n%s", err, out)
	}

	if branchList := strings.TrimSpace(runGitForTest(t, repo, "branch", "--list", syncBranch)); branchList == "" {
		t.Fatalf("expected current sync branch to be retained")
	}
	if refs := strings.TrimSpace(runGitForTest(t, repo, "for-each-ref", "--format=%(refname)", snapshotRef)); refs != snapshotRef {
		t.Fatalf("expected snapshot ref to be retained, got %q", refs)
	}
	if !strings.Contains(out, "Retained recovery run "+syncBranch+": current HEAD is on this sync branch") {
		t.Fatalf("expected retained current-branch reason, got:\n%s", out)
	}

	_ = spaceDir
}

func TestRunRecover_SkipsCorruptRecoveryMetadataFiles(t *testing.T) {
	runParallelCommandTest(t)

	repo, spaceDir, syncBranch, _ := createFailedPushRecoveryRun(t)
	badMetadataDir := filepath.Join(repo, ".git", "confluence-recovery", "ENG")
	if err := os.MkdirAll(badMetadataDir, 0o750); err != nil {
		t.Fatalf("mkdir bad metadata dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badMetadataDir, "bad.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("write bad metadata: %v", err)
	}
	chdirRepo(t, spaceDir)

	out, err := runRecoverForTest(t)
	if err != nil {
		t.Fatalf("recover inspection failed: %v\nOutput:\n%s", err, out)
	}

	if !strings.Contains(out, syncBranch) {
		t.Fatalf("expected recover output to include valid sync branch %q, got:\n%s", syncBranch, out)
	}
	if !strings.Contains(out, "warning: skipping unreadable recovery metadata") {
		t.Fatalf("expected warning for corrupt recovery metadata, got:\n%s", out)
	}
	if !strings.Contains(out, "bad.json") {
		t.Fatalf("expected warning to mention corrupt metadata file, got:\n%s", out)
	}
}

func TestRunRecover_NoArtifactsReportsClean(t *testing.T) {
	runParallelCommandTest(t)

	repo := t.TempDir()
	spaceDir := preparePushRepoWithBaseline(t, repo)
	chdirRepo(t, spaceDir)

	out, err := runRecoverForTest(t)
	if err != nil {
		t.Fatalf("recover on clean repo failed: %v\nOutput:\n%s", err, out)
	}

	if !strings.Contains(out, "no retained failed push artifacts found") {
		t.Fatalf("expected clean-state message, got:\n%s", out)
	}
}

func TestRunRecover_SectionedOutputFormat(t *testing.T) {
	runParallelCommandTest(t)

	_, spaceDir, syncBranch, snapshotRef := createFailedPushRecoveryRun(t)
	chdirRepo(t, spaceDir)

	out, err := runRecoverForTest(t)
	if err != nil {
		t.Fatalf("recover inspection failed: %v\nOutput:\n%s", err, out)
	}

	// Output must use the sectioned format.
	if !strings.Contains(out, "Recovery artifacts:") {
		t.Fatalf("expected 'Recovery artifacts:' section header, got:\n%s", out)
	}
	if !strings.Contains(out, "Snapshot refs:") {
		t.Fatalf("expected 'Snapshot refs:' section, got:\n%s", out)
	}
	if !strings.Contains(out, "Sync branches:") {
		t.Fatalf("expected 'Sync branches:' section, got:\n%s", out)
	}
	if !strings.Contains(out, "Failed runs:") {
		t.Fatalf("expected 'Failed runs:' section, got:\n%s", out)
	}

	// Each section must include the actual artifact identifiers.
	if !strings.Contains(out, snapshotRef) {
		t.Fatalf("expected snapshot ref %q in output, got:\n%s", snapshotRef, out)
	}
	if !strings.Contains(out, syncBranch) {
		t.Fatalf("expected sync branch %q in output, got:\n%s", syncBranch, out)
	}

	// The Failed runs section must include structured detail lines.
	if !strings.Contains(out, "Branch: "+syncBranch) {
		t.Fatalf("expected 'Branch:' detail line in Failed runs section, got:\n%s", out)
	}
	if !strings.Contains(out, "Snapshot: "+snapshotRef) {
		t.Fatalf("expected 'Snapshot:' detail line in Failed runs section, got:\n%s", out)
	}
	if !strings.Contains(out, "simulated update failure") {
		t.Fatalf("expected failure reason in Failed runs section, got:\n%s", out)
	}
}

func createFailedPushRecoveryRun(t *testing.T) (repo string, spaceDir string, syncBranch string, snapshotRef string) {
	t.Helper()

	repo = t.TempDir()
	spaceDir = preparePushRepoWithBaseline(t, repo)

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "Updated local content that will fail\n",
	})
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "local change")

	fake := newCmdFakePushRemote(1)
	failingFake := &failingPushRemote{cmdFakePushRemote: fake}

	oldPushFactory := newPushRemote
	oldPullFactory := newPullRemote
	newPushRemote = func(_ *config.Config) (syncflow.PushRemote, error) { return failingFake, nil }
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) { return failingFake, nil }
	t.Cleanup(func() {
		newPushRemote = oldPushFactory
		newPullRemote = oldPullFactory
	})

	setupEnv(t)
	chdirRepo(t, spaceDir)

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: ""}, OnConflictCancel, false)
	if err == nil {
		t.Fatal("runPush() expected error")
	}

	snapshotRef = strings.TrimSpace(runGitForTest(t, repo, "for-each-ref", "--format=%(refname)", "refs/confluence-sync/snapshots/ENG/"))
	if snapshotRef == "" {
		t.Fatal("expected snapshot ref to be retained")
	}
	syncBranch = strings.TrimSpace(runGitForTest(t, repo, "for-each-ref", "--format=%(refname:short)", "refs/heads/sync/ENG/"))
	if syncBranch == "" {
		t.Fatal("expected sync branch to be retained")
	}

	return repo, spaceDir, syncBranch, snapshotRef
}

func runRecoverForTest(t *testing.T, args ...string) (string, error) {
	t.Helper()

	previousYes := flagYes
	previousNonInteractive := flagNonInteractive
	defer func() {
		flagYes = previousYes
		flagNonInteractive = previousNonInteractive
	}()

	cmd := newRecoverCmd()
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader("y\n"))
	cmd.SetArgs(args)

	err := cmd.Execute()
	return out.String(), err
}
