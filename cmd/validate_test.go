package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rgonek/confluence-markdown-sync/internal/config"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func TestResolveValidateTargetContext_ResolvesSanitizedSpaceDirectoryByKey(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	spaceDir := filepath.Join(repo, "Technical documentation (TD)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: "Root",
			ID:    "1",

			Version:                1,
			ConfluenceLastModified: "2026-02-01T10:00:00Z",
		},
		Body: "content\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		PagePathIndex: map[string]string{"root.md": "1"},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	chdirRepo(t, repo)

	ctx, err := resolveValidateTargetContext(config.Target{Mode: config.TargetModeSpace, Value: "TD"})
	if err != nil {
		t.Fatalf("resolveValidateTargetContext() error: %v", err)
	}

	if ctx.spaceDir != spaceDir {
		t.Fatalf("spaceDir = %q, want %q", ctx.spaceDir, spaceDir)
	}
	if len(ctx.files) != 1 || filepath.Base(ctx.files[0]) != "root.md" {
		t.Fatalf("files = %v, want [root.md]", ctx.files)
	}
}

func TestRunValidateTarget_BlocksTamperedIDAgainstState(t *testing.T) {
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	rootPath := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Version: 1},
		Body:        "content\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "2", Version: 1},
		Body:        "content\n",
	})

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	err := runValidateTargetWithContext(context.Background(), out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if err == nil {
		t.Fatal("expected validate to fail for tampered id")
	}
	if !strings.Contains(out.String(), "[immutable] id") {
		t.Fatalf("expected immutable id issue, got:\n%s", out.String())
	}
}

func TestRunValidateTarget_IgnoresSpaceFrontmatter(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	rootPath := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Version: 1},
		Body:        "content\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Version: 1},
		Body:        "content\n",
	})

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	err := runValidateTargetWithContext(context.Background(), out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if err != nil {
		t.Fatalf("expected validate success when space differs, got: %v\nOutput:\n%s", err, out.String())
	}
}

func TestRunValidateTarget_BlocksCurrentToDraftTransition(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	rootPath := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Version: 1, State: "current"},
		Body:        "content\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Version: 1, State: "draft"},
		Body:        "content\n",
	})

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	err := runValidateTargetWithContext(context.Background(), out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if err == nil {
		t.Fatal("expected validate to fail for current->draft transition")
	}
	if !strings.Contains(out.String(), "[immutable] state") {
		t.Fatalf("expected immutable state issue, got:\n%s", out.String())
	}
}

func TestRunValidateTarget_AllowsDraftToDraftForExistingDraftPage(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	rootPath := filepath.Join(spaceDir, "root.md")
	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Version: 1, State: "draft"},
		Body:        "content\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	writeMarkdown(t, rootPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Version: 1, State: "draft"},
		Body:        "updated content\n",
	})

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	if err := runValidateTargetWithContext(context.Background(), out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("expected validate success for draft->draft, got: %v\nOutput:\n%s", err, out.String())
	}
}

func TestRunValidateTarget_AllowsNonAssetsMediaReferenceWithinSpace(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(filepath.Join(spaceDir, "images"), 0o750); err != nil {
		t.Fatalf("mkdir images dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(spaceDir, "images", "outside.png"), []byte("png"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root"},
		Body:        "![image](images/outside.png)\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG"}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	if err := runValidateTargetWithContext(context.Background(), out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("expected validate success, got: %v\nOutput:\n%s", err, out.String())
	}
}

func TestRunValidateTarget_AllowsLocalFileLinkAttachment(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(filepath.Join(spaceDir, "assets"), 0o750); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(spaceDir, "assets", "manual.pdf"), []byte("pdf"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root"},
		Body:        "[Manual](assets/manual.pdf)\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG"}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	if err := runValidateTargetWithContext(context.Background(), out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("expected validate success, got: %v\nOutput:\n%s", err, out.String())
	}
}

func TestRunValidateTarget_WarnsForMermaidFenceButSucceeds(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root"},
		Body:        "```mermaid\ngraph TD\n  A --> B\n```\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG"}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	if err := runValidateTargetWithContext(context.Background(), out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("expected validate success, got: %v\nOutput:\n%s", err, out.String())
	}

	got := out.String()
	if !strings.Contains(got, "MERMAID_PRESERVED_AS_CODEBLOCK") {
		t.Fatalf("expected Mermaid warning code, got:\n%s", got)
	}
	if !strings.Contains(got, "line ") {
		t.Fatalf("expected Mermaid warning line detail, got:\n%s", got)
	}
	if !strings.Contains(got, "Validation successful") {
		t.Fatalf("expected validate success footer, got:\n%s", got)
	}
}

func TestRunValidateTarget_FailsForMissingAssetFile(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(filepath.Join(spaceDir, "assets"), 0o750); err != nil {
		t.Fatalf("mkdir assets dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root"},
		Body:        "![missing](assets/missing.png)\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG"}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	err := runValidateTargetWithContext(context.Background(), out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if err == nil {
		t.Fatal("expected validate to fail for missing asset")
	}
	if !strings.Contains(out.String(), "asset assets/missing.png not found") {
		t.Fatalf("expected missing asset validation error, got:\n%s", out.String())
	}
}

func TestRunValidateTarget_OutsideAssetPathShowsActionableMessage(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root"},
		Body:        "![outside](../outside.png)\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG"}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	err := runValidateTargetWithContext(context.Background(), out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if err == nil {
		t.Fatal("expected validate to fail for outside-space asset reference")
	}
	if !strings.Contains(out.String(), "outside the space directory") {
		t.Fatalf("expected actionable outside-space message, got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Engineering (ENG)/assets/") {
		t.Fatalf("expected target assets directory hint, got:\n%s", out.String())
	}
}

func TestRunValidateTarget_AllowsCrossSpaceEncodedRelativeLink(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	engDir := filepath.Join(repo, "Engineering (ENG)")
	tdDir := filepath.Join(repo, "Technical Docs (TD)")
	if err := os.MkdirAll(engDir, 0o750); err != nil {
		t.Fatalf("mkdir eng dir: %v", err)
	}
	if err := os.MkdirAll(tdDir, 0o750); err != nil {
		t.Fatalf("mkdir td dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(tdDir, "target.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Target", ID: "200", Version: 1},
		Body:        "target\n",
	})
	if err := fs.SaveState(tdDir, fs.SpaceState{SpaceKey: "TD", PagePathIndex: map[string]string{"target.md": "200"}}); err != nil {
		t.Fatalf("save td state: %v", err)
	}

	writeMarkdown(t, filepath.Join(engDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "100", Version: 1},
		Body:        "[cross](../Technical%20Docs%20(TD)/target.md)\n",
	})
	if err := fs.SaveState(engDir, fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "100"}}); err != nil {
		t.Fatalf("save eng state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	if err := runValidateTargetWithContext(context.Background(), out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("expected validate success, got: %v\nOutput:\n%s", err, out.String())
	}
}

func TestRunValidateTarget_AllowsLinkToSimultaneousNewPageInSpaceScope(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "Fancy-Extensions.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Fancy Extensions"},
		Body:        "[New page](New-Page.md)\n",
	})
	writeMarkdown(t, filepath.Join(spaceDir, "New-Page.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "New Page"},
		Body:        "hello\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG"}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	if err := runValidateTargetWithContext(context.Background(), out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"}); err != nil {
		t.Fatalf("expected validate success, got: %v\nOutput:\n%s", err, out.String())
	}
}

func TestRunValidateTarget_FileModeAllowsBrandNewPageWithoutID(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	newPath := filepath.Join(spaceDir, "new-page.md")
	writeMarkdown(t, newPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "New Page"},
		Body:        "hello\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG"}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	if err := runValidateTargetWithContext(context.Background(), out, config.Target{Mode: config.TargetModeFile, Value: newPath}); err != nil {
		t.Fatalf("expected validate success for brand-new file, got: %v\nOutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Validation successful") {
		t.Fatalf("expected validate success footer, got:\n%s", out.String())
	}
}

func TestRunValidateTargetWithContext_ReturnsCancellation(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Version: 1},
		Body:        "content\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	chdirRepo(t, repo)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := runValidateTargetWithContext(ctx, &bytes.Buffer{}, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got: %v", err)
	}
}

func TestRunValidateTarget_BlocksDuplicatePageIDs(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)
	setupEnv(t)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	// Two different files claiming the same Confluence page ID
	writeMarkdown(t, filepath.Join(spaceDir, "page-a.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Page A", ID: "42", Version: 1},
		Body:        "content a\n",
	})
	writeMarkdown(t, filepath.Join(spaceDir, "page-b.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Page B", ID: "42", Version: 1},
		Body:        "content b\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{
		SpaceKey:      "ENG",
		PagePathIndex: map[string]string{"page-a.md": "42"},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	chdirRepo(t, repo)
	out := &bytes.Buffer{}
	err := runValidateTargetWithContext(context.Background(), out, config.Target{Mode: config.TargetModeSpace, Value: "Engineering (ENG)"})
	if err == nil {
		t.Fatal("expected validate to fail for duplicate page IDs")
	}
	got := out.String()
	if !strings.Contains(got, `"42"`) {
		t.Fatalf("expected duplicate id %q in output, got:\n%s", "42", got)
	}
	if !strings.Contains(got, "page-a.md") || !strings.Contains(got, "page-b.md") {
		t.Fatalf("expected both duplicate file paths in output, got:\n%s", got)
	}
}

func TestDetectDuplicatePageIDs_ReturnsNilForUniqueIDs(t *testing.T) {
	t.Parallel()
	index := map[string]string{
		"a.md": "1",
		"b.md": "2",
		"c.md": "3",
	}
	errs := detectDuplicatePageIDs(index)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for unique IDs, got: %v", errs)
	}
}

func TestDetectDuplicatePageIDs_ReturnsDuplicates(t *testing.T) {
	t.Parallel()
	index := map[string]string{
		"a.md": "99",
		"b.md": "99",
		"c.md": "100",
	}
	errs := detectDuplicatePageIDs(index)
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got: %v", errs)
	}
	if !strings.Contains(errs[0], `"99"`) {
		t.Errorf("expected error to mention id 99, got: %s", errs[0])
	}
	if !strings.Contains(errs[0], "a.md") || !strings.Contains(errs[0], "b.md") {
		t.Errorf("expected both file paths in error, got: %s", errs[0])
	}
}

func TestDetectDuplicatePageIDs_SkipsEmptyIDs(t *testing.T) {
	t.Parallel()
	index := map[string]string{
		"a.md": "",
		"b.md": "",
	}
	errs := detectDuplicatePageIDs(index)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for empty IDs, got: %v", errs)
	}
}

func TestRunValidateCommand(t *testing.T) {
	runParallelCommandTest(t)
	repo := t.TempDir()
	setupGitRepo(t, repo)

	spaceDir := filepath.Join(repo, "Engineering (ENG)")
	if err := os.MkdirAll(spaceDir, 0o750); err != nil {
		t.Fatalf("mkdir space dir: %v", err)
	}

	writeMarkdown(t, filepath.Join(spaceDir, "root.md"), fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{Title: "Root", ID: "1", Version: 1},
		Body:        "content\n",
	})
	if err := fs.SaveState(spaceDir, fs.SpaceState{SpaceKey: "ENG", PagePathIndex: map[string]string{"root.md": "1"}}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	runGitForTest(t, repo, "add", ".")
	runGitForTest(t, repo, "commit", "-m", "baseline")

	chdirRepo(t, repo)

	cmd := newValidateCmd()
	cmd.SetOut(&bytes.Buffer{})

	err := runValidateCommand(cmd, config.Target{Mode: config.TargetModeSpace, Value: spaceDir})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}
