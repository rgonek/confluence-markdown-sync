//go:build e2e

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func requireE2EConfig(t *testing.T) e2eConfig {
	t.Helper()

	required := map[string]string{
		"CONF_E2E_DOMAIN":              strings.TrimSpace(os.Getenv("CONF_E2E_DOMAIN")),
		"CONF_E2E_EMAIL":               strings.TrimSpace(os.Getenv("CONF_E2E_EMAIL")),
		"CONF_E2E_API_TOKEN":           strings.TrimSpace(os.Getenv("CONF_E2E_API_TOKEN")),
		"CONF_E2E_PRIMARY_SPACE_KEY":   strings.TrimSpace(os.Getenv("CONF_E2E_PRIMARY_SPACE_KEY")),
		"CONF_E2E_SECONDARY_SPACE_KEY": strings.TrimSpace(os.Getenv("CONF_E2E_SECONDARY_SPACE_KEY")),
	}
	for name, value := range required {
		if value == "" {
			t.Skipf("Skipping E2E test: %s not set", name)
		}
	}

	t.Setenv("ATLASSIAN_DOMAIN", required["CONF_E2E_DOMAIN"])
	t.Setenv("ATLASSIAN_EMAIL", required["CONF_E2E_EMAIL"])
	t.Setenv("ATLASSIAN_API_TOKEN", required["CONF_E2E_API_TOKEN"])

	return e2eConfig{
		Domain:            required["CONF_E2E_DOMAIN"],
		Email:             required["CONF_E2E_EMAIL"],
		APIToken:          required["CONF_E2E_API_TOKEN"],
		PrimarySpaceKey:   required["CONF_E2E_PRIMARY_SPACE_KEY"],
		SecondarySpaceKey: required["CONF_E2E_SECONDARY_SPACE_KEY"],
	}
}

func assertBaselineDiagnosticsAllowlisted(t *testing.T, spaceKey string, got []commandRunReportDiagnostic) {
	t.Helper()

	allowlist := sandboxBaselineDiagnosticAllowlist[strings.ToUpper(strings.TrimSpace(spaceKey))]
	unexpected := make([]string, 0)
	for _, diag := range got {
		if baselineDiagnosticAllowed(diag, allowlist) {
			continue
		}
		unexpected = append(unexpected, formatE2EDiagnostic(diag))
	}

	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		t.Fatalf(
			"unexpected baseline diagnostics for %s:\n%s\n\nDocument or remove them before treating the sandbox as release-ready.\nAllowed baseline entries:\n%s",
			spaceKey,
			strings.Join(unexpected, "\n"),
			formatExpectedDiagnostics(allowlist),
		)
	}
}

func baselineDiagnosticAllowed(diag commandRunReportDiagnostic, allowlist []e2eExpectedDiagnostic) bool {
	for _, expected := range allowlist {
		if expected.matches(diag) {
			return true
		}
	}
	return false
}

func (d e2eExpectedDiagnostic) matches(diag commandRunReportDiagnostic) bool {
	if strings.TrimSpace(d.Path) != "" && strings.TrimSpace(diag.Path) != strings.TrimSpace(d.Path) {
		return false
	}
	if strings.TrimSpace(d.Code) != "" && strings.TrimSpace(diag.Code) != strings.TrimSpace(d.Code) {
		return false
	}
	if strings.TrimSpace(d.MessageContains) != "" && !strings.Contains(diag.Message, d.MessageContains) {
		return false
	}
	return true
}

func formatExpectedDiagnostics(diags []e2eExpectedDiagnostic) string {
	if len(diags) == 0 {
		return "  (none)"
	}
	lines := make([]string, 0, len(diags))
	for _, diag := range diags {
		parts := make([]string, 0, 3)
		if strings.TrimSpace(diag.Path) != "" {
			parts = append(parts, "path="+diag.Path)
		}
		if strings.TrimSpace(diag.Code) != "" {
			parts = append(parts, "code="+diag.Code)
		}
		if strings.TrimSpace(diag.MessageContains) != "" {
			parts = append(parts, "message~="+diag.MessageContains)
		}
		lines = append(lines, "  - "+strings.Join(parts, ", "))
	}
	return strings.Join(lines, "\n")
}

func formatE2EDiagnostic(diag commandRunReportDiagnostic) string {
	return fmt.Sprintf(
		"  - path=%q code=%q category=%q action_required=%t message=%q",
		diag.Path,
		diag.Code,
		diag.Category,
		diag.ActionRequired,
		diag.Message,
	)
}

func assertGitWorkspaceClean(t *testing.T, workdir string) {
	t.Helper()

	cmd := exec.Command("git", "status", "--short")
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git status --short failed: %v\n%s", err, string(out))
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("expected clean git workspace, got:\n%s", string(out))
	}
}

func assertStatusOutputClean(t *testing.T, output string) {
	t.Helper()

	if strings.Count(output, "added (0)") != 2 || strings.Count(output, "modified (0)") != 2 || strings.Count(output, "deleted (0)") != 2 {
		t.Fatalf("expected clean conf status output, got:\n%s", output)
	}
	if strings.Contains(output, "Planned path moves") {
		t.Fatalf("expected no planned path moves in clean conf status output, got:\n%s", output)
	}
	if strings.Contains(output, "Conflict ahead") {
		t.Fatalf("expected no conflict-ahead section in clean conf status output, got:\n%s", output)
	}
	if !strings.Contains(output, "Version drift: no remote-ahead tracked pages") {
		t.Fatalf("expected zero version drift in conf status output, got:\n%s", output)
	}
}

func assertStatusOutputOmitsArtifacts(t *testing.T, output string, needles ...string) {
	t.Helper()

	for _, needle := range needles {
		needle = strings.TrimSpace(needle)
		if needle == "" {
			continue
		}
		if strings.Contains(output, needle) {
			t.Fatalf("expected conf status output to omit %q, got:\n%s", needle, output)
		}
	}
}

func projectRootFromWD(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	rootDir := wd
	for {
		if _, err := os.Stat(filepath.Join(rootDir, "go.mod")); err == nil {
			return rootDir
		}
		parent := filepath.Dir(rootDir)
		if parent == rootDir {
			break
		}
		rootDir = parent
	}

	t.Fatalf("could not locate project root from %s", wd)
	return ""
}

func confBinaryForOS(rootDir string) string {
	buildConfBinaryForE2E(rootDir)
	return confBinaryPath(rootDir)
}

func confBinaryPath(rootDir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(rootDir, "conf.exe")
	}
	return filepath.Join(rootDir, "conf")
}

func buildConfBinaryForE2E(rootDir string) {
	e2eBinaryBuildOnce.Do(func() {
		cmd := exec.Command("go", "build", "-o", confBinaryPath(rootDir), "./cmd/conf")
		cmd.Dir = rootDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			e2eBinaryBuildErr = fmt.Errorf("build conf binary: %w\n%s", err, string(out))
		}
	})
	if e2eBinaryBuildErr != nil {
		panic(e2eBinaryBuildErr)
	}
}

func findPulledSpaceDir(t *testing.T, workspaceRoot string) string {
	t.Helper()

	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", workspaceRoot, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(workspaceRoot, entry.Name())
		if _, err := os.Stat(filepath.Join(candidate, fs.StateFileName)); err == nil {
			return candidate
		}
	}

	t.Fatalf("could not find pulled space directory under %s", workspaceRoot)
	return ""
}

func findPulledSpaceDirBySpaceKey(t *testing.T, workspaceRoot, spaceKey string) string {
	t.Helper()

	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", workspaceRoot, err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(workspaceRoot, entry.Name())
		state, err := fs.LoadState(candidate)
		if err != nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(state.SpaceKey), strings.TrimSpace(spaceKey)) {
			return candidate
		}
	}

	t.Fatalf("could not find pulled space directory for %s under %s", spaceKey, workspaceRoot)
	return ""
}

func findMarkdownByPageID(t *testing.T, spaceDir, pageID string) string {
	t.Helper()

	var matched string
	err := filepath.WalkDir(spaceDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == "assets" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}

		doc, err := fs.ReadMarkdownDocument(path)
		if err != nil {
			return err
		}
		if strings.TrimSpace(doc.Frontmatter.ID) == pageID {
			matched = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		t.Fatalf("find markdown by page id: %v", err)
	}
	if strings.TrimSpace(matched) == "" {
		t.Fatalf("could not find markdown file for page ID %s in %s", pageID, spaceDir)
	}
	return matched
}

func findFirstMarkdownFile(t *testing.T, spaceDir string) string {
	t.Helper()

	var matched string
	err := filepath.WalkDir(spaceDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if d.Name() == "assets" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".md" {
			matched = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		t.Fatalf("find first markdown: %v", err)
	}
	if strings.TrimSpace(matched) == "" {
		t.Fatalf("no markdown files found in %s", spaceDir)
	}
	return matched
}

func prepareE2EConflictTarget(t *testing.T, spaceDir string, client *confluence.Client, runCMS func(args ...string) string) (string, string) {
	t.Helper()

	parentDir := filepath.Dir(findFirstMarkdownFile(t, spaceDir))
	return createE2EScratchPageAtDirWithBody(t, spaceDir, parentDir, client, runCMS, "Conflict E2E", "Scratch page for Confluence E2E coverage.\n")
}

func createE2EScratchPage(t *testing.T, spaceDir string, client *confluence.Client, runCMS func(args ...string) string, titlePrefix string) (string, string) {
	return createE2EScratchPageWithBody(t, spaceDir, client, runCMS, titlePrefix, "Scratch page for Confluence E2E coverage.\n")
}

func createE2EScratchPageAtDirWithBody(t *testing.T, spaceDir, targetDir string, client *confluence.Client, runCMS func(args ...string) string, titlePrefix, body string) (string, string) {
	t.Helper()

	stamp := time.Now().UTC().Format("20060102T150405") + fmt.Sprintf("-%09d", time.Now().UTC().Nanosecond())
	title := fmt.Sprintf("%s %s", titlePrefix, stamp)
	filePath := filepath.Join(targetDir, sanitizeE2EFileStem(title)+".md")
	if err := fs.WriteMarkdownDocument(filePath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: title},
		Body:        body,
	}); err != nil {
		t.Fatalf("WriteMarkdown scratch page: %v", err)
	}

	runCMS("push", filePath, "--on-conflict=cancel", "--yes", "--non-interactive")

	doc, err := fs.ReadMarkdownDocument(filePath)
	if err != nil {
		t.Fatalf("ReadMarkdown scratch page: %v", err)
	}
	pageID := strings.TrimSpace(doc.Frontmatter.ID)
	if pageID == "" {
		t.Fatal("expected scratch page push to assign a page id")
	}

	t.Cleanup(func() {
		if err := client.DeletePage(context.Background(), pageID, confluence.PageDeleteOptions{}); err != nil && err != confluence.ErrNotFound {
			t.Logf("cleanup delete scratch page %s: %v", pageID, err)
		}
	})

	return filePath, pageID
}

func createE2EScratchPageWithBody(t *testing.T, spaceDir string, client *confluence.Client, runCMS func(args ...string) string, titlePrefix, body string) (string, string) {
	t.Helper()

	return createE2EScratchPageAtDirWithBody(t, spaceDir, spaceDir, client, runCMS, titlePrefix, body)
}

func runConfJSONReport(t *testing.T, confBin, workdir string, args ...string) commandRunReport {
	t.Helper()

	cmd := exec.Command(confBin, args...)
	cmd.Dir = workdir
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("Command conf %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
	}

	var report commandRunReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("parse JSON report for conf %v: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout.String(), stderr.String())
	}
	return report
}

func contentStateNamesInclude(spaceStates []confluence.ContentState, expected ...string) bool {
	needles := map[string]struct{}{}
	for _, value := range expected {
		needles[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	for _, state := range spaceStates {
		delete(needles, strings.ToLower(strings.TrimSpace(state.Name)))
	}
	return len(needles) == 0
}

func selectContentStatusSequence(spaceStates []confluence.ContentState) (string, string, bool) {
	preferredInitial := []string{"ready for review", "ready to review", "verified", "rough draft"}
	preferredUpdate := []string{"in progress", "verified", "ready for review", "ready to review"}

	stateNames := make([]string, 0, len(spaceStates))
	stateSet := map[string]string{}
	for _, state := range spaceStates {
		name := strings.TrimSpace(state.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, exists := stateSet[key]; exists {
			continue
		}
		stateSet[key] = name
		stateNames = append(stateNames, name)
	}

	pick := func(candidates []string, exclude string) string {
		excludeKey := strings.ToLower(strings.TrimSpace(exclude))
		for _, candidate := range candidates {
			if actual, ok := stateSet[candidate]; ok && candidate != excludeKey {
				return actual
			}
		}
		for _, actual := range stateNames {
			if strings.ToLower(strings.TrimSpace(actual)) != excludeKey {
				return actual
			}
		}
		return ""
	}

	initial := pick(preferredInitial, "")
	if initial == "" {
		return "", "", false
	}
	update := pick(preferredUpdate, initial)
	if update == "" || strings.EqualFold(initial, update) {
		return "", "", false
	}
	return initial, update, true
}

var (
	e2eBinaryBuildOnce sync.Once
	e2eBinaryBuildErr  error
)

func findReportDiagnostic(diags []commandRunReportDiagnostic, code string) *commandRunReportDiagnostic {
	for i := range diags {
		if strings.TrimSpace(diags[i].Code) == strings.TrimSpace(code) {
			return &diags[i]
		}
	}
	return nil
}

func sanitizeE2EFileStem(value string) string {
	replacer := strings.NewReplacer(
		" ", "-",
		"/", "-",
		"\\", "-",
		":", "-",
		".", "-",
	)
	return replacer.Replace(value)
}

func relativeMarkdownPath(t *testing.T, sourceDir, targetPath string) string {
	t.Helper()

	relPath, err := filepath.Rel(sourceDir, targetPath)
	if err != nil {
		t.Fatalf("filepath.Rel(%s, %s): %v", sourceDir, targetPath, err)
	}
	return filepath.ToSlash(relPath)
}

func encodeMarkdownRelPath(path string) string {
	return strings.ReplaceAll(path, " ", "%20")
}

func waitForPageContentStatus(t *testing.T, ctx context.Context, client *confluence.Client, pageID, pageStatus, expected string) {
	t.Helper()

	deadline := time.Now().Add(45 * time.Second)
	for {
		status, err := client.GetContentStatus(ctx, pageID, pageStatus)
		if err == nil && strings.TrimSpace(status) == strings.TrimSpace(expected) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("page %s content status did not reach %q before timeout; last status=%q err=%v", pageID, expected, status, err)
		}
		time.Sleep(2 * time.Second)
	}
}

func waitForPageADF(t *testing.T, ctx context.Context, client *confluence.Client, pageID string, predicate func(adf []byte) bool) confluence.Page {
	t.Helper()

	deadline := time.Now().Add(45 * time.Second)
	for {
		page, err := client.GetPage(ctx, pageID)
		if err == nil && predicate(page.BodyADF) {
			return page
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("GetPage(%s) did not satisfy predicate before timeout: %v", pageID, err)
			}
			t.Fatalf("remote ADF for page %s did not satisfy predicate before timeout: %s", pageID, string(page.BodyADF))
		}
		time.Sleep(2 * time.Second)
	}
}

func waitForPageAttachments(t *testing.T, ctx context.Context, client *confluence.Client, pageID string, predicate func([]confluence.Attachment) bool) []confluence.Attachment {
	t.Helper()

	deadline := time.Now().Add(45 * time.Second)
	for {
		attachments, err := client.ListAttachments(ctx, pageID)
		if err == nil && predicate(attachments) {
			return attachments
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("ListAttachments(%s) did not satisfy predicate before timeout: %v", pageID, err)
			}
			t.Fatalf("attachments for page %s did not satisfy predicate before timeout: %+v", pageID, attachments)
		}
		time.Sleep(2 * time.Second)
	}
}

func waitForArchivedPageInSpace(t *testing.T, ctx context.Context, client *confluence.Client, spaceKey, pageID string) confluence.Page {
	t.Helper()

	deadline := time.Now().Add(45 * time.Second)
	for {
		cursor := ""
		for {
			result, err := client.ListPages(ctx, confluence.PageListOptions{
				SpaceKey: spaceKey,
				Status:   "archived",
				Limit:    100,
				Cursor:   cursor,
			})
			if err != nil {
				break
			}
			for _, page := range result.Pages {
				if strings.TrimSpace(page.ID) == strings.TrimSpace(pageID) {
					return page
				}
			}
			if strings.TrimSpace(result.NextCursor) == "" || result.NextCursor == cursor {
				break
			}
			cursor = result.NextCursor
		}

		if time.Now().After(deadline) {
			t.Fatalf("page %s did not appear in archived state before timeout", pageID)
		}
		time.Sleep(2 * time.Second)
	}
}

func collectADFMediaIdentitySets(adf []byte) (map[string]struct{}, map[string]struct{}) {
	attachmentIDs := map[string]struct{}{}
	renderIDs := map[string]struct{}{}

	var root any
	if err := json.Unmarshal(adf, &root); err != nil {
		return attachmentIDs, renderIDs
	}

	walkADFMediaNodes(root, func(attrs map[string]any) {
		if attachmentID, ok := attrs["attachmentId"].(string); ok && strings.TrimSpace(attachmentID) != "" {
			attachmentIDs[strings.TrimSpace(attachmentID)] = struct{}{}
		}
		if renderID, ok := attrs["id"].(string); ok && strings.TrimSpace(renderID) != "" {
			renderIDs[strings.TrimSpace(renderID)] = struct{}{}
		}
	})

	return attachmentIDs, renderIDs
}

func walkADFMediaNodes(node any, visit func(attrs map[string]any)) {
	switch typed := node.(type) {
	case map[string]any:
		if nodeType, ok := typed["type"].(string); ok && (nodeType == "media" || nodeType == "mediaInline") {
			if attrs, ok := typed["attrs"].(map[string]any); ok {
				visit(attrs)
			}
		}
		for _, value := range typed {
			walkADFMediaNodes(value, visit)
		}
	case []any:
		for _, value := range typed {
			walkADFMediaNodes(value, visit)
		}
	}
}

func adfContainsCodeBlockLanguage(adf []byte, language string) bool {
	var root any
	if err := json.Unmarshal(adf, &root); err != nil {
		return false
	}
	return walkADFForCodeBlockLanguage(root, language)
}

func walkADFForCodeBlockLanguage(node any, language string) bool {
	switch typed := node.(type) {
	case map[string]any:
		if nodeType, ok := typed["type"].(string); ok && nodeType == "codeBlock" {
			if attrs, ok := typed["attrs"].(map[string]any); ok {
				if lang, ok := attrs["language"].(string); ok && strings.EqualFold(strings.TrimSpace(lang), language) {
					return true
				}
			}
		}
		for _, value := range typed {
			if walkADFForCodeBlockLanguage(value, language) {
				return true
			}
		}
	case []any:
		for _, value := range typed {
			if walkADFForCodeBlockLanguage(value, language) {
				return true
			}
		}
	}
	return false
}

func backupFileContains(t *testing.T, rootDir, nameFragment, needle string) bool {
	t.Helper()

	found := false
	_ = filepath.WalkDir(rootDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() || !strings.Contains(d.Name(), nameFragment) {
			return walkErr
		}
		rawBackup, err := os.ReadFile(path) //nolint:gosec // test scans its own temp workspace
		if err == nil && strings.Contains(string(rawBackup), needle) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func mustExtractMarkdownDestination(t *testing.T, body, kind string) string {
	t.Helper()

	start, end, ok := findMarkdownLinkToken(body, kind)
	if !ok {
		t.Fatalf("could not extract %s markdown destination from body:\n%s", kind, body)
		return ""
	}
	token := body[start:end]
	openParen := strings.Index(token, "(")
	closeParen := strings.LastIndex(token, ")")
	if openParen < 0 || closeParen <= openParen {
		t.Fatalf("could not extract %s markdown destination from body:\n%s", kind, body)
		return ""
	}
	return strings.TrimSpace(token[openParen+1 : closeParen])
}

func mustExtractMarkdownLinkToken(t *testing.T, body, kind string) string {
	t.Helper()

	start, end, ok := findMarkdownLinkToken(body, kind)
	if !ok {
		t.Fatalf("could not extract %s markdown link token from body:\n%s", kind, body)
		return ""
	}
	return body[start:end]
}

func findMarkdownLinkToken(body, kind string) (int, int, bool) {
	for i := 0; i < len(body); i++ {
		if kind == "image" {
			if !strings.HasPrefix(body[i:], "![") {
				continue
			}
		} else {
			if body[i] != '[' || (i > 0 && body[i-1] == '!') {
				continue
			}
		}

		closeBracket := strings.Index(body[i:], "](")
		if closeBracket < 0 {
			continue
		}
		destStart := i + closeBracket + 2
		destEnd := strings.Index(body[destStart:], ")")
		if destEnd < 0 {
			continue
		}
		return i, destStart + destEnd + 1, true
	}
	return 0, 0, false
}

func waitForPlainISODateOrSkip(t *testing.T, ctx context.Context, client *confluence.Client, pageID, expectedText string) confluence.Page {
	t.Helper()

	deadline := time.Now().Add(45 * time.Second)
	var lastPage confluence.Page
	for {
		page, err := client.GetPage(ctx, pageID)
		if err == nil {
			lastPage = page
			adfStr := string(page.BodyADF)
			normalizedADF := normalizeE2EPlainDateText(adfStr)
			if !strings.Contains(adfStr, "\"type\":\"date\"") && strings.Contains(normalizedADF, "\"text\":\""+expectedText+"\"") {
				return page
			}
			if time.Now().After(deadline) && strings.Contains(adfStr, "\"type\":\"date\"") {
				t.Skipf("tenant coerces plain ISO date text into date nodes for page %s: %s", pageID, adfStr)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote ADF for page %s did not preserve plain ISO text before timeout: %s", pageID, string(lastPage.BodyADF))
		}
		time.Sleep(2 * time.Second)
	}
}

func normalizeE2EPlainDateText(value string) string {
	value = strings.ReplaceAll(value, "\u2060", "")
	value = strings.ReplaceAll(value, "\u2011", "-")
	return value
}

func waitForAttachmentPublicationOrSkip(t *testing.T, ctx context.Context, client *confluence.Client, pageID string, attachments []confluence.Attachment) confluence.Page {
	t.Helper()

	deadline := time.Now().Add(45 * time.Second)
	var lastPage confluence.Page
	for {
		page, err := client.GetPage(ctx, pageID)
		if err == nil {
			lastPage = page
			adfStr := string(page.BodyADF)
			if !strings.Contains(adfStr, "UNKNOWN_MEDIA_ID") && !strings.Contains(adfStr, "Invalid file id -") {
				_, renderIDs := collectADFMediaIdentitySets(page.BodyADF)
				allPresent := true
				for _, attachment := range attachments {
					if _, ok := renderIDs[strings.TrimSpace(attachment.FileID)]; !ok {
						allPresent = false
						break
					}
				}
				if allPresent {
					return page
				}
			}
			if time.Now().After(deadline) && strings.Contains(adfStr, "Invalid file id -") {
				t.Skipf("tenant rejects uploaded attachment ids as renderable media ids for page %s: %s", pageID, adfStr)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote ADF for page %s did not publish attachment media ids before timeout: %s", pageID, string(lastPage.BodyADF))
		}
		time.Sleep(2 * time.Second)
	}
}

func attachmentsExposeRenderableMediaIDs(attachments []confluence.Attachment) bool {
	if len(attachments) == 0 {
		return false
	}
	for _, attachment := range attachments {
		fileID := strings.TrimSpace(attachment.FileID)
		if fileID == "" {
			return false
		}
		if strings.HasPrefix(strings.ToLower(fileID), "att") {
			return false
		}
	}
	return true
}
