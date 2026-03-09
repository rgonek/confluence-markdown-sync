//go:build e2e

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
		t.Fatalf("Local edit should survive auto pull-merge via merged content or conflict markers, got:\n%s", bodyText)
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

func TestWorkflow_MermaidPushPreservesCodeBlock(t *testing.T) {
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

	spaceDir := findPulledSpaceDir(t, tmpDir)
	stamp := time.Now().UTC().Format("20060102T150405")
	title := "Mermaid E2E " + stamp
	filePath := filepath.Join(spaceDir, "Mermaid-E2E-"+stamp+".md")
	if err := fs.WriteMarkdownDocument(filePath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: title},
		Body:        "```mermaid\ngraph TD\n  A --> B\n```\n",
	}); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}

	runCMS("validate", filePath)
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
	for {
		page, err := client.GetPage(ctx, pageID)
		if err == nil && adfContainsCodeBlockLanguage(page.BodyADF, "mermaid") {
			return
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("GetPage(%s) did not expose Mermaid codeBlock before timeout: %v", pageID, err)
			}
			t.Fatalf("remote ADF for page %s did not contain Mermaid codeBlock before timeout: %s", pageID, string(page.BodyADF))
		}
		time.Sleep(2 * time.Second)
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
	attachmentIDByFilename := map[string]string{}
	for _, attachment := range attachments {
		attachmentIDByFilename[strings.TrimSpace(attachment.Filename)] = strings.TrimSpace(attachment.ID)
	}

	imageAttachmentID := attachmentIDByFilename[imageName]
	fileAttachmentID := attachmentIDByFilename[fileName]
	if imageAttachmentID == "" || fileAttachmentID == "" {
		t.Fatalf("expected uploaded attachments for %s and %s, got %+v", imageName, fileName, attachments)
	}

	page := waitForPageADF(t, ctx, client, pageID, func(adf []byte) bool {
		if strings.Contains(string(adf), "UNKNOWN_MEDIA_ID") {
			return false
		}
		attachmentIDs, renderIDs := collectADFMediaIdentitySets(adf)
		if _, ok := attachmentIDs[imageAttachmentID]; !ok {
			return false
		}
		if _, ok := attachmentIDs[fileAttachmentID]; !ok {
			return false
		}
		return len(renderIDs) >= 2
	})
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

	pulledDoc.Body = fmt.Sprintf("![Diagram](%s)\n", expectedImageRel)
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
	if strings.Contains(finalDoc.Body, fileName) || strings.Contains(finalDoc.Body, expectedFileRel) {
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
	if runtime.GOOS == "windows" {
		return filepath.Join(rootDir, "conf.exe")
	}
	return filepath.Join(rootDir, "conf")
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

	return createE2EScratchPage(t, spaceDir, client, runCMS, "Conflict E2E")
}

func createE2EScratchPage(t *testing.T, spaceDir string, client *confluence.Client, runCMS func(args ...string) string, titlePrefix string) (string, string) {
	t.Helper()

	stamp := time.Now().UTC().Format("20060102T150405") + fmt.Sprintf("-%09d", time.Now().UTC().Nanosecond())
	title := fmt.Sprintf("%s %s", titlePrefix, stamp)
	parentDir := filepath.Dir(findFirstMarkdownFile(t, spaceDir))
	filePath := filepath.Join(parentDir, sanitizeE2EFileStem(title)+".md")
	if err := fs.WriteMarkdownDocument(filePath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: title},
		Body:        "Scratch page for Confluence E2E coverage.\n",
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
