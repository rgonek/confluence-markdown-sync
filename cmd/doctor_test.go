package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestNewDoctorCmd(t *testing.T) {
	cmd := newDoctorCmd()
	if cmd == nil {
		t.Fatal("expected command not to be nil")
	}
	if cmd.Use != "doctor [TARGET]" {
		t.Fatalf("expected use 'doctor [TARGET]', got %s", cmd.Use)
	}
}

func TestContainsConflictMarkers(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"no markers", "# Hello\n\nContent here", false},
		{"conflict start marker", "<<<<<<< HEAD\ncontent", true},
		{"conflict separator", "content\n=======\nother", true},
		{"conflict end marker", "content\n>>>>>>> branch", true},
		{"marker in middle of content", "before\n<<<<<<< HEAD\nafter", true},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := containsConflictMarkers(tc.input)
			if got != tc.want {
				t.Errorf("containsConflictMarkers(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestBuildDoctorReport_MissingFile(t *testing.T) {
	dir := t.TempDir()

	state := fs.NewSpaceState()
	state.SpaceKey = "TEST"
	state.PagePathIndex = map[string]string{
		"missing.md": "123",
	}

	report, err := buildDoctorReport(nil, dir, "TEST", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Kind == "missing-file" && issue.Path == "missing.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing-file issue for missing.md, got: %v", report.Issues)
	}
}

func TestBuildDoctorReport_IDMismatch(t *testing.T) {
	dir := t.TempDir()

	content := "---\nid: \"999\"\nversion: 1\n---\n\nHello"
	if err := os.WriteFile(filepath.Join(dir, "page.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	state := fs.NewSpaceState()
	state.SpaceKey = "TEST"
	state.PagePathIndex = map[string]string{
		"page.md": "123", // mismatch: state says 123, file says 999
	}

	report, err := buildDoctorReport(nil, dir, "TEST", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Kind == "id-mismatch" && issue.Path == "page.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected id-mismatch issue for page.md, got: %v", report.Issues)
	}
}

func TestBuildDoctorReport_ConflictMarkers(t *testing.T) {
	dir := t.TempDir()

	content := "---\nid: \"123\"\nversion: 1\n---\n\n<<<<<<< HEAD\nmy content\n=======\ntheir content\n>>>>>>> branch\n"
	if err := os.WriteFile(filepath.Join(dir, "conflict.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	state := fs.NewSpaceState()
	state.SpaceKey = "TEST"
	state.PagePathIndex = map[string]string{
		"conflict.md": "123",
	}

	report, err := buildDoctorReport(nil, dir, "TEST", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Kind == "conflict-markers" && issue.Path == "conflict.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected conflict-markers issue for conflict.md, got: %v", report.Issues)
	}
}

func TestBuildDoctorReport_UntrackedID(t *testing.T) {
	dir := t.TempDir()

	// File with an id that is NOT in the state index
	content := "---\nid: \"456\"\nversion: 1\n---\n\nOrphan page"
	if err := os.WriteFile(filepath.Join(dir, "orphan.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	state := fs.NewSpaceState()
	state.SpaceKey = "TEST"
	// state.PagePathIndex is empty — nothing tracked

	report, err := buildDoctorReport(nil, dir, "TEST", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	found := false
	for _, issue := range report.Issues {
		if issue.Kind == "untracked-id" && issue.Path == "orphan.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected untracked-id issue for orphan.md, got: %v", report.Issues)
	}
}

func TestBuildDoctorReport_CleanState(t *testing.T) {
	dir := t.TempDir()

	content := "---\nid: \"123\"\nversion: 2\n---\n\nAll good"
	if err := os.WriteFile(filepath.Join(dir, "clean.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	state := fs.NewSpaceState()
	state.SpaceKey = "TEST"
	state.PagePathIndex = map[string]string{
		"clean.md": "123",
	}

	report, err := buildDoctorReport(nil, dir, "TEST", state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Issues) != 0 {
		t.Fatalf("expected no issues, got: %v", report.Issues)
	}
}

func TestRepairDoctorIssues_MissingFile(t *testing.T) {
	dir := t.TempDir()

	state := fs.NewSpaceState()
	state.SpaceKey = "TEST"
	state.PagePathIndex = map[string]string{
		"missing.md": "123",
	}

	issues := []DoctorIssue{
		{Kind: "missing-file", Path: "missing.md", Message: "file not found"},
	}

	out := new(bytes.Buffer)
	repaired, errs := repairDoctorIssues(out, dir, state, issues)

	if repaired != 1 {
		t.Fatalf("expected 1 repair, got %d", repaired)
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if _, exists := state.PagePathIndex["missing.md"]; exists {
		t.Fatal("expected missing.md to be removed from state index")
	}
}

func TestRepairDoctorIssues_UntrackedID(t *testing.T) {
	dir := t.TempDir()

	content := "---\nid: \"789\"\nversion: 1\n---\n\nContent"
	if err := os.WriteFile(filepath.Join(dir, "untracked.md"), []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	state := fs.NewSpaceState()
	state.SpaceKey = "TEST"

	issues := []DoctorIssue{
		{Kind: "untracked-id", Path: "untracked.md", Message: "not tracked"},
	}

	out := new(bytes.Buffer)
	repaired, errs := repairDoctorIssues(out, dir, state, issues)

	if repaired != 1 {
		t.Fatalf("expected 1 repair, got %d", repaired)
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
	if id, exists := state.PagePathIndex["untracked.md"]; !exists || id != "789" {
		t.Fatalf("expected untracked.md -> 789 in state index, got %v", state.PagePathIndex)
	}
}

func TestRepairDoctorIssues_NonRepairableIssue(t *testing.T) {
	dir := t.TempDir()
	state := fs.NewSpaceState()

	issues := []DoctorIssue{
		{Kind: "conflict-markers", Path: "conflict.md", Message: "has conflict markers"},
	}

	out := new(bytes.Buffer)
	repaired, errs := repairDoctorIssues(out, dir, state, issues)

	if repaired != 0 {
		t.Fatalf("expected 0 repairs for conflict-markers, got %d", repaired)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for non-repairable issue, got %v", errs)
	}
}
