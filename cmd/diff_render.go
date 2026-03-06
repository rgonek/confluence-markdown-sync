package cmd

import (
	"context"
	"fmt"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

func hydrateDiffPageMetadata(
	ctx context.Context,
	remote syncflow.PullRemote,
	page confluence.Page,
	relPath string,
) (confluence.Page, []syncflow.PullDiagnostic) {
	diagnostics := make([]syncflow.PullDiagnostic, 0, 2)

	status, err := remote.GetContentStatus(ctx, page.ID, page.Status)
	if err != nil {
		diagnostics = append(diagnostics, syncflow.PullDiagnostic{
			Path:    filepath.ToSlash(relPath),
			Code:    "CONTENT_STATUS_FETCH_FAILED",
			Message: fmt.Sprintf("fetch content status for page %s: %v", page.ID, err),
		})
	} else {
		page.ContentStatus = status
	}

	labels, err := remote.GetLabels(ctx, page.ID)
	if err != nil {
		diagnostics = append(diagnostics, syncflow.PullDiagnostic{
			Path:    filepath.ToSlash(relPath),
			Code:    "LABELS_FETCH_FAILED",
			Message: fmt.Sprintf("fetch labels for page %s: %v", page.ID, err),
		})
	} else {
		page.Labels = labels
	}

	return page, diagnostics
}

func renderDiffMarkdown(
	ctx context.Context,
	page confluence.Page,
	spaceKey string,
	sourcePath string,
	relPath string,
	pagePathByIDAbs map[string]string,
	attachmentPathByID map[string]string,
) ([]byte, []syncflow.PullDiagnostic, error) {
	forward, err := converter.Forward(ctx, page.BodyADF, converter.ForwardConfig{
		LinkHook:  syncflow.NewForwardLinkHook(sourcePath, pagePathByIDAbs, spaceKey),
		MediaHook: syncflow.NewForwardMediaHook(sourcePath, attachmentPathByID),
	}, sourcePath)
	if err != nil {
		return nil, nil, fmt.Errorf("convert page %s: %w", page.ID, err)
	}

	doc := fs.MarkdownDocument{
		Frontmatter: fs.Frontmatter{
			Title:   page.Title,
			ID:      page.ID,
			Version: page.Version,
			State:   page.Status,
			Status:  page.ContentStatus,
			Labels:  page.Labels,
		},
		Body: forward.Markdown,
	}

	rendered, err := fs.FormatMarkdownDocument(doc)
	if err != nil {
		return nil, nil, fmt.Errorf("format page %s markdown: %w", page.ID, err)
	}

	diagnostics := make([]syncflow.PullDiagnostic, 0, len(forward.Warnings))
	for _, warning := range forward.Warnings {
		diagnostics = append(diagnostics, syncflow.PullDiagnostic{
			Path:    filepath.ToSlash(relPath),
			Code:    string(warning.Type),
			Message: warning.Message,
		})
	}

	return rendered, diagnostics, nil
}

func copyLocalMarkdownSnapshot(spaceDir, snapshotDir string) error {
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

		raw, err := os.ReadFile(path) //nolint:gosec // path comes from filepath.WalkDir under spaceDir
		if err != nil {
			return err
		}
		raw, err = normalizeDiffMarkdown(raw)
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(spaceDir, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(snapshotDir, relPath)
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o750); err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, raw, 0o600); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("prepare local markdown snapshot: %w", err)
	}
	return nil
}

func normalizeDiffMarkdown(raw []byte) ([]byte, error) {
	doc, err := fs.ParseMarkdownDocument(raw)
	if err != nil {
		return raw, nil
	}

	doc.Frontmatter.CreatedBy = ""
	doc.Frontmatter.CreatedAt = ""
	doc.Frontmatter.UpdatedBy = ""
	doc.Frontmatter.UpdatedAt = ""

	normalized, err := fs.FormatMarkdownDocument(doc)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

func buildDiffAttachmentPathByID(spaceDir string, attachmentIndex map[string]string) map[string]string {
	out := map[string]string{}
	relPaths := make([]string, 0, len(attachmentIndex))
	for relPath := range attachmentIndex {
		relPaths = append(relPaths, relPath)
	}
	sort.Strings(relPaths)

	for _, relPath := range relPaths {
		attachmentID := strings.TrimSpace(attachmentIndex[relPath])
		if attachmentID == "" {
			continue
		}
		if _, exists := out[attachmentID]; exists {
			continue
		}

		normalized := filepath.ToSlash(filepath.Clean(relPath))
		normalized = strings.TrimPrefix(normalized, "./")
		out[attachmentID] = filepath.Join(spaceDir, filepath.FromSlash(normalized))
	}

	return out
}
