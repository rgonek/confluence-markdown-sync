package search

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rgonek/confluence-markdown-sync/internal/fs"
)

// Indexer orchestrates file walking and calls Store methods.
// It is backend-agnostic and operates exclusively via the Store interface.
type Indexer struct {
	store   Store
	rootDir string
}

// NewIndexer creates an Indexer that writes to store and scans from rootDir.
func NewIndexer(store Store, rootDir string) *Indexer {
	return &Indexer{store: store, rootDir: rootDir}
}

// Reindex performs a full reindex of all discovered spaces in rootDir.
// It returns the total number of documents indexed.
func (ix *Indexer) Reindex() (int, error) {
	states, err := fs.FindAllStateFiles(ix.rootDir)
	if err != nil {
		return 0, fmt.Errorf("search indexer: discover spaces: %w", err)
	}

	total := 0
	for spaceDir, state := range states {
		count, err := ix.IndexSpace(spaceDir, state.SpaceKey)
		if err != nil {
			return total, fmt.Errorf("search indexer: index space %s: %w", spaceDir, err)
		}
		total += count
	}

	if err := ix.store.UpdateMeta(); err != nil {
		return total, fmt.Errorf("search indexer: update meta: %w", err)
	}
	return total, nil
}

// IndexSpace walks spaceDir for Markdown files and indexes them all.
// Any existing documents for those files are replaced.
// It returns the number of documents indexed.
func (ix *Indexer) IndexSpace(spaceDir, spaceKey string) (int, error) {
	return ix.walkAndIndex(spaceDir, spaceKey, time.Time{})
}

// IncrementalUpdate indexes only files whose mtime is newer than the last
// recorded index time.  Falls back to a full reindex if no prior timestamp exists.
func (ix *Indexer) IncrementalUpdate() (int, error) {
	lastAt, err := ix.store.LastIndexedAt()
	if err != nil {
		return 0, fmt.Errorf("search indexer: read last-indexed-at: %w", err)
	}
	if lastAt.IsZero() {
		return ix.Reindex()
	}

	states, err := fs.FindAllStateFiles(ix.rootDir)
	if err != nil {
		return 0, fmt.Errorf("search indexer: discover spaces: %w", err)
	}

	total := 0
	for spaceDir, state := range states {
		count, err := ix.walkAndIndex(spaceDir, state.SpaceKey, lastAt)
		if err != nil {
			return total, fmt.Errorf("search indexer: incremental index space %s: %w", spaceDir, err)
		}
		total += count
	}

	if total > 0 {
		if err := ix.store.UpdateMeta(); err != nil {
			return total, fmt.Errorf("search indexer: update meta: %w", err)
		}
	}
	return total, nil
}

// Close releases the underlying store.
func (ix *Indexer) Close() error {
	return ix.store.Close()
}

// — private helpers —

// walkAndIndex walks spaceDir and indexes all .md files.
// If cutoff is non-zero, only files with mtime > cutoff are re-indexed.
func (ix *Indexer) walkAndIndex(spaceDir, spaceKey string, cutoff time.Time) (int, error) {
	total := 0
	spaceName := filepath.Base(spaceDir)

	err := filepath.WalkDir(spaceDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Skip assets/ and all hidden directories (e.g., .git)
			if d.Name() == "assets" || strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			return nil
		}

		// Mtime filter for incremental updates.
		if !cutoff.IsZero() {
			info, err := d.Info()
			if err != nil || !info.ModTime().After(cutoff) {
				return nil
			}
		}

		relPath, err := filepath.Rel(spaceDir, path)
		if err != nil {
			return nil // skip; unexpected path
		}
		relPath = filepath.ToSlash(relPath)

		// Build a repo-root-relative path: "<spaceDirName>/<relPath>".
		docPath := spaceName + "/" + relPath

		count, err := ix.indexFile(path, docPath, spaceKey)
		if err != nil {
			// Best-effort: skip broken files rather than aborting the walk.
			return nil
		}
		total += count
		return nil
	})
	return total, err
}

// indexFile reads the Markdown document at absPath, parses its structure, and
// upserts all resulting documents (1 page + N sections + M code blocks) into the store.
// docPath is the repository-relative path (forward slashes).
func (ix *Indexer) indexFile(absPath, docPath, spaceKey string) (int, error) {
	info, err := os.Stat(absPath)
	if err != nil {
		return 0, err
	}

	mdDoc, err := fs.ReadMarkdownDocument(absPath)
	if err != nil {
		return 0, fmt.Errorf("read document %s: %w", absPath, err)
	}

	fm := mdDoc.Frontmatter
	labels := fs.NormalizeLabels(fm.Labels)
	modTime := info.ModTime()

	// Delete existing documents for this path before reinserting.
	if err := ix.store.DeleteByPath(docPath); err != nil {
		return 0, fmt.Errorf("delete old docs for %s: %w", docPath, err)
	}

	// Build the document set.
	docs := make([]Document, 0, 32)

	// 1. Page document: full body as Content for FTS across entire page.
	docs = append(docs, Document{
		ID:        "page:" + docPath,
		Type:      DocTypePage,
		Path:      docPath,
		PageID:    fm.ID,
		Title:     fm.Title,
		SpaceKey:  spaceKey,
		Labels:    labels,
		Content:   mdDoc.Body,
		ModTime:   &modTime,
		CreatedBy: fm.CreatedBy,
		CreatedAt: normalizeDate(fm.CreatedAt),
		UpdatedBy: fm.UpdatedBy,
		UpdatedAt: normalizeDate(fm.UpdatedAt),
	})

	// 2. Section and code-block documents.
	parsed := ParseMarkdownStructure([]byte(mdDoc.Body))

	for _, sec := range parsed.Sections {
		docs = append(docs, Document{
			ID:           fmt.Sprintf("section:%s:%d", docPath, sec.Line),
			Type:         DocTypeSection,
			Path:         docPath,
			PageID:       fm.ID,
			Title:        fm.Title,
			SpaceKey:     spaceKey,
			Labels:       labels,
			Content:      sec.Content,
			HeadingPath:  sec.HeadingPath,
			HeadingText:  sec.HeadingText,
			HeadingLevel: sec.HeadingLevel,
			Line:         sec.Line,
			ModTime:      &modTime,
			CreatedBy:    fm.CreatedBy,
			CreatedAt:    normalizeDate(fm.CreatedAt),
			UpdatedBy:    fm.UpdatedBy,
			UpdatedAt:    normalizeDate(fm.UpdatedAt),
		})
	}

	for _, cb := range parsed.CodeBlocks {
		docs = append(docs, Document{
			ID:           fmt.Sprintf("code:%s:%d", docPath, cb.Line),
			Type:         DocTypeCode,
			Path:         docPath,
			PageID:       fm.ID,
			Title:        fm.Title,
			SpaceKey:     spaceKey,
			Labels:       labels,
			Content:      cb.Content,
			HeadingPath:  cb.HeadingPath,
			HeadingText:  cb.HeadingText,
			HeadingLevel: cb.HeadingLevel,
			Language:     cb.Language,
			Line:         cb.Line,
			ModTime:      &modTime,
			CreatedBy:    fm.CreatedBy,
			CreatedAt:    normalizeDate(fm.CreatedAt),
			UpdatedBy:    fm.UpdatedBy,
			UpdatedAt:    normalizeDate(fm.UpdatedAt),
		})
	}

	if err := ix.store.Index(docs); err != nil {
		return 0, fmt.Errorf("store index for %s: %w", docPath, err)
	}
	return len(docs), nil
}

// normalizeDate attempts to parse s using common date/datetime layouts and returns
// an RFC3339-formatted string in UTC. Returns s unchanged if it is already RFC3339,
// or the original string if it cannot be parsed at all.
func normalizeDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return s
}
