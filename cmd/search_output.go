package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/rgonek/confluence-markdown-sync/internal/search"
	"golang.org/x/term"
)

// resolveSearchFormat uses the actual output stream for terminal detection.
func resolveSearchFormat(format string, out io.Writer) string {
	if format != "auto" {
		return format
	}
	if file, ok := out.(*os.File); ok && term.IsTerminal(int(file.Fd())) { //nolint:gosec // Fd is small and fits in int
		return "text"
	}
	return "toon"
}

type searchSummary struct {
	count   string
	message string
	hints   []string
}

func searchCount(shown int, total uint64) string {
	return fmt.Sprintf("%d of %d total", shown, total)
}

func (opts searchRunOptions) filterArgs() []string {
	var args []string
	for _, filter := range []struct{ name, value string }{
		{"--space", opts.space},
		{"--heading", opts.heading},
		{"--created-by", opts.createdBy},
		{"--updated-by", opts.updatedBy},
		{"--created-after", opts.createdAfter},
		{"--created-before", opts.createdBefore},
		{"--updated-after", opts.updatedAfter},
		{"--updated-before", opts.updatedBefore},
	} {
		if filter.value != "" {
			args = append(args, filter.name, quoteSearchArg(filter.value))
		}
	}
	for _, label := range opts.labels {
		args = append(args, "--label", quoteSearchArg(label))
	}
	return args
}

func (opts searchRunOptions) summary(query string, filters []string, shown int, total uint64) searchSummary {
	summary := searchSummary{count: searchCount(shown, total)}
	if total > 0 && uint64(shown) == total {
		return summary
	}

	engine := " --engine " + quoteSearchArg(opts.engine)
	flags := engine + " --format " + opts.format + " --result-detail " + opts.resultDetail
	if len(filters) > 0 {
		flags += " " + strings.Join(filters, " ")
	}
	var queryArg string
	if query != "" {
		queryArg = " -- " + quoteSearchArg(query)
	}

	if total == 0 {
		summary.message = "No results found"
		if query != "" {
			summary.message += " for " + strconv.Quote(query)
		}
		if len(filters) > 0 {
			summary.message += " with filters " + strings.Join(filters, " ")
		}
		summary.message += "."
		summary.hints = []string{
			"Run `conf search --list-spaces" + engine + "` to discover indexed spaces.",
			"Run `conf search --list-labels" + engine + "` to discover indexed labels.",
			"Run `conf search --reindex" + flags + queryArg + "` to rebuild the index and retry.",
		}
	} else {
		summary.hints = []string{
			fmt.Sprintf("Run `conf search --limit %d%s%s` to see all %d results.", total, flags, queryArg, total),
		}
	}
	return summary
}

var simpleSearchArg = regexp.MustCompile(`^[A-Za-z0-9_./:+-]+$`)
var powerShellSingleQuotes = strings.NewReplacer(
	"'", "''",
	"\u2018", "\u2018\u2018",
	"\u2019", "\u2019\u2019",
	"\u201a", "\u201a\u201a",
	"\u201b", "\u201b\u201b",
)

// quoteSearchArg quotes an argument for PowerShell on Windows and POSIX shells elsewhere.
func quoteSearchArg(value string) string {
	if simpleSearchArg.MatchString(value) {
		return value
	}
	if runtime.GOOS == "windows" {
		return "'" + powerShellSingleQuotes.Replace(value) + "'"
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func printSearchResults(out io.Writer, results []search.SearchResult, format, detail string, summary searchSummary) error {
	switch format {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	case "toon":
		return printSearchTOON(out, results, detail, summary)
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "count: %s\n", summary.count)
	if summary.message != "" {
		fmt.Fprintln(&buf, summary.message)
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
		fmt.Fprintf(&buf, "%s%s%s\n", doc.Path, titleStr, labelsStr)

		// Metadata byline (only when any field is present)
		var metaParts []string
		if doc.CreatedBy != "" {
			metaParts = append(metaParts, "Created by: "+doc.CreatedBy)
		}
		if doc.CreatedAt != "" {
			metaParts = append(metaParts, "Created: "+shortDate(doc.CreatedAt))
		}
		if doc.UpdatedBy != "" {
			metaParts = append(metaParts, "Updated by: "+doc.UpdatedBy)
		}
		if doc.UpdatedAt != "" {
			metaParts = append(metaParts, "Updated: "+shortDate(doc.UpdatedAt))
		}
		if len(metaParts) > 0 {
			fmt.Fprintf(&buf, "  %s\n", strings.Join(metaParts, "  "))
		}

		// Section context
		if doc.Type != search.DocTypePage && len(doc.HeadingPath) > 0 {
			lineInfo := ""
			if doc.Line > 0 {
				lineInfo = fmt.Sprintf(" (line %d)", doc.Line)
			}
			fmt.Fprintf(&buf, "  %s%s\n", strings.Join(doc.HeadingPath, " > "), lineInfo)
		}

		// Snippet
		if r.Snippet != "" {
			fmt.Fprintf(&buf, "    ...%s...\n", strings.TrimSpace(r.Snippet))
		}
	}
	for _, hint := range summary.hints {
		fmt.Fprintf(&buf, "help: %s\n", hint)
	}
	_, err := io.WriteString(out, buf.String())
	return err
}

// printSearchStringList renders the values of a search facet.
func printSearchStringList(out io.Writer, facet string, items []string, format string) error {
	var message string
	if len(items) == 0 {
		message = "No indexed " + facet + "."
	}
	switch format {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(items)
	case "toon":
		var enc searchTOON
		enc.field("count", searchCount(len(items), uint64(len(items))))
		enc.array(facet, items)
		if message != "" {
			enc.field("message", message)
		}
		return enc.writeTo(out)
	default:
		var buf strings.Builder
		for _, item := range items {
			fmt.Fprintln(&buf, item)
		}
		if message != "" {
			fmt.Fprintln(&buf, message)
		}
		_, err := io.WriteString(out, buf.String())
		return err
	}
}
