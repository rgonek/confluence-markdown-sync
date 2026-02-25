package cmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

var runIDPattern = regexp.MustCompile(`run_id=([^\s]+)`)

func TestRunPush_LifecycleLogsIncludeStableRunID(t *testing.T) {
	previousPreflight := flagPushPreflight
	flagPushPreflight = true
	t.Cleanup(func() { flagPushPreflight = previousPreflight })

	logs, restore := captureInfoLogs(t)
	defer restore()

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	err := runPush(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"}, "", true)
	if err == nil {
		t.Fatal("expected runPush() to fail for --preflight with --dry-run")
	}

	assertLifecycleRunID(t, logs.String(), "push_started", "push_finished", "push")
}

func TestRunPull_LifecycleLogsIncludeStableRunID(t *testing.T) {
	repo := t.TempDir()
	chdirRepo(t, repo)
	setupEnv(t)

	oldFactory := newPullRemote
	newPullRemote = func(_ *config.Config) (syncflow.PullRemote, error) {
		return nil, errors.New("simulated client failure")
	}
	t.Cleanup(func() { newPullRemote = oldFactory })

	logs, restore := captureInfoLogs(t)
	defer restore()

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})

	err := runPull(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"})
	if err == nil {
		t.Fatal("expected runPull() to fail when remote factory fails")
	}

	assertLifecycleRunID(t, logs.String(), "pull_started", "pull_finished", "pull")
}

func TestRunDiff_LifecycleLogsIncludeStableRunID(t *testing.T) {
	logs, restore := captureInfoLogs(t)
	defer restore()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetContext(ctx)

	err := runDiff(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got: %v", err)
	}

	assertLifecycleRunID(t, logs.String(), "diff_started", "diff_finished", "diff")
}

func TestRunValidate_LifecycleLogsIncludeStableRunID(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "ENG"), 0o750); err != nil {
		t.Fatalf("mkdir ENG dir: %v", err)
	}
	chdirRepo(t, repo)

	logs, restore := captureInfoLogs(t)
	defer restore()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetContext(ctx)

	err := runValidateCommand(cmd, config.Target{Mode: config.TargetModeSpace, Value: "ENG"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got: %v", err)
	}

	assertLifecycleRunID(t, logs.String(), "validate_started", "validate_finished", "validate")
}

func captureInfoLogs(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()

	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	return &buf, func() {
		slog.SetDefault(previous)
	}
}

func assertLifecycleRunID(t *testing.T, logs, startEvent, endEvent, command string) {
	t.Helper()

	startLine := findLogLine(logs, startEvent)
	endLine := findLogLine(logs, endEvent)
	if startLine == "" {
		t.Fatalf("missing %s log line in:\n%s", startEvent, logs)
	}
	if endLine == "" {
		t.Fatalf("missing %s log line in:\n%s", endEvent, logs)
	}

	startRunID := extractRunID(startLine)
	endRunID := extractRunID(endLine)
	if startRunID == "" || endRunID == "" {
		t.Fatalf("expected run_id on lifecycle lines:\nstart=%s\nend=%s", startLine, endLine)
	}
	if startRunID != endRunID {
		t.Fatalf("run IDs differ across lifecycle logs: start=%s end=%s", startRunID, endRunID)
	}

	if !strings.Contains(startLine, "command="+command) {
		t.Fatalf("start log missing command=%s: %s", command, startLine)
	}
	if !strings.Contains(endLine, "command="+command) {
		t.Fatalf("finish log missing command=%s: %s", command, endLine)
	}
}

func findLogLine(logs, marker string) string {
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, marker) {
			return line
		}
	}
	return ""
}

func extractRunID(line string) string {
	match := runIDPattern.FindStringSubmatch(line)
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}
