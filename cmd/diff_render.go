package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/confluence"
	"github.com/rgonek/confluence-markdown-sync/internal/converter"
	"github.com/rgonek/confluence-markdown-sync/internal/fs"
	syncflow "github.com/rgonek/confluence-markdown-sync/internal/sync"
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

type diffMetadataSummary struct {
	Path    string
	Changes []string
}

func summarizeMetadataDrift(relPath string, localRaw, remoteRaw []byte) diffMetadataSummary {
	localDoc, err := fs.ParseMarkdownDocument(localRaw)
	if err != nil {
		return diffMetadataSummary{}
	}
	remoteDoc, err := fs.ParseMarkdownDocument(remoteRaw)
	if err != nil {
		return diffMetadataSummary{}
	}

	changes := make([]string, 0, 3)
	localState := displayDiffState(localDoc.Frontmatter.State)
	remoteState := displayDiffState(remoteDoc.Frontmatter.State)
	if localState != remoteState {
		changes = append(changes, fmt.Sprintf("state: %s -> %s", localState, remoteState))
	}

	localStatus := strings.TrimSpace(localDoc.Frontmatter.Status)
	remoteStatus := strings.TrimSpace(remoteDoc.Frontmatter.Status)
	if localStatus != remoteStatus {
		changes = append(changes, fmt.Sprintf("status: %q -> %q", localStatus, remoteStatus))
	}

	localLabels := fs.NormalizeLabels(localDoc.Frontmatter.Labels)
	remoteLabels := fs.NormalizeLabels(remoteDoc.Frontmatter.Labels)
	if !slices.Equal(localLabels, remoteLabels) {
		changes = append(changes, fmt.Sprintf("labels: %s -> %s", formatDiffLabels(localLabels), formatDiffLabels(remoteLabels)))
	}

	if len(changes) == 0 {
		return diffMetadataSummary{}
	}

	return diffMetadataSummary{
		Path:    filepath.ToSlash(relPath),
		Changes: changes,
	}
}

func writeDiffMetadataSummary(out io.Writer, summaries []diffMetadataSummary) error {
	filtered := make([]diffMetadataSummary, 0, len(summaries))
	for _, summary := range summaries {
		if summary.Path == "" || len(summary.Changes) == 0 {
			continue
		}
		filtered = append(filtered, summary)
	}
	if len(filtered) == 0 {
		return nil
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Path < filtered[j].Path
	})

	if _, err := fmt.Fprintln(out, "metadata drift summary:"); err != nil {
		return fmt.Errorf("write metadata drift summary: %w", err)
	}
	for _, summary := range filtered {
		if _, err := fmt.Fprintf(out, "  %s\n", summary.Path); err != nil {
			return fmt.Errorf("write metadata drift summary: %w", err)
		}
		for _, change := range summary.Changes {
			if _, err := fmt.Fprintf(out, "    - %s\n", change); err != nil {
				return fmt.Errorf("write metadata drift summary: %w", err)
			}
		}
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return fmt.Errorf("write metadata drift summary: %w", err)
	}
	return nil
}

func displayDiffState(state string) string {
	normalized := strings.TrimSpace(strings.ToLower(state))
	if normalized == "" || normalized == "current" {
		return "current"
	}
	return normalized
}

func formatDiffLabels(labels []string) string {
	if len(labels) == 0 {
		return "[]"
	}
	return "[" + strings.Join(labels, ", ") + "]"
}
