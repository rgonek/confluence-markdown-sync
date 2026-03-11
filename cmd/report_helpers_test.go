package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func decodeCommandReportJSON(t *testing.T, raw []byte) commandReportJSON {
	t.Helper()

	var report commandReportJSON
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("output is not valid JSON report: %v\n%s", err, string(raw))
	}
	return report
}

func assertReportMetadata(t *testing.T, report commandReportJSON, command string, success bool) {
	t.Helper()

	if report.Command != command {
		t.Fatalf("command = %q, want %q", report.Command, command)
	}
	if report.Success != success {
		t.Fatalf("success = %v, want %v", report.Success, success)
	}
	if strings.TrimSpace(report.RunID) == "" {
		t.Fatal("run_id should not be empty")
	}
	if strings.TrimSpace(report.Timing.StartedAt) == "" {
		t.Fatal("timing.started_at should not be empty")
	}
	if strings.TrimSpace(report.Timing.FinishedAt) == "" {
		t.Fatal("timing.finished_at should not be empty")
	}
	if report.Timing.DurationMs < 0 {
		t.Fatalf("timing.duration_ms = %d, want >= 0", report.Timing.DurationMs)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsDiagnostic(report commandReportJSON, code, field string) bool {
	for _, diag := range report.Diagnostics {
		if diag.Code == code && diag.Field == field {
			return true
		}
	}
	return false
}

func containsDiagnosticCode(report commandReportJSON, code string) bool {
	for _, diag := range report.Diagnostics {
		if diag.Code == code {
			return true
		}
	}
	return false
}

func containsRecoveryArtifact(report commandReportJSON, artifactType, status string) bool {
	for _, artifact := range report.RecoveryArtifacts {
		if artifact.Type == artifactType && artifact.Status == status {
			return true
		}
	}
	return false
}

func createUnmergedWorkspaceRepo(t *testing.T) string {
	t.Helper()

	repo := t.TempDir()
	setupGitRepo(t, repo)

	path := filepath.Join(repo, "conflict.md")
	if err := os.WriteFile(path, []byte("base\n"), 0o600); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "base")

	runGitForTest(t, repo, "checkout", "-b", "topic")
	if err := os.WriteFile(path, []byte("topic\n"), 0o600); err != nil {
		t.Fatalf("write topic file: %v", err)
	}
	runGitForTest(t, repo, "commit", "-am", "topic change")

	runGitForTest(t, repo, "checkout", "main")
	if err := os.WriteFile(path, []byte("main\n"), 0o600); err != nil {
		t.Fatalf("write main file: %v", err)
	}
	runGitForTest(t, repo, "commit", "-am", "main change")

	cmd := exec.Command("git", "merge", "topic") //nolint:gosec // test helper intentionally creates merge conflict
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected git merge topic to fail, output:\n%s", string(out))
	}

	return repo
}
