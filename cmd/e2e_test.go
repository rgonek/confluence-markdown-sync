//go:build e2e

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

type e2eConfig struct {
	Domain            string
	Email             string
	APIToken          string
	PrimarySpaceKey   string
	SecondarySpaceKey string
}

type e2eExpectedDiagnostic struct {
	Path            string
	Code            string
	MessageContains string
}

var sandboxBaselineDiagnosticAllowlist = map[string][]e2eExpectedDiagnostic{
	"TD2": {
		{
			Path: "17727489",
			Code: "UNKNOWN_MEDIA_ID_UNRESOLVED",
		},
		{
			Path:            "Technical-Documentation/Live-Workflow-Test-2026-03-05/Endpoint-Notes.md",
			Code:            "unresolved_reference",
			MessageContains: "pageId=17727489#Task-list",
		},
		{
			Path:            "Technical-Documentation/Live-Workflow-Test-2026-03-05/Live-Workflow-Test-2026-03-05.md",
			Code:            "unresolved_reference",
			MessageContains: "pageId=17727489",
		},
		{
			Path:            "Technical-Documentation/Live-Workflow-Test-2026-03-05/Live-Workflow-Test-2026-03-05.md",
			Code:            "unresolved_reference",
			MessageContains: "pageId=17530900#Task-list",
		},
		{
			Path:            "Technical-Documentation/Live-Workflow-Test-2026-03-05/Checklist-and-Diagrams.md",
			MessageContains: "UNKNOWN_MEDIA_ID",
		},
	},
	"SD2": {
		{
			Path:            "Software-Development/Release-Sandbox-2026-03-05.md",
			Code:            "CROSS_SPACE_LINK_PRESERVED",
			MessageContains: "pageId=17334539",
		},
	},
}

func TestWorkflow_ConflictResolution(t *testing.T) {
	e2eCfg := requireE2EConfig(t)
	spaceKey := e2eCfg.PrimarySpaceKey

	ctx := context.Background()
	cfg, err := config.Load("") // Load from env
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	client, err := confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// 0. Build conf
	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	// 1. Setup temp workspace
	tmpDir, err := os.MkdirTemp("", "conf-e2e-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCmd := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command %s %v failed: %v\n%s", name, args, err, string(out))
		}
		return string(out)
	}

	runCMS := func(args ...string) string {
		return runCmd(confBin, args...)
	}

	// init
	runCMS("init")

	// pull
	runCMS("pull", spaceKey, "--yes")

	spaceDir := findPulledSpaceDir(t, tmpDir)
	simplePath, pageID := prepareE2EConflictTarget(t, spaceDir, client, runCMS)

	// 2. Modify local
	doc, err := fs.ReadMarkdownDocument(simplePath)
	if err != nil {
		t.Fatalf("ReadMarkdown: %v", err)
	}
	doc.Body += "\n\nLocal change for E2E test"
	if err := fs.WriteMarkdownDocument(simplePath, doc); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}

	// 3. Simulate remote conflict
	remotePage, err := client.GetPage(ctx, pageID)
	if err != nil {
		t.Fatalf("GetPage: %v", err)
	}

	updatedRemote, err := client.UpdatePage(ctx, pageID, confluence.PageUpsertInput{
		SpaceID:      remotePage.SpaceID,
		ParentPageID: remotePage.ParentPageID,
		Title:        remotePage.Title,
		Version:      remotePage.Version + 1,
		BodyADF:      []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Remote update for E2E conflict test"}]}]}`),
	})
	if err != nil {
		t.Fatalf("Remote update (conflict simulation) failed: %v", err)
	}
	fmt.Printf("Remote version after simulation: %d\n", updatedRemote.Version)

	// Wait for eventual consistency
	time.Sleep(5 * time.Second)

	// 4. Try push --on-conflict=cancel -> should fail
	cmd := exec.Command(confBin, "push", simplePath, "--on-conflict=cancel", "--yes", "--non-interactive")
	cmd.Dir = tmpDir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("Push should have failed due to conflict, but succeeded\n%s", string(out))
	}
	if !strings.Contains(string(out), "conflict") {
		t.Fatalf("Push failed but error doesn't mention conflict: %s", string(out))
	}
	fmt.Printf("Push failed as expected (Conflict detected)\n")

	// 5. Run pull to resolve conflict (this will fail with merge conflict in file)
	cmd = exec.Command(confBin, "pull", simplePath, "--yes", "--non-interactive")
	cmd.Dir = tmpDir
	out, _ = cmd.CombinedOutput()
	if !strings.Contains(string(out), "conflict") {
		t.Fatalf("Pull should have reported conflict: %s", string(out))
	}
	fmt.Printf("Pull reported conflict as expected\n")
	simplePath = findMarkdownByPageID(t, spaceDir, pageID)

	// 6. Force resolve conflict by rewriting the file with new content and correct version
	remotePageAfterUpdate, err := client.GetPage(ctx, pageID)
	if err != nil {
		t.Fatalf("GetPage after update: %v", err)
	}

	doc.Frontmatter.Version = remotePageAfterUpdate.Version
	doc.Body = "Resolved conflict body\n" + doc.Body
	if err := fs.WriteMarkdownDocument(simplePath, doc); err != nil {
		t.Fatalf("WriteMarkdown (resolved): %v", err)
	}

	// Mark as resolved in git
	relSimplePath, err := filepath.Rel(tmpDir, simplePath)
	if err != nil {
		t.Fatalf("rel path: %v", err)
	}
	runCmd("git", "add", filepath.ToSlash(relSimplePath))

	// 7. Push again -> should succeed
	runCMS("push", simplePath, "--on-conflict=cancel", "--yes", "--non-interactive")
	fmt.Printf("Push after manual resolution succeeded\n")
}

func TestWorkflow_PushAutoPullMerge(t *testing.T) {
	e2eCfg := requireE2EConfig(t)
	spaceKey := e2eCfg.PrimarySpaceKey

	ctx := context.Background()
	cfg, err := config.Load("")
	client, _ := confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})

	// 0. Setup
	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-autopull-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(confBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command conf %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	// 1. Init & Pull
	runCMS("init")
	runCMS("pull", spaceKey, "--yes")

	// 2. Modify local
	spaceDir := findPulledSpaceDir(t, tmpDir)
	simplePath, pageID := prepareE2EConflictTarget(t, spaceDir, client, runCMS)
	doc, _ := fs.ReadMarkdownDocument(simplePath)
	doc.Body += "\n\nLocal change for auto pull-merge test"
	_ = fs.WriteMarkdownDocument(simplePath, doc)

	// 3. Simulate remote update (bump version)
	remotePage, _ := client.GetPage(ctx, pageID)
	client.UpdatePage(ctx, pageID, confluence.PageUpsertInput{
		SpaceID:      remotePage.SpaceID,
		ParentPageID: remotePage.ParentPageID,
		Title:        remotePage.Title,
		Version:      remotePage.Version + 1,
		BodyADF:      []byte(`{"version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Remote update for auto pull-merge"}]}]}`),
	})
	time.Sleep(5 * time.Second)

	// 4. Run push with --on-conflict=pull-merge
	// This should trigger the automatic pull
	pcmd := exec.Command(confBin, "push", simplePath, "--on-conflict=pull-merge", "--yes", "--non-interactive")
	pcmd.Dir = tmpDir
	out, err := pcmd.CombinedOutput()
	// It might fail with a content conflict error (which is what happens in this test setup)
	// but it should at least mention "automatic pull-merge"
	if err != nil && !strings.Contains(string(out), "automatic pull-merge") {
		t.Fatalf("Push failed with unexpected error: %v\n%s", err, string(out))
	}
	if !strings.Contains(string(out), "automatic pull-merge") {
		t.Fatalf("Push output should mention automatic pull-merge: %s", string(out))
	}
	fmt.Printf("Push automatically triggered pull-merge (Error handled if content conflict)\n")

	// 5. Verify local state
	simplePath = findMarkdownByPageID(t, spaceDir, pageID)
	doc, _ = fs.ReadMarkdownDocument(simplePath)
	// After pull (even with conflict), the frontmatter version should be updated
	if doc.Frontmatter.Version != remotePage.Version+1 {
		t.Fatalf("Local version should be updated after auto pull-merge: got %d, want %d", doc.Frontmatter.Version, remotePage.Version+1)
	}
	raw, readErr := os.ReadFile(simplePath)
	if readErr != nil {
		t.Fatalf("ReadFile after auto pull-merge: %v", readErr)
	}
	bodyText := string(raw)
	if !strings.Contains(bodyText, "Local change for auto pull-merge test") && !strings.Contains(bodyText, "<<<<<<<") {
		backupFound := backupFileContains(t, spaceDir, "My Local Changes", "Local change for auto pull-merge test")
		if !backupFound {
			t.Fatalf("Local edit should survive auto pull-merge via merged content, conflict markers, or side-by-side backup, got:\n%s\n\npush output:\n%s", bodyText, string(out))
		}
	}
}

func TestWorkflow_AgenticFullCycle(t *testing.T) {
	e2eCfg := requireE2EConfig(t)
	spaceKey := e2eCfg.PrimarySpaceKey

	// 0. Setup
	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-agentic-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(confBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command conf %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	// 1. Init & Pull
	runCMS("init")
	runCMS("pull", spaceKey, "--yes", "--non-interactive")

	// 2. Edit
	spaceDir := findPulledSpaceDir(t, tmpDir)
	simplePath := findFirstMarkdownFile(t, spaceDir)
	doc, err := fs.ReadMarkdownDocument(simplePath)
	if err != nil {
		t.Fatalf("ReadMarkdown: %v", err)
	}
	doc.Body += "\n\nAgentic update " + time.Now().Format(time.RFC3339)
	if err := fs.WriteMarkdownDocument(simplePath, doc); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}

	// 3. Validate
	runCMS("validate", simplePath)

	// 4. Diff
	runCMS("diff", simplePath)

	// 5. Push
	runCMS("push", simplePath, "--on-conflict=cancel", "--yes", "--non-interactive")

	fmt.Printf("Agentic full cycle succeeded\n")
}

func TestWorkflow_SandboxBaselineDiagnosticsAllowlist(t *testing.T) {
	e2eCfg := requireE2EConfig(t)

	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-baseline-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(confBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command conf %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	runCMS("init")

	for _, spaceKey := range []string{e2eCfg.PrimarySpaceKey, e2eCfg.SecondarySpaceKey} {
		report := runConfJSONReport(t, confBin, tmpDir, "pull", spaceKey, "--yes", "--non-interactive", "--skip-missing-assets", "--force", "--report-json")
		if !report.Success {
			t.Fatalf("baseline pull report for %s should be successful: %+v", spaceKey, report)
		}
		assertBaselineDiagnosticsAllowlisted(t, spaceKey, report.Diagnostics)
	}
}

func TestWorkflow_CrossSpaceLinkPreservation(t *testing.T) {
	e2eCfg := requireE2EConfig(t)
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	client, err := confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-cross-space-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(confBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command conf %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	runCMS("init")
	runCMS("pull", e2eCfg.PrimarySpaceKey, "--yes", "--non-interactive")
	runCMS("pull", e2eCfg.SecondarySpaceKey, "--yes", "--non-interactive")

	primaryDir := findPulledSpaceDirBySpaceKey(t, tmpDir, e2eCfg.PrimarySpaceKey)
	secondaryDir := findPulledSpaceDirBySpaceKey(t, tmpDir, e2eCfg.SecondarySpaceKey)

	targetPath, targetPageID := createE2EScratchPageWithBody(t, secondaryDir, client, runCMS, "Cross Space Target", "## Section A\n\nRemote cross-space target.\n")
	sourceTitle := "Cross Space Source"
	sourceBody := "[Cross Space](" + encodeMarkdownRelPath(relativeMarkdownPath(t, primaryDir, targetPath)) + "#section-a)\n"
	_, sourcePageID := createE2EScratchPageWithBody(t, primaryDir, client, runCMS, sourceTitle, sourceBody)

	report := runConfJSONReport(t, confBin, tmpDir, "pull", e2eCfg.PrimarySpaceKey, "--force", "--yes", "--non-interactive", "--report-json")
	if !report.Success {
		t.Fatalf("pull report should be successful: %+v", report)
	}

	sourcePath := findMarkdownByPageID(t, primaryDir, sourcePageID)
	doc, err := fs.ReadMarkdownDocument(sourcePath)
	if err != nil {
		t.Fatalf("ReadMarkdown: %v", err)
	}

	expectedHref := cfg.Domain + "/wiki/pages/viewpage.action?pageId=" + targetPageID + "#section-a"
	if !strings.Contains(doc.Body, "[Cross Space]("+expectedHref+")") {
		t.Fatalf("expected preserved cross-space href %q, body=\n%s", expectedHref, doc.Body)
	}

	diag := findReportDiagnostic(report.Diagnostics, "CROSS_SPACE_LINK_PRESERVED")
	if diag == nil {
		t.Fatalf("expected CROSS_SPACE_LINK_PRESERVED diagnostic, got %+v", report.Diagnostics)
	}
	if diag.Category != "preserved_external_link" {
		t.Fatalf("diagnostic category = %q, want preserved_external_link", diag.Category)
	}
	if diag.ActionRequired {
		t.Fatalf("diagnostic should not require action: %+v", diag)
	}
}

func TestWorkflow_TaskListRoundTrip(t *testing.T) {
	e2eCfg := requireE2EConfig(t)
	spaceKey := e2eCfg.PrimarySpaceKey

	ctx := context.Background()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	client, err := confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-tasklist-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(confBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command conf %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	runCMS("init")
	runCMS("pull", spaceKey, "--yes", "--non-interactive")

	spaceDir := findPulledSpaceDirBySpaceKey(t, tmpDir, spaceKey)
	body := "- [ ] Open migration checklist\n- [x] Verify round-trip coverage\n"
	filePath, pageID := createE2EScratchPageWithBody(t, spaceDir, client, runCMS, "Task List E2E", body)

	waitForPageADF(t, ctx, client, pageID, func(adf []byte) bool {
		adfStr := string(adf)
		return strings.Contains(adfStr, "\"type\":\"taskList\"") &&
			strings.Contains(adfStr, "\"type\":\"taskItem\"") &&
			strings.Contains(adfStr, "\"state\":\"TODO\"") &&
			strings.Contains(adfStr, "\"state\":\"DONE\"")
	})

	runCMS("pull", filePath, "--yes", "--non-interactive")

	filePath = findMarkdownByPageID(t, spaceDir, pageID)
	doc, err := fs.ReadMarkdownDocument(filePath)
	if err != nil {
		t.Fatalf("ReadMarkdown: %v", err)
	}
	if !strings.Contains(doc.Body, "- [ ] Open migration checklist") {
		t.Fatalf("expected unchecked task after round-trip, body=\n%s", doc.Body)
	}
	if !strings.Contains(doc.Body, "- [x] Verify round-trip coverage") {
		t.Fatalf("expected checked task after round-trip, body=\n%s", doc.Body)
	}
}

func TestWorkflow_PlantUMLRoundTrip(t *testing.T) {
	e2eCfg := requireE2EConfig(t)
	spaceKey := e2eCfg.PrimarySpaceKey

	ctx := context.Background()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	client, err := confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-plantuml-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(confBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command conf %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	runCMS("init")
	runCMS("pull", spaceKey, "--yes", "--non-interactive")

	spaceDir := findPulledSpaceDirBySpaceKey(t, tmpDir, spaceKey)
	body := "::: { .adf-extension key=\"plantumlcloud\" filename=\"architecture.puml\" }\n```puml\n@startuml\nA -> B: Hello\n@enduml\n```\n:::\n"
	filePath, pageID := createE2EScratchPageWithBody(t, spaceDir, client, runCMS, "PlantUML E2E", body)

	waitForPageADF(t, ctx, client, pageID, func(adf []byte) bool {
		adfStr := string(adf)
		return strings.Contains(adfStr, "\"extensionKey\":\"plantumlcloud\"") &&
			strings.Contains(adfStr, "\"filename\":{\"value\":\"architecture.puml\"")
	})

	runCMS("pull", filePath, "--yes", "--non-interactive")

	filePath = findMarkdownByPageID(t, spaceDir, pageID)
	doc, err := fs.ReadMarkdownDocument(filePath)
	if err != nil {
		t.Fatalf("ReadMarkdown: %v", err)
	}
	if !strings.Contains(doc.Body, "key=\"plantumlcloud\"") {
		t.Fatalf("expected plantuml extension wrapper after round-trip, body=\n%s", doc.Body)
	}
	if !strings.Contains(doc.Body, "```puml") || !strings.Contains(doc.Body, "A -> B: Hello") {
		t.Fatalf("expected plantuml code block after round-trip, body=\n%s", doc.Body)
	}
}

func TestWorkflow_MermaidWarningAndRoundTrip(t *testing.T) {
	e2eCfg := requireE2EConfig(t)
	spaceKey := e2eCfg.PrimarySpaceKey

	ctx := context.Background()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	client, err := confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-mermaid-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(confBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command conf %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	runCMS("init")
	runCMS("pull", spaceKey, "--yes", "--non-interactive")

	spaceDir := findPulledSpaceDirBySpaceKey(t, tmpDir, spaceKey)
	stamp := time.Now().UTC().Format("20060102T150405")
	title := "Mermaid E2E " + stamp
	filePath := filepath.Join(spaceDir, "Mermaid-E2E-"+stamp+".md")
	if err := fs.WriteMarkdownDocument(filePath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: title},
		Body:        "```mermaid\ngraph TD\n  A --> B\n```\n",
	}); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}

	validateOut := runCMS("validate", filePath)
	if !strings.Contains(validateOut, "MERMAID_PRESERVED_AS_CODEBLOCK") {
		t.Fatalf("expected Mermaid validate warning, got:\n%s", validateOut)
	}
	runCMS("push", filePath, "--on-conflict=cancel", "--yes", "--non-interactive")

	doc, err := fs.ReadMarkdownDocument(filePath)
	if err != nil {
		t.Fatalf("ReadMarkdown: %v", err)
	}
	pageID := strings.TrimSpace(doc.Frontmatter.ID)
	if pageID == "" {
		t.Fatal("expected pushed Mermaid page to receive a page id")
	}
	t.Cleanup(func() {
		if err := client.DeletePage(context.Background(), pageID, confluence.PageDeleteOptions{}); err != nil && err != confluence.ErrNotFound {
			t.Logf("cleanup delete page %s: %v", pageID, err)
		}
	})

	deadline := time.Now().Add(30 * time.Second)
	mermaidPublished := false
	for {
		page, err := client.GetPage(ctx, pageID)
		if err == nil && adfContainsCodeBlockLanguage(page.BodyADF, "mermaid") {
			mermaidPublished = true
			break
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("GetPage(%s) did not expose Mermaid codeBlock before timeout: %v", pageID, err)
			}
			t.Fatalf("remote ADF for page %s did not contain Mermaid codeBlock before timeout: %s", pageID, string(page.BodyADF))
		}
		time.Sleep(2 * time.Second)
	}
	if !mermaidPublished {
		t.Fatalf("remote ADF for page %s did not contain Mermaid codeBlock before timeout", pageID)
	}

	runCMS("pull", filePath, "--yes", "--non-interactive")

	filePath = findMarkdownByPageID(t, spaceDir, pageID)
	doc, err = fs.ReadMarkdownDocument(filePath)
	if err != nil {
		t.Fatalf("ReadMarkdown after pull: %v", err)
	}
	if !strings.Contains(doc.Body, "```mermaid") || !strings.Contains(doc.Body, "A --> B") {
		t.Fatalf("expected Mermaid fence after round-trip, body=\n%s", doc.Body)
	}
}

func TestWorkflow_ContentStatusRoundTrip(t *testing.T) {
	e2eCfg := requireE2EConfig(t)
	spaceKey := e2eCfg.PrimarySpaceKey

	ctx := context.Background()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	client, err := confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	spaceStates, err := client.ListSpaceContentStates(ctx, spaceKey)
	if err != nil {
		t.Skipf("content status API unavailable for tenant: %v", err)
	}
	initialStatus, updateStatus, ok := selectContentStatusSequence(spaceStates)
	if !ok {
		t.Skipf("space %s does not expose at least two usable content states for this E2E", spaceKey)
	}
	t.Logf("using content statuses: initial=%q update=%q", initialStatus, updateStatus)

	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-content-status-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(confBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command conf %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	runCMS("init")
	runCMS("pull", spaceKey, "--yes", "--non-interactive")

	spaceDir := findPulledSpaceDirBySpaceKey(t, tmpDir, spaceKey)
	stamp := time.Now().UTC().Format("20060102T150405")
	filePath := filepath.Join(spaceDir, "Content-Status-E2E-"+stamp+".md")
	if err := fs.WriteMarkdownDocument(filePath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:  "Content Status E2E " + stamp,
			Status: initialStatus,
		},
		Body: "content status e2e body\n",
	}); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}

	runCMS("push", filePath, "--on-conflict=cancel", "--yes", "--non-interactive")

	doc, err := fs.ReadMarkdownDocument(filePath)
	if err != nil {
		t.Fatalf("ReadMarkdown after create push: %v", err)
	}
	pageID := strings.TrimSpace(doc.Frontmatter.ID)
	if pageID == "" {
		t.Fatal("expected created page id after push")
	}
	t.Cleanup(func() {
		if err := client.DeletePage(context.Background(), pageID, confluence.PageDeleteOptions{}); err != nil && err != confluence.ErrNotFound {
			t.Logf("cleanup delete scratch page %s: %v", pageID, err)
		}
	})

	waitForPageContentStatus(t, ctx, client, pageID, "current", initialStatus)

	doc.Frontmatter.Status = updateStatus
	if err := fs.WriteMarkdownDocument(filePath, doc); err != nil {
		t.Fatalf("WriteMarkdown update status: %v", err)
	}
	runCMS("push", filePath, "--on-conflict=cancel", "--yes", "--non-interactive")
	waitForPageContentStatus(t, ctx, client, pageID, "current", updateStatus)

	doc, err = fs.ReadMarkdownDocument(filePath)
	if err != nil {
		t.Fatalf("ReadMarkdown after status update: %v", err)
	}
	doc.Frontmatter.Status = ""
	if err := fs.WriteMarkdownDocument(filePath, doc); err != nil {
		t.Fatalf("WriteMarkdown clear status: %v", err)
	}
	runCMS("push", filePath, "--on-conflict=cancel", "--yes", "--non-interactive")
	waitForPageContentStatus(t, ctx, client, pageID, "current", "")
}

func TestWorkflow_PlainISODateTextStability(t *testing.T) {
	e2eCfg := requireE2EConfig(t)
	spaceKey := e2eCfg.PrimarySpaceKey

	ctx := context.Background()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	client, err := confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-date-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(confBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command conf %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	runCMS("init")
	runCMS("pull", spaceKey, "--yes", "--non-interactive")

	spaceDir := findPulledSpaceDirBySpaceKey(t, tmpDir, spaceKey)
	filePath, pageID := createE2EScratchPageWithBody(t, spaceDir, client, runCMS, "Plain Date E2E", "Release date: 2026-03-09\n")

	waitForPlainISODateOrSkip(t, ctx, client, pageID, "Release date: 2026-03-09")

	runCMS("pull", filePath, "--yes", "--non-interactive")

	filePath = findMarkdownByPageID(t, spaceDir, pageID)
	doc, err := fs.ReadMarkdownDocument(filePath)
	if err != nil {
		t.Fatalf("ReadMarkdown after pull: %v", err)
	}
	if !strings.Contains(doc.Body, "Release date: 2026-03-09") {
		t.Fatalf("expected plain ISO date text after round-trip, body=\n%s", doc.Body)
	}
}

func TestWorkflow_AttachmentPublicationRoundTripAndDeletion(t *testing.T) {
	e2eCfg := requireE2EConfig(t)
	spaceKey := e2eCfg.PrimarySpaceKey

	ctx := context.Background()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	client, err := confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-attachments-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(confBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command conf %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	runCMS("init")
	runCMS("pull", spaceKey, "--yes", "--non-interactive")

	spaceDir := findPulledSpaceDir(t, tmpDir)
	parentDir := filepath.Dir(findFirstMarkdownFile(t, spaceDir))
	stamp := time.Now().UTC().Format("20060102T150405")
	title := "Attachment E2E " + stamp
	filePath := filepath.Join(parentDir, sanitizeE2EFileStem(title)+".md")
	imageName := "diagram-" + stamp + ".png"
	fileName := "manual-" + stamp + ".txt"
	imagePath := filepath.Join(parentDir, imageName)
	fileAssetPath := filepath.Join(parentDir, fileName)

	if err := os.WriteFile(imagePath, []byte("png-bytes"), 0o600); err != nil {
		t.Fatalf("write image asset: %v", err)
	}
	if err := os.WriteFile(fileAssetPath, []byte("manual-bytes"), 0o600); err != nil {
		t.Fatalf("write file asset: %v", err)
	}

	if err := fs.WriteMarkdownDocument(filePath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: title},
		Body:        fmt.Sprintf("![Diagram](%s)\n[Manual](%s)\n", imageName, fileName),
	}); err != nil {
		t.Fatalf("WriteMarkdown attachment page: %v", err)
	}

	runCMS("validate", filePath)
	runCMS("push", filePath, "--on-conflict=cancel", "--yes", "--non-interactive")

	doc, err := fs.ReadMarkdownDocument(filePath)
	if err != nil {
		t.Fatalf("ReadMarkdown attachment page: %v", err)
	}
	pageID := strings.TrimSpace(doc.Frontmatter.ID)
	if pageID == "" {
		t.Fatal("expected attachment page push to assign a page id")
	}
	t.Cleanup(func() {
		if err := client.DeletePage(context.Background(), pageID, confluence.PageDeleteOptions{}); err != nil && err != confluence.ErrNotFound {
			t.Logf("cleanup delete attachment page %s: %v", pageID, err)
		}
	})

	attachments := waitForPageAttachments(t, ctx, client, pageID, func(got []confluence.Attachment) bool {
		return len(got) == 2
	})
	if !attachmentsExposeRenderableMediaIDs(attachments) {
		t.Skipf("tenant does not expose usable attachment media ids for inline publish: %+v", attachments)
	}
	attachmentIDByFilename := map[string]string{}
	for _, attachment := range attachments {
		attachmentIDByFilename[strings.TrimSpace(attachment.Filename)] = strings.TrimSpace(attachment.ID)
	}

	imageAttachmentID := attachmentIDByFilename[imageName]
	fileAttachmentID := attachmentIDByFilename[fileName]
	if imageAttachmentID == "" || fileAttachmentID == "" {
		t.Fatalf("expected uploaded attachments for %s and %s, got %+v", imageName, fileName, attachments)
	}

	page := waitForAttachmentPublicationOrSkip(t, ctx, client, pageID, attachments)
	if strings.Contains(string(page.BodyADF), "UNKNOWN_MEDIA_ID") {
		t.Fatalf("expected published ADF without UNKNOWN_MEDIA_ID, got %s", string(page.BodyADF))
	}

	runCMS("pull", spaceKey, "--force", "--yes", "--non-interactive")

	filePath = findMarkdownByPageID(t, spaceDir, pageID)
	pulledDoc, err := fs.ReadMarkdownDocument(filePath)
	if err != nil {
		t.Fatalf("ReadMarkdown after pull: %v", err)
	}

	expectedImageRel := filepath.ToSlash(filepath.Join("assets", pageID, imageAttachmentID+"-"+imageName))
	expectedFileRel := filepath.ToSlash(filepath.Join("assets", pageID, fileAttachmentID+"-"+fileName))
	fileToken := mustExtractMarkdownLinkToken(t, pulledDoc.Body, "file")
	fileDest := mustExtractMarkdownDestination(t, pulledDoc.Body, "file")
	if !strings.Contains(pulledDoc.Body, expectedImageRel) {
		t.Fatalf("expected pulled markdown to reference %s, body=\n%s", expectedImageRel, pulledDoc.Body)
	}
	if !strings.Contains(pulledDoc.Body, expectedFileRel) {
		t.Fatalf("expected pulled markdown to reference %s, body=\n%s", expectedFileRel, pulledDoc.Body)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, filepath.FromSlash(expectedImageRel))); err != nil {
		t.Fatalf("expected pulled image asset to exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, filepath.FromSlash(expectedFileRel))); err != nil {
		t.Fatalf("expected pulled file asset to exist: %v", err)
	}

	pulledDoc.Body = strings.TrimSpace(strings.Replace(pulledDoc.Body, fileToken, "", 1)) + "\n"
	if err := fs.WriteMarkdownDocument(filePath, pulledDoc); err != nil {
		t.Fatalf("WriteMarkdown after attachment removal: %v", err)
	}
	if err := os.Remove(filepath.Join(spaceDir, filepath.FromSlash(expectedFileRel))); err != nil {
		t.Fatalf("remove local attachment asset: %v", err)
	}

	runCMS("validate", filePath)
	runCMS("push", filePath, "--on-conflict=cancel", "--yes", "--non-interactive")

	attachments = waitForPageAttachments(t, ctx, client, pageID, func(got []confluence.Attachment) bool {
		if len(got) != 1 {
			return false
		}
		return strings.TrimSpace(got[0].Filename) == imageName
	})
	if len(attachments) != 1 || strings.TrimSpace(attachments[0].Filename) != imageName {
		t.Fatalf("expected only remaining attachment %s, got %+v", imageName, attachments)
	}

	runCMS("pull", spaceKey, "--force", "--yes", "--non-interactive")

	filePath = findMarkdownByPageID(t, spaceDir, pageID)
	finalDoc, err := fs.ReadMarkdownDocument(filePath)
	if err != nil {
		t.Fatalf("ReadMarkdown after deletion pull: %v", err)
	}
	if strings.Contains(finalDoc.Body, fileName) || strings.Contains(finalDoc.Body, fileDest) {
		t.Fatalf("expected deleted attachment reference to be removed, body=\n%s", finalDoc.Body)
	}
	if _, err := os.Stat(filepath.Join(spaceDir, filepath.FromSlash(expectedFileRel))); !os.IsNotExist(err) {
		t.Fatalf("expected deleted local asset to be gone, stat=%v", err)
	}

	state, err := fs.LoadState(spaceDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if _, exists := state.AttachmentIndex[expectedFileRel]; exists {
		t.Fatalf("expected deleted attachment state entry to be removed for %s", expectedFileRel)
	}
	if got := strings.TrimSpace(state.AttachmentIndex[expectedImageRel]); got == "" {
		t.Fatalf("expected remaining attachment state entry for %s", expectedImageRel)
	}
}

func TestWorkflow_PageDeleteArchivesRemotely(t *testing.T) {
	e2eCfg := requireE2EConfig(t)
	spaceKey := e2eCfg.PrimarySpaceKey

	ctx := context.Background()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	client, err := confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-archive-delete-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(confBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command conf %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	runCMS("init")
	runCMS("pull", spaceKey, "--yes", "--non-interactive")

	spaceDir := findPulledSpaceDirBySpaceKey(t, tmpDir, spaceKey)
	filePath, pageID := createE2EScratchPage(t, spaceDir, client, runCMS, "Archive Delete E2E")

	if err := os.Remove(filePath); err != nil {
		t.Fatalf("remove scratch markdown: %v", err)
	}

	runCMS("push", spaceKey, "--on-conflict=cancel", "--yes", "--non-interactive")

	archivedPage := waitForArchivedPageInSpace(t, ctx, client, spaceKey, pageID)
	if !strings.EqualFold(strings.TrimSpace(archivedPage.Status), "archived") {
		t.Fatalf("expected remote page %s to be archived, got status=%q", pageID, archivedPage.Status)
	}

	runCMS("pull", spaceKey, "--yes", "--non-interactive")

	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Fatalf("expected deleted markdown to stay absent after pull, stat=%v", err)
	}

	state, err := fs.LoadState(spaceDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	for relPath, trackedPageID := range state.PagePathIndex {
		if strings.TrimSpace(trackedPageID) == pageID {
			t.Fatalf("expected archived page %s to be removed from tracked state, still mapped at %s", pageID, relPath)
		}
	}
}

func TestWorkflow_EndToEndCleanupParity(t *testing.T) {
	e2eCfg := requireE2EConfig(t)

	ctx := context.Background()
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}

	client, err := confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-cleanup-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCmd := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command %s %v failed: %v\n%s", name, args, err, string(out))
		}
		return string(out)
	}

	runCMS := func(args ...string) string {
		return runCmd(confBin, args...)
	}

	runCMS("init")
	runCMS("pull", e2eCfg.PrimarySpaceKey, "--yes", "--non-interactive")
	runCMS("pull", e2eCfg.SecondarySpaceKey, "--yes", "--non-interactive")

	primaryDir := findPulledSpaceDirBySpaceKey(t, tmpDir, e2eCfg.PrimarySpaceKey)
	secondaryDir := findPulledSpaceDirBySpaceKey(t, tmpDir, e2eCfg.SecondarySpaceKey)

	secondaryTargetPath, secondaryPageID := createE2EScratchPageWithBody(t, secondaryDir, client, runCMS, "Cleanup Parity Cross Space", "Cross-space cleanup target.\n")

	stamp := time.Now().UTC().Format("20060102T150405") + fmt.Sprintf("-%09d", time.Now().UTC().Nanosecond())
	parentTitle := "Cleanup Parity Parent " + stamp
	parentStem := sanitizeE2EFileStem(parentTitle)
	parentDir := filepath.Join(primaryDir, parentStem)
	parentPath := filepath.Join(parentDir, parentStem+".md")
	childTitle := "Cleanup Parity Child " + stamp
	childStem := sanitizeE2EFileStem(childTitle)
	childPath := filepath.Join(parentDir, childStem+".md")
	attachmentSourcePath := filepath.Join(parentDir, "cleanup-note-"+stamp+".txt")

	if err := os.MkdirAll(parentDir, 0o750); err != nil {
		t.Fatalf("MkdirAll parent dir: %v", err)
	}
	if err := os.WriteFile(attachmentSourcePath, []byte("cleanup parity attachment\n"), 0o600); err != nil {
		t.Fatalf("WriteFile attachment: %v", err)
	}
	if err := fs.WriteMarkdownDocument(parentPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: parentTitle},
		Body:        "Parent page for cleanup parity verification.\n",
	}); err != nil {
		t.Fatalf("WriteMarkdown parent: %v", err)
	}

	crossSpaceRel := encodeMarkdownRelPath(relativeMarkdownPath(t, filepath.Dir(childPath), secondaryTargetPath))
	attachmentRel := encodeMarkdownRelPath(relativeMarkdownPath(t, filepath.Dir(childPath), attachmentSourcePath))
	if err := fs.WriteMarkdownDocument(childPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: childTitle},
		Body: fmt.Sprintf(
			"[Cross Space](%s)\n\n[Attachment](%s)\n",
			crossSpaceRel,
			attachmentRel,
		),
	}); err != nil {
		t.Fatalf("WriteMarkdown child: %v", err)
	}

	runCMS("push", e2eCfg.PrimarySpaceKey, "--on-conflict=cancel", "--yes", "--non-interactive")

	parentDoc, err := fs.ReadMarkdownDocument(parentPath)
	if err != nil {
		t.Fatalf("ReadMarkdown parent after push: %v", err)
	}
	parentPageID := strings.TrimSpace(parentDoc.Frontmatter.ID)
	if parentPageID == "" {
		t.Fatal("expected cleanup parity parent page id after push")
	}

	childDoc, err := fs.ReadMarkdownDocument(childPath)
	if err != nil {
		t.Fatalf("ReadMarkdown child after push: %v", err)
	}
	childPageID := strings.TrimSpace(childDoc.Frontmatter.ID)
	if childPageID == "" {
		t.Fatal("expected cleanup parity child page id after push")
	}

	attachments := waitForPageAttachments(t, ctx, client, childPageID, func(got []confluence.Attachment) bool {
		return len(got) == 1
	})
	if len(attachments) != 1 {
		t.Fatalf("expected one attachment on cleanup parity child page, got %+v", attachments)
	}

	state, err := fs.LoadState(primaryDir)
	if err != nil {
		t.Fatalf("LoadState primary: %v", err)
	}

	var normalizedAttachmentPath string
	for relPath, attachmentID := range state.AttachmentIndex {
		if strings.TrimSpace(attachmentID) == strings.TrimSpace(attachments[0].ID) {
			normalizedAttachmentPath = relPath
			break
		}
	}
	if strings.TrimSpace(normalizedAttachmentPath) == "" {
		t.Fatalf("expected attachment %s in local state, state=%+v", attachments[0].ID, state.AttachmentIndex)
	}

	if err := os.Remove(childPath); err != nil {
		t.Fatalf("remove child markdown: %v", err)
	}
	if err := os.Remove(parentPath); err != nil {
		t.Fatalf("remove parent markdown: %v", err)
	}
	runCMS("push", e2eCfg.PrimarySpaceKey, "--on-conflict=cancel", "--yes", "--non-interactive")

	waitForArchivedPageInSpace(t, ctx, client, e2eCfg.PrimarySpaceKey, parentPageID)
	waitForArchivedPageInSpace(t, ctx, client, e2eCfg.PrimarySpaceKey, childPageID)

	if err := os.Remove(secondaryTargetPath); err != nil {
		t.Fatalf("remove secondary target markdown: %v", err)
	}
	runCMS("push", e2eCfg.SecondarySpaceKey, "--on-conflict=cancel", "--yes", "--non-interactive")
	waitForArchivedPageInSpace(t, ctx, client, e2eCfg.SecondarySpaceKey, secondaryPageID)

	runCMS("pull", e2eCfg.PrimarySpaceKey, "--force", "--yes", "--non-interactive")
	runCMS("pull", e2eCfg.SecondarySpaceKey, "--force", "--yes", "--non-interactive")

	assertGitWorkspaceClean(t, tmpDir)
	assertStatusOutputOmitsArtifacts(t, runCMS("status", e2eCfg.PrimarySpaceKey), parentStem, childStem)
	assertStatusOutputOmitsArtifacts(t, runCMS("status", e2eCfg.SecondarySpaceKey), filepath.Base(secondaryTargetPath))

	if _, err := os.Stat(parentPath); !os.IsNotExist(err) {
		t.Fatalf("expected deleted parent markdown to stay absent after cleanup, stat=%v", err)
	}
	if _, err := os.Stat(childPath); !os.IsNotExist(err) {
		t.Fatalf("expected deleted child markdown to stay absent after cleanup, stat=%v", err)
	}
	if _, err := os.Stat(filepath.Join(primaryDir, filepath.FromSlash(normalizedAttachmentPath))); !os.IsNotExist(err) {
		t.Fatalf("expected deleted child attachment asset to stay absent after cleanup, stat=%v", err)
	}
	if _, err := os.Stat(secondaryTargetPath); !os.IsNotExist(err) {
		t.Fatalf("expected deleted cross-space target markdown to stay absent after cleanup, stat=%v", err)
	}
}

func TestWorkflow_PushDryRunNonMutating(t *testing.T) {
	e2eCfg := requireE2EConfig(t)
	spaceKey := e2eCfg.PrimarySpaceKey

	// 0. Setup
	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-dryrun-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(confBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command conf %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	// 1. Init & Pull
	runCMS("init")
	runCMS("pull", spaceKey, "--yes")

	// 2. Modify local
	spaceDir := findPulledSpaceDir(t, tmpDir)
	simplePath := findFirstMarkdownFile(t, spaceDir)
	doc, _ := fs.ReadMarkdownDocument(simplePath)
	originalVersion := doc.Frontmatter.Version
	doc.Body += "\n\nDry run test change"
	_ = fs.WriteMarkdownDocument(simplePath, doc)

	// 3. Run push --dry-run
	runCMS("push", simplePath, "--dry-run", "--yes", "--non-interactive", "--on-conflict=force")

	// 4. Verify local file is UNCHANGED in terms of version
	docAfter, _ := fs.ReadMarkdownDocument(simplePath)
	if docAfter.Frontmatter.Version != originalVersion {
		t.Errorf("Dry run mutated confluence_version! got %d, want %d", docAfter.Frontmatter.Version, originalVersion)
	}
	if docAfter.Body != doc.Body {
		t.Errorf("Dry run mutated body! (expected it to stay with my local changes)")
	}

	// 5. Verify no git tags or branches were created in the temp workspace
	gitCmd := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = tmpDir
		out, _ := cmd.CombinedOutput()
		return string(out)
	}

	gitOut := gitCmd("branch")
	if strings.Contains(gitOut, "sync/") {
		t.Errorf("Dry run left a sync branch in workspace: %s", gitOut)
	}

	tagOut := gitCmd("tag")
	if strings.Contains(tagOut, "confluence-sync/push/") {
		t.Errorf("Dry run created a push tag in workspace: %s", tagOut)
	}

	fmt.Printf("Dry run non-mutation test passed\n")
}

func TestWorkflow_PullDiscardLocal(t *testing.T) {
	e2eCfg := requireE2EConfig(t)
	spaceKey := e2eCfg.PrimarySpaceKey

	// 0. Setup

	rootDir := projectRootFromWD(t)
	confBin := confBinaryForOS(rootDir)

	tmpDir, err := os.MkdirTemp("", "conf-e2e-discard-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(confBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command conf %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	// 1. Init & Pull
	runCMS("init")
	runCMS("pull", spaceKey, "--yes")

	// 2. Modify local
	spaceDir := findPulledSpaceDir(t, tmpDir)
	simplePath := findFirstMarkdownFile(t, spaceDir)
	doc, _ := fs.ReadMarkdownDocument(simplePath)
	doc.Body += "\n\nLocal change to be discarded"
	_ = fs.WriteMarkdownDocument(simplePath, doc)

	// 3. Pull with --discard-local
	runCMS("pull", simplePath, "--discard-local", "--yes")

	// 4. Verify local change is GONE
	docAfter, _ := fs.ReadMarkdownDocument(simplePath)
	if strings.Contains(docAfter.Body, "Local change to be discarded") {
		t.Errorf("Local change was NOT discarded despite --discard-local!")
	}

	fmt.Printf("Pull --discard-local test passed\n")
}
