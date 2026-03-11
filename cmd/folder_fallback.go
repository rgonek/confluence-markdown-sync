package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
)

func handleFolderPageFallback(in io.Reader, out io.Writer, spaceDir string, folderErr *syncflow.FolderPageFallbackRequiredError) (string, bool, error) {
	if folderErr == nil {
		return "", false, nil
	}
	if flagNonInteractive {
		return "", false, fmt.Errorf("folder %q requires explicit interactive confirmation before converting it into a page-backed hierarchy; rerun without --non-interactive and accept the fallback or rename the folder", folderErr.Path)
	}

	title := fmt.Sprintf("Convert %q into a page-with-subpages node?", folderErr.Path)
	description := fmt.Sprintf("Confluence cannot keep %q as a pure folder (%s). Accepting will add %s locally and continue the push.", folderErr.Path, folderErr.Reason, previewIndexPathForDir(folderErr.Path))
	accepted := false

	if outputSupportsProgress(out) {
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(title).
					Description(description).
					Value(&accepted),
			),
		).WithOutput(out)
		if err := form.Run(); err != nil {
			return "", false, err
		}
	} else {
		if _, err := fmt.Fprintf(out, "%s\n%s [y/N]: ", title, description); err != nil {
			return "", false, err
		}
		choice, err := readPromptLine(in)
		if err != nil {
			return "", false, err
		}
		choice = strings.ToLower(strings.TrimSpace(choice))
		accepted = choice == "y" || choice == "yes"
	}

	if !accepted {
		return "", false, fmt.Errorf("folder downgrade for %q was not accepted; rename or restructure locally before retrying", folderErr.Path)
	}

	indexRelPath, err := materializeFolderAsPage(spaceDir, folderErr.Path)
	if err != nil {
		return "", false, err
	}
	_, _ = fmt.Fprintf(out, "created page-backed hierarchy node %s to preserve local intent\n", indexRelPath)
	return indexRelPath, true, nil
}

func materializeFolderAsPage(spaceDir, dirPath string) (string, error) {
	dirPath = normalizeRepoRelPath(dirPath)
	if dirPath == "" || dirPath == "." {
		return "", fmt.Errorf("folder path is required")
	}

	indexRelPath := previewIndexPathForDir(dirPath)
	indexAbsPath := filepath.Join(spaceDir, filepath.FromSlash(indexRelPath))
	if _, err := os.Stat(indexAbsPath); err == nil {
		return indexRelPath, nil
	}
	if err := os.MkdirAll(filepath.Dir(indexAbsPath), 0o750); err != nil {
		return "", err
	}
	if err := fs.WriteMarkdownDocument(indexAbsPath, fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title: filepath.Base(filepath.FromSlash(dirPath)),
		},
		Body: "",
	}); err != nil {
		return "", err
	}
	return indexRelPath, nil
}
