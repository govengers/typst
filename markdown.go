package typst

import (
	"bytes"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extensionast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// markdownParser parses CommonMark with the GitHub extensions (tables, strikethrough, task
// lists, bare links); tablelessParser leaves tables out. goldmark's parsers are safe for
// concurrent use.
var (
	markdownParser  = goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()
	tablelessParser = goldmark.New(goldmark.WithExtensions(extension.Strikethrough, extension.TaskList, extension.Linkify)).Parser()
)

// linkSchemes are the URL schemes FromMarkdown keeps as links; any other link renders as its
// text only, so no javascript:, file: or data: target reaches a PDF.
var linkSchemes = map[string]bool{"http": true, "https": true, "mailto": true}

// MaximumMarkdownSize is the largest input FromMarkdown converts; a longer input is returned as
// Escape of it, plain text. goldmark's parse is quadratic on deeply nested input, and this bound
// keeps the worst case near half a second.
const MaximumMarkdownSize = 32 << 10

const (
	// maximumNesting bounds how deep the calls FromMarkdown emits nest; deeper Markdown keeps
	// its text without the structure. typst refuses markup nested too deeply ("maximum show rule
	// depth exceeded" from 17 block quotes in 0.15.1), and the template's own nesting counts too.
	maximumNesting = 8
	// maximumLinkLength is the longest link target typst accepts ("URL is too long" beyond it,
	// 0.13 to 0.15); a longer link keeps only its text.
	maximumLinkLength = 8000
	// maximumTableCells bounds the table cells an input may make. goldmark fills every short
	// row up to the header's width, so 32 KiB of one-cell rows under a wide header become
	// millions of cells and gigabytes; beyond the bound tables stay text.
	maximumTableCells = 1 << 16
	// maximumOutputSize bounds the markup FromMarkdown returns; a reference link repeated
	// copies its target each time. Beyond it the input is returned as Escape of it.
	maximumOutputSize = 32 * MaximumMarkdownSize
)

// FromMarkdown converts Markdown written by a user (CommonMark with GitHub tables,
// strikethrough, task lists and bare links) into Typst markup. Every character of the input
// ends up as escaped text or inside a string literal; the structure is built only from function
// calls this converter emits (#heading, #list, #enum, #table, #raw, #link, ...), so the input
// cannot reach Typst code. Raw HTML stays visible as text, images render as their alternative
// text, and links keep only http, https and mailto targets.
//
// The output stays within what typst accepts and the work within a bound: structure nested
// deeper than eight calls (block quotes, lists, tables, headings, emphasis, links, counted
// together) keeps only its text, a link target longer than typst accepts keeps only its label,
// tables stay text when they would make too many cells, and an input longer than
// MaximumMarkdownSize, or one whose markup grows beyond 32 times that, is returned as Escape of
// it. Keep the template's own nesting around the result shallow.
//
// Pass the result to the template as data and evaluate it there:
//
//	#eval(data.description, mode: "markup")
func FromMarkdown(markdown string) string {
	if len(markdown) > MaximumMarkdownSize {
		return Escape(markdown)
	}
	source := []byte(strings.ToValidUTF8(markdown, "\uFFFD"))
	parser := markdownParser
	if tableCells(source) > maximumTableCells {
		parser = tablelessParser
	}
	writer := &markdownWriter{source: source}
	writer.blocks(parser.Parse(text.NewReader(source)), "\n\n")
	if writer.full() {
		return Escape(markdown)
	}
	return writer.output.String()
}

// tableCells estimates from above how many table cells goldmark makes of source: per paragraph
// (lines up to a blank line), the line count times the widest row that may be a delimiter row.
func tableCells(source []byte) int {
	cells, lines, columns := 0, 0, 0
	for line := range bytes.Lines(source) {
		if len(bytes.TrimSpace(line)) == 0 {
			cells += lines * columns
			lines, columns = 0, 0
			continue
		}
		lines++
		if bytes.Contains(line, []byte("-")) && bytes.Contains(line, []byte("|")) && len(bytes.Trim(line, " \t\r\n|:->")) == 0 {
			columns = max(columns, bytes.Count(line, []byte("|"))+1)
		}
	}
	return cells + lines*columns
}

type markdownWriter struct {
	source []byte
	output strings.Builder
	depth  int // how many calls with content enclose the current position
}

// nest writes body inside the call that open starts and closing ends, or body alone beyond
// maximumNesting.
func (w *markdownWriter) nest(open, closing string, body func()) {
	if w.depth >= maximumNesting {
		body()
		return
	}
	w.depth++
	w.write(open)
	body()
	w.write(closing)
	w.depth--
}

// full reports whether the output outgrew maximumOutputSize; FromMarkdown then discards it,
// so the writer stops.
func (w *markdownWriter) full() bool {
	return w.output.Len() > maximumOutputSize
}

func (w *markdownWriter) write(parts ...string) {
	if w.full() {
		return
	}
	for _, part := range parts {
		w.output.WriteString(part)
	}
}

// blocks writes the block children of parent, separated by separator.
func (w *markdownWriter) blocks(parent ast.Node, separator string) {
	for child := parent.FirstChild(); child != nil && !w.full(); child = child.NextSibling() {
		if child != parent.FirstChild() {
			w.write(separator)
		}
		w.block(child)
	}
}

func (w *markdownWriter) block(node ast.Node) {
	switch node := node.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		w.inlines(node)
	case *ast.Heading:
		w.nest("#heading(level: "+strconv.Itoa(node.Level)+")[", "]", func() { w.inlines(node) })
	case *ast.ThematicBreak:
		w.write("#line(length: 100%)")
	case *ast.FencedCodeBlock:
		w.write("#raw(", Quote(w.lines(node)), ", block: true")
		if language := node.Language(w.source); len(language) > 0 {
			w.write(", lang: ", Quote(string(language)))
		}
		w.write(")")
	case *ast.CodeBlock:
		w.write("#raw(", Quote(w.lines(node)), ", block: true)")
	case *ast.Blockquote:
		w.nest("#quote(block: true)[", "]", func() { w.blocks(node, "\n\n") })
	case *ast.List:
		w.list(node)
	case *ast.HTMLBlock:
		content := w.lines(node)
		if node.HasClosure() {
			content += "\n" + strings.TrimSuffix(string(node.ClosureLine.Value(w.source)), "\n")
		}
		w.write(Escape(content))
	case *extensionast.Table:
		w.table(node)
	default:
		// A node kind this converter does not know: keep its content, drop its structure.
		w.blocks(node, "\n\n")
	}
}

// lines returns the raw lines of a code or HTML block without the final line break.
func (w *markdownWriter) lines(node ast.Node) string {
	var content strings.Builder
	lines := node.Lines()
	for index := range lines.Len() {
		segment := lines.At(index)
		content.Write(segment.Value(w.source))
	}
	return strings.TrimSuffix(content.String(), "\n")
}

func (w *markdownWriter) list(list *ast.List) {
	if w.depth >= maximumNesting {
		// Too deep for a call: every item becomes a paragraph of its own.
		w.blocks(list, "\n\n")
		return
	}
	open := "#list("
	if list.IsOrdered() {
		open = "#enum(start: " + strconv.Itoa(list.Start) + ", "
	}
	// A loose list separates its items' paragraphs; a tight item holds one text block and
	// maybe a nested list, which needs no paragraph break.
	separator := "\n"
	if !list.IsTight {
		open += "tight: false, "
		separator = "\n\n"
	}
	w.nest(open, ")", func() {
		for item := list.FirstChild(); item != nil; item = item.NextSibling() {
			if item != list.FirstChild() {
				w.write(", ")
			}
			w.write("[")
			w.blocks(item, separator)
			w.write("]")
		}
	})
}

func (w *markdownWriter) table(table *extensionast.Table) {
	if w.depth >= maximumNesting {
		// Too deep for a call: every row becomes a paragraph of its cells.
		for row := table.FirstChild(); row != nil; row = row.NextSibling() {
			if row != table.FirstChild() {
				w.write("\n\n")
			}
			w.cells(row, " ")
		}
		return
	}
	alignments := make([]string, len(table.Alignments))
	for index, alignment := range table.Alignments {
		alignments[index] = typstAlignment(alignment)
	}
	open := "#table(columns: " + strconv.Itoa(len(alignments)) + ", align: (" + strings.Join(alignments, ", ") + ")"
	w.nest(open, ")", func() {
		// goldmark gives every row the header's number of cells, filling a short row with empty
		// ones and dropping the cells beyond.
		for row := table.FirstChild(); row != nil; row = row.NextSibling() {
			if _, header := row.(*extensionast.TableHeader); header {
				w.write(", table.header([")
				w.cells(row, "], [")
				w.write("])")
				continue
			}
			w.write(", [")
			w.cells(row, "], [")
			w.write("]")
		}
	})
}

// cells writes the inlines of a table row's cells, separated by separator.
func (w *markdownWriter) cells(row ast.Node, separator string) {
	for cell := row.FirstChild(); cell != nil; cell = cell.NextSibling() {
		if cell != row.FirstChild() {
			w.write(separator)
		}
		w.inlines(cell)
	}
}

func typstAlignment(alignment extensionast.Alignment) string {
	switch alignment {
	case extensionast.AlignLeft:
		return "left"
	case extensionast.AlignRight:
		return "right"
	case extensionast.AlignCenter:
		return "center"
	default:
		return "auto"
	}
}

// inlines writes the inline children of parent.
func (w *markdownWriter) inlines(parent ast.Node) {
	for child := parent.FirstChild(); child != nil && !w.full(); child = child.NextSibling() {
		w.inline(child)
	}
}

func (w *markdownWriter) inline(node ast.Node) {
	switch node := node.(type) {
	case *ast.Text:
		w.text(node)
	case *ast.String:
		w.write(Escape(string(node.Value)))
	case *ast.CodeSpan:
		w.write("#raw(", Quote(w.codeSpan(node)), ")")
	case *ast.Emphasis:
		function := "#emph["
		if node.Level >= 2 {
			function = "#strong["
		}
		w.nest(function, "]", func() { w.inlines(node) })
	case *extensionast.Strikethrough:
		w.nest("#strike[", "]", func() { w.inlines(node) })
	case *ast.Link:
		w.link(string(resolveMarkdown(node.Destination)), func() { w.inlines(node) })
	case *ast.AutoLink:
		destination, label := string(node.URL(w.source)), string(node.Label(w.source))
		if node.AutoLinkType == ast.AutoLinkEmail && !strings.HasPrefix(strings.ToLower(destination), "mailto:") {
			destination = "mailto:" + destination
		}
		w.link(destination, func() { w.write(Escape(label)) })
	case *ast.RawHTML:
		var content strings.Builder
		for index := range node.Segments.Len() {
			segment := node.Segments.At(index)
			content.Write(segment.Value(w.source))
		}
		w.write(Escape(content.String()))
	case *extensionast.TaskCheckBox:
		if node.IsChecked {
			w.write(Escape("[x] "))
		} else {
			w.write(Escape("[ ] "))
		}
	default:
		// An image renders as its alternative text; an unknown inline as its content.
		w.inlines(node)
	}
}

// text writes a text node with Markdown's backslash escapes and entities resolved, then
// escaped for Typst.
func (w *markdownWriter) text(node *ast.Text) {
	value := node.Value(w.source)
	if !node.IsRaw() {
		value = resolveMarkdown(value)
	}
	w.write(Escape(string(value)))
	switch {
	case node.HardLineBreak():
		w.write("#linebreak()\n")
	case node.SoftLineBreak():
		w.write("\n")
	}
}

// entityPattern matches one HTML entity or numeric character reference.
var entityPattern = regexp.MustCompile(`^&(?:#[0-9]{1,7}|#[xX][0-9a-fA-F]{1,6}|[A-Za-z][A-Za-z0-9]{1,31});`)

// resolveMarkdown resolves Markdown's backslash escapes and entities in one pass, so what one
// of them produces is never read again: \&copy; and &#38;copy; stay the text &copy;.
func resolveMarkdown(value []byte) []byte {
	if !bytes.ContainsAny(value, `\&`) {
		return value
	}
	resolved := make([]byte, 0, len(value))
	for index := 0; index < len(value); index++ {
		switch character := value[index]; {
		case character == '\\' && index+1 < len(value) && util.IsPunct(value[index+1]):
			index++
			resolved = append(resolved, value[index])
		case character == '&':
			entity := entityPattern.Find(value[index:])
			if entity == nil {
				resolved = append(resolved, character)
				continue
			}
			// Each resolver sees only this entity, so the order of the two does not matter.
			resolved = append(resolved, util.ResolveEntityNames(util.ResolveNumericReferences(entity))...)
			index += len(entity) - 1
		default:
			resolved = append(resolved, character)
		}
	}
	return resolved
}

// lineEndings turns every line ending into a space.
var lineEndings = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

// codeSpan returns the text of an inline code span; a line break inside it is a space.
func (w *markdownWriter) codeSpan(node *ast.CodeSpan) string {
	var content strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if text, ok := child.(*ast.Text); ok {
			content.Write(text.Segment.Value(w.source))
		}
	}
	return lineEndings.Replace(content.String())
}

// link writes a link to destination around the label, or only the label when the destination
// is not an absolute URL with one of linkSchemes or longer than typst accepts.
func (w *markdownWriter) link(destination string, label func()) {
	parsed, err := url.Parse(destination)
	if err != nil || !linkSchemes[strings.ToLower(parsed.Scheme)] || len(destination) > maximumLinkLength {
		label()
		return
	}
	w.nest("#link("+Quote(destination)+")[", "]", label)
}
