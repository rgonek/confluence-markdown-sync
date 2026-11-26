package cmd

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rgonek/confluence-markdown-sync/internal/search"
)

// searchTOON encodes the scalar, string array, and table forms used by search (TOON 4.4).
type searchTOON struct {
	buf strings.Builder
	err error
}

func (enc *searchTOON) writeTo(out io.Writer) error {
	if enc.err != nil {
		return enc.err
	}
	_, err := io.WriteString(out, strings.TrimSuffix(enc.buf.String(), "\n"))
	return err
}

func (enc *searchTOON) field(key, value string) {
	fmt.Fprintf(&enc.buf, "%s: %s\n", key, enc.stringToken(value))
}

func (enc *searchTOON) array(key string, values []string) {
	if len(values) == 0 {
		fmt.Fprintf(&enc.buf, "%s: []\n", key)
		return
	}
	tokens := make([]string, len(values))
	for i, value := range values {
		tokens[i] = enc.stringToken(value)
	}
	fmt.Fprintf(&enc.buf, "%s[%d]: %s\n", key, len(tokens), strings.Join(tokens, ","))
}

var toonNumericLike = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

// stringToken applies the TOON quoting and escaping rules with comma as the delimiter.
func (enc *searchTOON) stringToken(value string) string {
	if !utf8.ValidString(value) {
		enc.err = fmt.Errorf("search: cannot encode invalid UTF-8 as TOON")
		return `""`
	}
	if value != "" && value == strings.Trim(value, " \t") &&
		value != "true" && value != "false" && value != "null" &&
		value[0] != '-' && value[0] != '#' && !toonNumericLike.MatchString(value) &&
		strings.IndexFunc(value, func(r rune) bool {
			return r < ' ' || strings.ContainsRune(`:,"\[]{}`, r)
		}) == -1 {
		return value
	}

	var quoted strings.Builder
	quoted.WriteByte('"')
	for _, r := range value {
		switch r {
		case '\\', '"':
			quoted.WriteByte('\\')
			quoted.WriteRune(r)
		case '\n':
			quoted.WriteString(`\n`)
		case '\r':
			quoted.WriteString(`\r`)
		case '\t':
			quoted.WriteString(`\t`)
		default:
			if r < ' ' {
				fmt.Fprintf(&quoted, `\u%04x`, r)
			} else {
				quoted.WriteRune(r)
			}
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}

func toonScore(score float64) string {
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return "null"
	}
	if score == 0 {
		return "0"
	}
	return strconv.FormatFloat(score, 'f', -1, 64)
}

func printSearchTOON(out io.Writer, results []search.SearchResult, detail string, summary searchSummary) error {
	var enc searchTOON
	enc.field("count", summary.count)
	if summary.message != "" {
		enc.field("message", summary.message)
	}

	if len(results) == 0 {
		enc.array("results", nil)
	} else {
		fields := "path,heading_path,heading_text,line,snippet"
		if detail != "minimal" {
			fields += ",title,space_key,labels,created_by,created_at,updated_by,updated_at,score"
		}
		if detail == "full" {
			fields += ",id,type,page_id,heading_level,language,mod_time,content"
		}
		fmt.Fprintf(&enc.buf, "results[%d]{%s}:\n", len(results), fields)
		for _, result := range results {
			enc.resultRow(result, detail)
		}
	}
	if len(summary.hints) > 0 {
		enc.array("help", summary.hints)
	}
	return enc.writeTo(out)
}

func (enc *searchTOON) resultRow(result search.SearchResult, detail string) {
	doc := result.Document
	str := enc.stringToken
	cells := []string{
		str(doc.Path),
		str(strings.Join(doc.HeadingPath, " > ")),
		str(doc.HeadingText),
		strconv.Itoa(doc.Line),
		str(result.Snippet),
	}
	if detail != "minimal" {
		cells = append(cells,
			str(doc.Title),
			str(doc.SpaceKey),
			str(strings.Join(doc.Labels, ", ")),
			str(doc.CreatedBy),
			str(doc.CreatedAt),
			str(doc.UpdatedBy),
			str(doc.UpdatedAt),
			toonScore(result.Score),
		)
	}
	if detail == "full" {
		var modTime string
		if doc.ModTime != nil {
			modTime = doc.ModTime.Format(time.RFC3339Nano)
		}
		cells = append(cells,
			str(doc.ID),
			str(doc.Type),
			str(doc.PageID),
			strconv.Itoa(doc.HeadingLevel),
			str(doc.Language),
			str(modTime),
			str(doc.Content),
		)
	}
	fmt.Fprintf(&enc.buf, "  %s\n", strings.Join(cells, ","))
}
