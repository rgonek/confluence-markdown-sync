package cmd

import (
	"encoding/json"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"github.com/spf13/cobra"
)

const reportJSONFlagName = "report-json"

type commandRunReport struct {
	RunID string `json:"run_id"`

	Command string `json:"command"`
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`

	Timing commandRunReportTiming `json:"timing"`
	Target commandRunReportTarget `json:"target"`

	Diagnostics          []commandRunReportDiagnostic        `json:"diagnostics"`
	MutatedFiles         []string                            `json:"mutated_files"`
	MutatedPages         []commandRunReportPage              `json:"mutated_pages"`
	AttachmentOperations []commandRunReportAttachmentOp      `json:"attachment_operations"`
	FallbackModes        []string                            `json:"fallback_modes"`
	RecoveryArtifacts    []commandRunReportRecoveryArtifact  `json:"recovery_artifacts"`
	ConflictResolution   *commandRunReportConflictResolution `json:"conflict_resolution,omitempty"`
}

type commandRunReportTiming struct {
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	DurationMs int64  `json:"duration_ms"`
}

type commandRunReportTarget struct {
	Mode     string `json:"mode"`
	Value    string `json:"value"`
	SpaceKey string `json:"space_key,omitempty"`
	SpaceDir string `json:"space_dir,omitempty"`
	File     string `json:"file,omitempty"`
}

type commandRunReportDiagnostic struct {
	Path           string `json:"path,omitempty"`
	Code           string `json:"code"`
	Field          string `json:"field,omitempty"`
	Message        string `json:"message"`
	Category       string `json:"category,omitempty"`
	ActionRequired bool   `json:"action_required,omitempty"`
}

type commandRunReportPage struct {
	Operation string `json:"operation,omitempty"`
	Path      string `json:"path,omitempty"`
	PageID    string `json:"page_id,omitempty"`
	Title     string `json:"title,omitempty"`
	Version   int    `json:"version,omitempty"`
	Deleted   bool   `json:"deleted,omitempty"`
}

type commandRunReportAttachmentOp struct {
	Type         string `json:"type"`
	Path         string `json:"path,omitempty"`
	PageID       string `json:"page_id,omitempty"`
	AttachmentID string `json:"attachment_id,omitempty"`
}

type commandRunReportRecoveryArtifact struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type commandRunReportConflictResolution struct {
	Policy               string                         `json:"policy"`
	Status               string                         `json:"status"`
	MutatedFiles         []string                       `json:"mutated_files"`
	Diagnostics          []commandRunReportDiagnostic   `json:"diagnostics"`
	AttachmentOperations []commandRunReportAttachmentOp `json:"attachment_operations"`
	FallbackModes        []string                       `json:"fallback_modes"`
}

type validateCommandResult struct {
	SpaceKey    string
	SpaceDir    string
	TargetFile  string
	Diagnostics []commandRunReportDiagnostic
}

type diffCommandResult struct {
	SpaceKey     string
	SpaceDir     string
	TargetFile   string
	Diagnostics  []syncflow.PullDiagnostic
	ChangedFiles []string
}

type pushWorktreeOutcome struct {
	Result             syncflow.PushResult
	Warnings           []string
	NoChanges          bool
	ConflictResolution *commandRunReportConflictResolution
}

func addReportJSONFlag(cmd *cobra.Command) {
	cmd.Flags().Bool(reportJSONFlagName, false, "Emit a structured JSON run report")
}

func commandRequestsJSONReport(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	flag := cmd.Flags().Lookup(reportJSONFlagName)
	if flag == nil {
		return false
	}
	enabled, err := cmd.Flags().GetBool(reportJSONFlagName)
	return err == nil && enabled
}

func newCommandRunReport(runID, command string, target config.Target, startedAt time.Time) commandRunReport {
	return commandRunReport{
		RunID:   strings.TrimSpace(runID),
		Command: strings.TrimSpace(command),
		Timing: commandRunReportTiming{
			StartedAt: startedAt.UTC().Format(time.RFC3339Nano),
		},
		Target: commandRunReportTarget{
			Mode:  reportTargetMode(target),
			Value: target.Value,
		},
		Diagnostics:          []commandRunReportDiagnostic{},
		MutatedFiles:         []string{},
		MutatedPages:         []commandRunReportPage{},
		AttachmentOperations: []commandRunReportAttachmentOp{},
		FallbackModes:        []string{},
		RecoveryArtifacts:    []commandRunReportRecoveryArtifact{},
	}
}

func reportTargetMode(target config.Target) string {
	if target.IsFile() {
		return "file"
	}
	return "space"
}

func (r *commandRunReport) finalize(runErr error, finishedAt time.Time) {
	r.Success = runErr == nil
	if runErr != nil {
		r.Error = runErr.Error()
	}
	r.Timing.FinishedAt = finishedAt.UTC().Format(time.RFC3339Nano)
	r.Timing.DurationMs = finishedAt.Sub(parseReportTime(r.Timing.StartedAt)).Milliseconds()
	if r.Timing.DurationMs < 0 {
		r.Timing.DurationMs = 0
	}
	r.FallbackModes = sortedUniqueStrings(r.FallbackModes)
	r.MutatedFiles = sortedUniqueStrings(r.MutatedFiles)
	if r.ConflictResolution != nil {
		r.ConflictResolution.FallbackModes = sortedUniqueStrings(r.ConflictResolution.FallbackModes)
		r.ConflictResolution.MutatedFiles = sortedUniqueStrings(r.ConflictResolution.MutatedFiles)
	}
}

func parseReportTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func writeCommandRunReport(out io.Writer, report commandRunReport) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func reportWriter(cmd *cobra.Command, actual io.Writer) io.Writer {
	if commandRequestsJSONReport(cmd) {
		return ensureSynchronizedCmdError(cmd)
	}
	return actual
}

func reportRelativePath(spaceDir, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if filepath.IsAbs(value) && strings.TrimSpace(spaceDir) != "" {
		spaceDirForRel := filepath.Clean(spaceDir)
		valueForRel := filepath.Clean(value)
		if resolved, err := filepath.EvalSymlinks(spaceDirForRel); err == nil {
			spaceDirForRel = resolved
		}
		if resolved, err := filepath.EvalSymlinks(valueForRel); err == nil {
			valueForRel = resolved
		}
		if rel, err := filepath.Rel(spaceDirForRel, valueForRel); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.ToSlash(value)
}

func pageIDFromAttachmentPath(path string) string {
	parts := strings.Split(filepath.ToSlash(strings.TrimSpace(path)), "/")
	if len(parts) >= 2 && parts[0] == "assets" {
		return parts[1]
	}
	return ""
}

func fallbackModesFromPullDiagnostics(diags []syncflow.PullDiagnostic) []string {
	modes := make([]string, 0)
	for _, diag := range diags {
		switch strings.TrimSpace(diag.Code) {
		case "FOLDER_LOOKUP_UNAVAILABLE":
			modes = append(modes, "folder_lookup_unavailable")
		case "CONTENT_STATUS_COMPATIBILITY_MODE":
			modes = append(modes, "content_status_compatibility_mode")
		}
	}
	return sortedUniqueStrings(modes)
}

func fallbackModesFromPushDiagnostics(diags []syncflow.PushDiagnostic) []string {
	modes := make([]string, 0)
	for _, diag := range diags {
		switch strings.TrimSpace(diag.Code) {
		case "FOLDER_COMPATIBILITY_MODE":
			modes = append(modes, "folder_lookup_unavailable")
		case "CONTENT_STATUS_COMPATIBILITY_MODE":
			modes = append(modes, "content_status_compatibility_mode")
		}
	}
	return sortedUniqueStrings(modes)
}

func reportDiagnosticsFromPull(diags []syncflow.PullDiagnostic, spaceDir string) []commandRunReportDiagnostic {
	out := make([]commandRunReportDiagnostic, 0, len(diags))
	for _, diag := range diags {
		out = append(out, commandRunReportDiagnostic{
			Path:           reportRelativePath(spaceDir, diag.Path),
			Code:           strings.TrimSpace(diag.Code),
			Message:        strings.TrimSpace(diag.Message),
			Category:       strings.TrimSpace(diag.Category),
			ActionRequired: diag.ActionRequired,
		})
	}
	return out
}

func reportDiagnosticsFromPush(diags []syncflow.PushDiagnostic, spaceDir string) []commandRunReportDiagnostic {
	out := make([]commandRunReportDiagnostic, 0, len(diags))
	for _, diag := range diags {
		out = append(out, commandRunReportDiagnostic{
			Path:    reportRelativePath(spaceDir, diag.Path),
			Code:    strings.TrimSpace(diag.Code),
			Message: strings.TrimSpace(diag.Message),
		})
	}
	return out
}

func reportAttachmentOpsFromPull(result syncflow.PullResult, spaceDir string) []commandRunReportAttachmentOp {
	out := make([]commandRunReportAttachmentOp, 0, len(result.DownloadedAssets)+len(result.DeletedAssets))
	for _, path := range result.DownloadedAssets {
		rel := reportRelativePath(spaceDir, path)
		out = append(out, commandRunReportAttachmentOp{
			Type:         "download",
			Path:         rel,
			PageID:       pageIDFromAttachmentPath(rel),
			AttachmentID: strings.TrimSpace(result.State.AttachmentIndex[rel]),
		})
	}
	for _, path := range result.DeletedAssets {
		rel := reportRelativePath(spaceDir, path)
		out = append(out, commandRunReportAttachmentOp{
			Type:   "delete",
			Path:   rel,
			PageID: pageIDFromAttachmentPath(rel),
		})
	}
	return out
}

func reportAttachmentOpsFromPush(result syncflow.PushResult, spaceDir string) []commandRunReportAttachmentOp {
	out := make([]commandRunReportAttachmentOp, 0)
	for _, diag := range result.Diagnostics {
		rel := reportRelativePath(spaceDir, diag.Path)
		switch strings.TrimSpace(diag.Code) {
		case "ATTACHMENT_CREATED":
			out = append(out, commandRunReportAttachmentOp{
				Type:         "upload",
				Path:         rel,
				PageID:       pageIDFromAttachmentPath(rel),
				AttachmentID: strings.TrimSpace(result.State.AttachmentIndex[rel]),
			})
		case "ATTACHMENT_DELETED":
			out = append(out, commandRunReportAttachmentOp{
				Type:   "delete",
				Path:   rel,
				PageID: pageIDFromAttachmentPath(rel),
			})
		case "ATTACHMENT_PRESERVED":
			out = append(out, commandRunReportAttachmentOp{
				Type:         "preserve",
				Path:         rel,
				PageID:       pageIDFromAttachmentPath(rel),
				AttachmentID: strings.TrimSpace(result.State.AttachmentIndex[rel]),
			})
		}
	}
	return out
}

func sortedUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	set := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (r *commandRunReport) setRecoveryArtifactStatus(artifactType, name, status string) {
	artifactType = strings.TrimSpace(artifactType)
	name = strings.TrimSpace(name)
	status = strings.TrimSpace(status)
	if artifactType == "" || name == "" || status == "" {
		return
	}
	for i := range r.RecoveryArtifacts {
		if r.RecoveryArtifacts[i].Type == artifactType && r.RecoveryArtifacts[i].Name == name {
			r.RecoveryArtifacts[i].Status = status
			return
		}
	}
	r.RecoveryArtifacts = append(r.RecoveryArtifacts, commandRunReportRecoveryArtifact{
		Type:   artifactType,
		Name:   name,
		Status: status,
	})
}
