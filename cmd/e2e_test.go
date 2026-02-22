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

func TestWorkflow_ConflictResolution(t *testing.T) {
	if os.Getenv("ATLASSIAN_DOMAIN") == "" {
		t.Skip("Skipping E2E test: ATLASSIAN_DOMAIN not set")
	}

	spaceKey := "TD"
	pageID := "2981891" // Simple.md

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

	// 0. Build cms
	wd, _ := os.Getwd()
	// Find project root by looking for go.mod
	rootDir := wd
	for {
		if _, err := os.Stat(filepath.Join(rootDir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(rootDir)
		if parent == rootDir {
			break
		}
		rootDir = parent
	}
	cmsBin := filepath.Join(rootDir, "cms.exe")

	// 1. Setup temp workspace
	tmpDir, err := os.MkdirTemp("", "cms-e2e-*")
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
		return runCmd(cmsBin, args...)
	}

	// init
	runCMS("init")

	// pull
	runCMS("pull", spaceKey, "--yes")

	// Verify Simple.md exists
	simplePath := filepath.Join(tmpDir, "Technical documentation (TD)", "Simple.md")
	if _, err := os.Stat(simplePath); err != nil {
		t.Fatalf("Simple.md not found after pull: %v", err)
	}

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
	cmd := exec.Command(cmsBin, "push", "Technical documentation (TD)/Simple.md", "--on-conflict=cancel", "--yes", "--non-interactive")
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
	cmd = exec.Command(cmsBin, "pull", "Technical documentation (TD)/Simple.md", "--yes", "--non-interactive")
	cmd.Dir = tmpDir
	out, _ = cmd.CombinedOutput()
	if !strings.Contains(string(out), "conflict") {
		t.Fatalf("Pull should have reported conflict: %s", string(out))
	}
	fmt.Printf("Pull reported conflict as expected\n")

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
	runCmd("git", "add", "Technical documentation (TD)/Simple.md")

	// 7. Push again -> should succeed
	runCMS("push", "Technical documentation (TD)/Simple.md", "--on-conflict=cancel", "--yes", "--non-interactive")
	fmt.Printf("Push after manual resolution succeeded\n")
}

func TestWorkflow_PushAutoPullMerge(t *testing.T) {
	if os.Getenv("ATLASSIAN_DOMAIN") == "" {
		t.Skip("Skipping E2E test: ATLASSIAN_DOMAIN not set")
	}

	spaceKey := "TD"
	pageID := "2981891" // Simple.md

	ctx := context.Background()
	cfg, err := config.Load("")
	client, _ := confluence.NewClient(confluence.ClientConfig{
		BaseURL:  cfg.Domain,
		Email:    cfg.Email,
		APIToken: cfg.APIToken,
	})

	// 0. Setup
	wd, _ := os.Getwd()
	// Find project root by looking for go.mod
	rootDir := wd
	for {
		if _, err := os.Stat(filepath.Join(rootDir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(rootDir)
		if parent == rootDir {
			break
		}
		rootDir = parent
	}
	cmsBin := filepath.Join(rootDir, "cms.exe")

	tmpDir, err := os.MkdirTemp("", "cms-e2e-autopull-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(cmsBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command cms %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	// 1. Init & Pull
	runCMS("init")
	runCMS("pull", spaceKey, "--yes")

	// 2. Modify local
	simplePath := filepath.Join(tmpDir, "Technical documentation (TD)", "Simple.md")
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
	pcmd := exec.Command(cmsBin, "push", "Technical documentation (TD)/Simple.md", "--on-conflict=pull-merge", "--yes", "--non-interactive")
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
	doc, _ = fs.ReadMarkdownDocument(simplePath)
	// After pull (even with conflict), the frontmatter version should be updated
	if doc.Frontmatter.Version != remotePage.Version+1 {
		t.Fatalf("Local version should be updated after auto pull-merge: got %d, want %d", doc.Frontmatter.Version, remotePage.Version+1)
	}
	// Note: body might have conflict markers or be merged depending on git
}

func TestWorkflow_AgenticFullCycle(t *testing.T) {
	if os.Getenv("ATLASSIAN_DOMAIN") == "" {
		t.Skip("Skipping E2E test: ATLASSIAN_DOMAIN not set")
	}

	spaceKey := "TD"

	// 0. Setup
	wd, _ := os.Getwd()
	// Find project root by looking for go.mod
	rootDir := wd
	for {
		if _, err := os.Stat(filepath.Join(rootDir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(rootDir)
		if parent == rootDir {
			break
		}
		rootDir = parent
	}
	cmsBin := filepath.Join(rootDir, "cms.exe")

	tmpDir, err := os.MkdirTemp("", "cms-e2e-agentic-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(cmsBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command cms %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	// 1. Init & Pull
	runCMS("init")
	runCMS("pull", spaceKey, "--yes", "--non-interactive")

	// 2. Edit
	simplePath := filepath.Join(tmpDir, "Technical documentation (TD)", "Simple.md")
	doc, err := fs.ReadMarkdownDocument(simplePath)
	if err != nil {
		t.Fatalf("ReadMarkdown: %v", err)
	}
	doc.Body += "\n\nAgentic update " + time.Now().Format(time.RFC3339)
	if err := fs.WriteMarkdownDocument(simplePath, doc); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}

	// 3. Validate
	runCMS("validate", "Technical documentation (TD)/Simple.md")

	// 4. Diff
	runCMS("diff", "Technical documentation (TD)/Simple.md")

	// 5. Push
	runCMS("push", "Technical documentation (TD)/Simple.md", "--on-conflict=cancel", "--yes", "--non-interactive")

	fmt.Printf("Agentic full cycle succeeded\n")
}

func TestWorkflow_PushDryRunNonMutating(t *testing.T) {
	if os.Getenv("ATLASSIAN_DOMAIN") == "" {
		t.Skip("Skipping E2E test: ATLASSIAN_DOMAIN not set")
	}

	spaceKey := "TD"

	// 0. Setup
	wd, _ := os.Getwd()
	rootDir := wd
	for {
		if _, err := os.Stat(filepath.Join(rootDir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(rootDir)
		if parent == rootDir {
			break
		}
		rootDir = parent
	}
	cmsBin := filepath.Join(rootDir, "cms.exe")

	tmpDir, err := os.MkdirTemp("", "cms-e2e-dryrun-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(cmsBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command cms %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	// 1. Init & Pull
	runCMS("init")
	runCMS("pull", spaceKey, "--yes")

	// 2. Modify local
	simplePath := filepath.Join(tmpDir, "Technical documentation (TD)", "Simple.md")
	doc, _ := fs.ReadMarkdownDocument(simplePath)
	originalVersion := doc.Frontmatter.Version
	doc.Body += "\n\nDry run test change"
	_ = fs.WriteMarkdownDocument(simplePath, doc)

	// 3. Run push --dry-run
	runCMS("push", "Technical documentation (TD)/Simple.md", "--dry-run", "--yes", "--non-interactive", "--on-conflict=force")

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
	if os.Getenv("ATLASSIAN_DOMAIN") == "" {
		t.Skip("Skipping E2E test: ATLASSIAN_DOMAIN not set")
	}

	spaceKey := "TD"

	// 0. Setup

	wd, _ := os.Getwd()
	rootDir := wd
	for {
		if _, err := os.Stat(filepath.Join(rootDir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(rootDir)
		if parent == rootDir {
			break
		}
		rootDir = parent
	}
	cmsBin := filepath.Join(rootDir, "cms.exe")

	tmpDir, err := os.MkdirTemp("", "cms-e2e-discard-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	runCMS := func(args ...string) string {
		cmd := exec.Command(cmsBin, args...)
		cmd.Dir = tmpDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("Command cms %v failed: %v\n%s", args, err, string(out))
		}
		return string(out)
	}

	// 1. Init & Pull
	runCMS("init")
	runCMS("pull", spaceKey, "--yes")

	// 2. Modify local
	simplePath := filepath.Join(tmpDir, "Technical documentation (TD)", "Simple.md")
	doc, _ := fs.ReadMarkdownDocument(simplePath)
	doc.Body += "\n\nLocal change to be discarded"
	_ = fs.WriteMarkdownDocument(simplePath, doc)

	// 3. Pull with --discard-local
	runCMS("pull", "Technical documentation (TD)/Simple.md", "--discard-local", "--yes")

	// 4. Verify local change is GONE
	docAfter, _ := fs.ReadMarkdownDocument(simplePath)
	if strings.Contains(docAfter.Body, "Local change to be discarded") {
		t.Errorf("Local change was NOT discarded despite --discard-local!")
	}

	fmt.Printf("Pull --discard-local test passed\n")
}
