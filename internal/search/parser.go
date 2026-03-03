package search

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// ParseResult is the structured representation of a Markdown document's headings and
// code blocks, produced by ParseMarkdownStructure.
type ParseResult struct {
	// Sections holds heading-anchored sections of the document.
	// Each section contains the heading metadata and the body text that follows
	// the heading up to (but not including) the next same-or-higher-level heading.
	Sections []Section

	// CodeBlocks holds all fenced code blocks together with their heading context.
	CodeBlocks []CodeBlock
}

// Section is a slice of Markdown body text bounded by heading boundaries.
type Section struct {
	// HeadingText is the plain text of the heading node.
	HeadingText string

	// HeadingLevel is 1–6.
	HeadingLevel int

	// HeadingPath is the full ancestor hierarchy including this heading,
	// e.g. ["# Overview", "## Auth", "### Token Refresh"].
	HeadingPath []string

	// Content is the raw Markdown text of the section body (excluding the heading line).
	Content string

	// Line is the 1-based line of the heading in the source file.
	Line int
}

// CodeBlock is a fenced code block with its contextual heading hierarchy.
type CodeBlock struct {
	// Language is the info string after the opening fence (may be empty).
	Language string

	// Content is the raw code text inside the fence.
	Content string

	// HeadingPath is the heading ancestry at the point of the code block.
	HeadingPath []string

	// HeadingText is the innermost heading text (empty if the code block precedes all headings).
	HeadingText string

	// HeadingLevel is the level of HeadingText (0 if none).
	HeadingLevel int

	// Line is the 1-based start line of the code block in the source file.
	Line int
}

// headingEntry tracks the active heading stack used during AST traversal.
type headingEntry struct {
	level int
	text  string
	line  int
}

// ParseMarkdownStructure walks the Goldmark AST of the given Markdown source and
// extracts heading-anchored sections and fenced code blocks.
//
// Line numbers are 1-based.  The function is pure: it allocates no I/O resources.
func ParseMarkdownStructure(source []byte) ParseResult {
	parser := goldmark.New().Parser()
	reader := text.NewReader(source)
	doc := parser.Parse(reader)

	var (
		headingStack []headingEntry
		sections     []Section
		codeBlocks   []CodeBlock

		// sectionStart is the byte offset at which the current section body begins
		// (i.e., just after the heading line).
		sectionStart int
	)

	// offsetToLine converts a byte offset into a 1-based line number.
	offsetToLine := func(offset int) int {
		if offset <= 0 {
			return 1
		}
		if offset > len(source) {
			offset = len(source)
		}
		return bytes.Count(source[0:offset], []byte{'\n'}) + 1
	}

	// headingPathStrings builds the HeadingPath slice from the current stack.
	headingPathStrings := func(stack []headingEntry) []string {
		path := make([]string, len(stack))
		for i, e := range stack {
			path[i] = fmt.Sprintf("%s %s", strings.Repeat("#", e.level), e.text)
		}
		return path
	}

	// finishSection closes the pending section, appending it to the sections slice.
	// endOffset is the byte offset at which the section body ends (exclusive).
	finishSection := func(endOffset int) {
		if len(headingStack) == 0 {
			return
		}
		top := headingStack[len(headingStack)-1]
		body := ""
		if sectionStart < endOffset && endOffset <= len(source) {
			body = strings.TrimSpace(string(source[sectionStart:endOffset]))
		}
		sections = append(sections, Section{
			HeadingText:  top.text,
			HeadingLevel: top.level,
			HeadingPath:  headingPathStrings(headingStack),
			Content:      body,
			Line:         top.line,
		})
	}

	// currentHeadingContext returns the heading context at the current position in the AST.
	currentHeadingContext := func() (path []string, text string, level int) {
		path = headingPathStrings(headingStack)
		if len(headingStack) > 0 {
			top := headingStack[len(headingStack)-1]
			text = top.text
			level = top.level
		}
		return
	}

	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		switch n := node.(type) {
		case *ast.Heading:
			if !entering {
				return ast.WalkContinue, nil
			}

			// Determine the byte offset of this heading node.
			headingOffset := 0
			if n.Lines().Len() > 0 {
				headingOffset = n.Lines().At(0).Start
			} else if n.HasChildren() {
				// For setext-style headings the child carries the offset.
				child := n.FirstChild()
				if child != nil && child.Lines().Len() > 0 {
					headingOffset = child.Lines().At(0).Start
				}
			}

			headingLine := offsetToLine(headingOffset)

			// Collect the plain text of the heading.
			headingText := extractHeadingText(n, source)

			// Close any open sections that are at the same or lower depth.
			for len(headingStack) > 0 && headingStack[len(headingStack)-1].level >= n.Level {
				finishSection(headingOffset)
				headingStack = headingStack[:len(headingStack)-1]
			}

			// Push the new heading onto the stack.
			headingStack = append(headingStack, headingEntry{
				level: n.Level,
				text:  headingText,
				line:  headingLine,
			})

			// The section body begins immediately after the heading text.
			// We advance past the entire heading lines block.
			if n.Lines().Len() > 0 {
				last := n.Lines().At(n.Lines().Len() - 1)
				sectionStart = last.Stop
			} else {
				sectionStart = headingOffset
			}

		case *ast.FencedCodeBlock:
			if !entering {
				return ast.WalkContinue, nil
			}

			// Byte offset and line of the opening fence.
			codeOffset := 0
			if n.Lines().Len() > 0 {
				codeOffset = n.Lines().At(0).Start
			}
			codeLine := offsetToLine(codeOffset)

			// Language info string.
			lang := ""
			if n.Info != nil {
				lang = strings.TrimSpace(string(n.Info.Value(source)))
				// Strip options like "go {lineNumbers=true}" — keep only the first token.
				if spaceIdx := strings.IndexByte(lang, ' '); spaceIdx >= 0 {
					lang = lang[:spaceIdx]
				}
			}

			// Code content: concatenate all line segments.
			var codeBuf strings.Builder
			for i := 0; i < n.Lines().Len(); i++ {
				seg := n.Lines().At(i)
				codeBuf.Write(source[seg.Start:seg.Stop])
			}

			path, headText, headLevel := currentHeadingContext()
			codeBlocks = append(codeBlocks, CodeBlock{
				Language:     lang,
				Content:      strings.TrimRight(codeBuf.String(), "\n"),
				HeadingPath:  path,
				HeadingText:  headText,
				HeadingLevel: headLevel,
				Line:         codeLine,
			})
		}
		return ast.WalkContinue, nil
	})

	// Close all remaining open sections from innermost to outermost.
	for len(headingStack) > 0 {
		finishSection(len(source))
		headingStack = headingStack[:len(headingStack)-1]
	}

	return ParseResult{
		Sections:   sections,
		CodeBlocks: codeBlocks,
	}
}

// extractHeadingText returns the concatenated plain text of all Text children
// within a heading node.
func extractHeadingText(heading *ast.Heading, source []byte) string {
	var buf strings.Builder
	_ = ast.Walk(heading, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if t, ok := node.(*ast.Text); ok {
			buf.Write(t.Value(source))
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(buf.String())
}
