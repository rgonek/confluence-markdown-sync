package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/search"
	"github.com/rgonek/confluence-markdown-sync/internal/search/blevestore"
	"github.com/rgonek/confluence-markdown-sync/internal/search/sqlitestore"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const searchIndexDir = ".confluence-search-index"

func newSearchCmd() *cobra.Command {
	var (
		flagSearchSpace      string
		flagSearchLabels     []string
		flagSearchHeading    string
		flagSearchFormat     string
		flagSearchLimit      int
		flagSearchReindex    bool
		flagSearchEngine     string
		flagSearchListLabels bool
		flagSearchListSpaces bool
	)

	cmd := &cobra.Command{
		Use:   "search QUERY",
		Short: "Full-text search across the local Confluence Markdown workspace",
		Long: `search indexes and queries Markdown files in your local Confluence workspace.

The index is built automatically on first use and updated incrementally on
subsequent runs. Use --reindex to force a full rebuild.

Examples:
  conf search "oauth token refresh"
  conf search "deploy pipeline" --space DEV --label ci
  conf search --list-labels
  conf search --list-spaces --format json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			query := ""
			if len(args) > 0 {
				query = args[0]
			}
			return runSearch(cmd, query, searchRunOptions{
				space:      flagSearchSpace,
				labels:     flagSearchLabels,
				heading:    flagSearchHeading,
				format:     flagSearchFormat,
				limit:      flagSearchLimit,
				reindex:    flagSearchReindex,
				engine:     flagSearchEngine,
				listLabels: flagSearchListLabels,
				listSpaces: flagSearchListSpaces,
			})
		},
	}

	cmd.Flags().StringVar(&flagSearchSpace, "space", "", "Filter results to a specific Confluence space key")
	cmd.Flags().StringArrayVar(&flagSearchLabels, "label", nil, "Filter by label (repeatable)")
	cmd.Flags().StringVar(&flagSearchHeading, "heading", "", "Restrict results to sections under headings matching this substring")
	cmd.Flags().StringVar(&flagSearchFormat, "format", "auto", `Output format: "text", "json", or "auto" (TTY→text, pipe→json)`)
	cmd.Flags().IntVar(&flagSearchLimit, "limit", 20, "Maximum number of results to return")
	cmd.Flags().BoolVar(&flagSearchReindex, "reindex", false, "Force a full reindex before searching")
	cmd.Flags().StringVar(&flagSearchEngine, "engine", "sqlite", `Search backend: "sqlite" or "bleve"`)
	cmd.Flags().BoolVar(&flagSearchListLabels, "list-labels", false, "List all indexed labels and exit")
	cmd.Flags().BoolVar(&flagSearchListSpaces, "list-spaces", false, "List all indexed spaces and exit")

	return cmd
}

type searchRunOptions struct {
	space      string
	labels     []string
	heading    string
	format     string
	limit      int
	reindex    bool
	engine     string
	listLabels bool
	listSpaces bool
}

func runSearch(cmd *cobra.Command, query string, opts searchRunOptions) error {
	out := cmd.OutOrStdout()

	repoRoot, err := gitRepoRoot()
	if err != nil {
		return err
	}

	store, err := openSearchStore(opts.engine, repoRoot)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	indexer := search.NewIndexer(store, repoRoot)

	if opts.reindex {
		count, err := indexer.Reindex()
		if err != nil {
			return fmt.Errorf("search: reindex: %w", err)
		}
		_, _ = fmt.Fprintf(out, "Reindexed %d document(s)\n", count)
	} else {
		_, err := indexer.IncrementalUpdate()
		if err != nil {
			return fmt.Errorf("search: incremental update: %w", err)
		}
	}

	format := resolveSearchFormat(opts.format, out)

	if opts.listLabels {
		labels, err := store.ListLabels()
		if err != nil {
			return fmt.Errorf("search: list labels: %w", err)
		}
		return printSearchStringList(out, labels, format)
	}

	if opts.listSpaces {
		spaces, err := store.ListSpaces()
		if err != nil {
			return fmt.Errorf("search: list spaces: %w", err)
		}
		return printSearchStringList(out, spaces, format)
	}

	if query == "" && !opts.listLabels && !opts.listSpaces {
		return fmt.Errorf("search: QUERY argument is required (or use --list-labels / --list-spaces)")
	}

	results, err := store.Search(search.SearchOptions{
		Query:         query,
		SpaceKey:      opts.space,
		Labels:        opts.labels,
		HeadingFilter: opts.heading,
		Limit:         opts.limit,
	})
	if err != nil {
		return fmt.Errorf("search: query: %w", err)
	}

	return printSearchResults(out, results, format)
}

// openSearchStore opens the appropriate Store backend based on engine name.
func openSearchStore(engine, repoRoot string) (search.Store, error) {
	indexRoot := filepath.Join(repoRoot, searchIndexDir)

	switch strings.ToLower(engine) {
	case "sqlite", "":
		dbPath := filepath.Join(indexRoot, "search.db")
		return sqlitestore.Open(dbPath)
	case "bleve":
		blevePath := filepath.Join(indexRoot, "bleve")
		return blevestore.Open(blevePath)
	default:
		return nil, fmt.Errorf("search: unknown engine %q (valid values: sqlite, bleve)", engine)
	}
}

// resolveSearchFormat resolves "auto" to "text" or "json" based on TTY detection.
func resolveSearchFormat(format string, out io.Writer) string {
	if format != "auto" {
		return format
	}
	// If out is not os.Stdout fall back to json (pipe-like context).
	if out == os.Stdout && term.IsTerminal(int(os.Stdout.Fd())) {
		return "text"
	}
	return "json"
}

// printSearchResults renders search results in the requested format.
func printSearchResults(out io.Writer, results []search.SearchResult, format string) error {
	if format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}

	// Text format
	if len(results) == 0 {
		_, _ = fmt.Fprintln(out, "No results found.")
		return nil
	}

	for _, r := range results {
		doc := r.Document
		// Header line: path + title + labels
		labelsStr := ""
		if len(doc.Labels) > 0 {
			labelsStr = " [" + strings.Join(doc.Labels, ", ") + "]"
		}
		titleStr := ""
		if doc.Title != "" {
			titleStr = " - " + doc.Title
		}
		_, _ = fmt.Fprintf(out, "%s%s%s\n", doc.Path, titleStr, labelsStr)

		// Section context
		if doc.Type != search.DocTypePage && len(doc.HeadingPath) > 0 {
			headings := make([]string, len(doc.HeadingPath))
			for i, h := range doc.HeadingPath {
				headings[i] = strings.TrimLeft(h, "# ")
				headings[i] = "## " + headings[i]
				// Re-use the original heading text (which already has #-prefix) as-is.
				headings[i] = h
			}
			lineInfo := ""
			if doc.Line > 0 {
				lineInfo = fmt.Sprintf(" (line %d)", doc.Line)
			}
			_, _ = fmt.Fprintf(out, "  %s%s\n", strings.Join(doc.HeadingPath, " > "), lineInfo)
		}

		// Snippet
		if r.Snippet != "" {
			_, _ = fmt.Fprintf(out, "    ...%s...\n", strings.TrimSpace(r.Snippet))
		}
	}
	return nil
}

// updateSearchIndexForSpace opens the default SQLite search store and runs an
// incremental update scoped to a single space directory. Errors are non-fatal
// from the caller's perspective — the function itself returns the error so the
// caller can emit a warning.
func updateSearchIndexForSpace(repoRoot, spaceDir, spaceKey string, out io.Writer) error {
	dbPath := filepath.Join(repoRoot, searchIndexDir, "search.db")
	store, err := sqlitestore.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open search store: %w", err)
	}
	defer func() { _ = store.Close() }()

	indexer := search.NewIndexer(store, repoRoot)
	count, err := indexer.IndexSpace(spaceDir, spaceKey)
	if err != nil {
		return fmt.Errorf("index space %s: %w", spaceKey, err)
	}
	if count > 0 {
		_, _ = fmt.Fprintf(out, "Updated search index: %d document(s) for space %s\n", count, spaceKey)
	}
	return nil
}

// printSearchStringList renders a list of strings (labels or spaces) in the requested format.
func printSearchStringList(out io.Writer, items []string, format string) error {
	if format == "json" {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(items)
	}

	// Text format
	for _, item := range items {
		_, _ = fmt.Fprintln(out, item)
	}
	return nil
}

// projectResult returns a copy of r with fields zeroed out based on detail level.
// "full" returns r unchanged. "standard" drops Content, ID, PageID, Type, Language,
// HeadingLevel, ModTime. "minimal" keeps only Path, HeadingPath, HeadingText, Line,
// Snippet. Unknown values fall back to "full".
func projectResult(r search.SearchResult, detail string) search.SearchResult {
	switch detail {
	case "standard":
		r.Document = search.Document{
			Path:        r.Document.Path,
			Title:       r.Document.Title,
			SpaceKey:    r.Document.SpaceKey,
			Labels:      r.Document.Labels,
			HeadingPath: r.Document.HeadingPath,
			HeadingText: r.Document.HeadingText,
			Line:        r.Document.Line,
		}
		return r
	case "minimal":
		r.Document = search.Document{
			Path:        r.Document.Path,
			HeadingPath: r.Document.HeadingPath,
			HeadingText: r.Document.HeadingText,
			Line:        r.Document.Line,
		}
		r.Score = 0
		return r
	default: // "full" and unknown values
		return r
	}
}
