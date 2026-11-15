package typst

import (
	"net/url"
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
// lists, bare links). goldmark's parser is safe for concurrent use.
var markdownParser = goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()

// linkSchemes are the URL schemes FromMarkdown keeps as links; any other link renders as its
// text only, so no javascript:, file: or data: target reaches a PDF.
var linkSchemes = map[string]bool{"http": true, "https": true, "mailto": true}

// FromMarkdown converts Markdown written by a user (CommonMark with GitHub tables,
// strikethrough, task lists and bare links) into Typst markup. Every character of the input
// ends up as escaped text or inside a string literal; the structure is built only from function
// calls this converter emits (#heading, #list, #enum, #table, #raw, #link, ...), so the input
// cannot reach Typst code. Raw HTML stays visible as text, images render as their alternative
// text, and links keep only http, https and mailto targets.
//
// Pass the result to the template as data and evaluate it there:
//
//	#eval(data.description, mode: "markup")
func FromMarkdown(markdown string) string {
	source := []byte(strings.ToValidUTF8(markdown, "\uFFFD"))
	writer := &markdownWriter{source: source}
	writer.blocks(markdownParser.Parse(text.NewReader(source)), "\n\n")
	return writer.output.String()
}

type markdownWriter struct {
	source []byte
	output strings.Builder
}

func (w *markdownWriter) write(parts ...string) {
	for _, part := range parts {
		w.output.WriteString(part)
	}
}

// blocks writes the block children of parent, separated by separator.
func (w *markdownWriter) blocks(parent ast.Node, separator string) {
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
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
		w.write("#heading(level: ", strconv.Itoa(node.Level), ")[")
		w.inlines(node)
		w.write("]")
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
		w.write("#quote(block: true)[")
		w.blocks(node, "\n\n")
		w.write("]")
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
	if list.IsOrdered() {
		w.write("#enum(start: ", strconv.Itoa(list.Start), ", ")
	} else {
		w.write("#list(")
	}
	// A loose list separates its items' paragraphs; a tight item holds one text block and
	// maybe a nested list, which needs no paragraph break.
	separator := "\n"
	if !list.IsTight {
		w.write("tight: false, ")
		separator = "\n\n"
	}
	for item := list.FirstChild(); item != nil; item = item.NextSibling() {
		if item != list.FirstChild() {
			w.write(", ")
		}
		w.write("[")
		w.blocks(item, separator)
		w.write("]")
	}
	w.write(")")
}

func (w *markdownWriter) table(table *extensionast.Table) {
	columns := len(table.Alignments)
	w.write("#table(columns: ", strconv.Itoa(columns), ", align: (")
	for index, alignment := range table.Alignments {
		if index > 0 {
			w.write(", ")
		}
		w.write(typstAlignment(alignment))
	}
	w.write(")")
	for row := table.FirstChild(); row != nil; row = row.NextSibling() {
		_, header := row.(*extensionast.TableHeader)
		if header {
			w.write(", table.header(")
		}
		cell := row.FirstChild()
		for index := range columns {
			if index > 0 || !header {
				w.write(", ")
			}
			w.write("[")
			// GFM fills a short row with empty cells and drops the cells beyond the header's.
			if cell != nil {
				w.inlines(cell)
				cell = cell.NextSibling()
			}
			w.write("]")
		}
		if header {
			w.write(")")
		}
	}
	w.write(")")
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
	for child := parent.FirstChild(); child != nil; child = child.NextSibling() {
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
		w.write(function)
		w.inlines(node)
		w.write("]")
	case *extensionast.Strikethrough:
		w.write("#strike[")
		w.inlines(node)
		w.write("]")
	case *ast.Link:
		w.link(string(node.Destination), func() { w.inlines(node) })
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
		value = util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(value)))
	}
	w.write(Escape(string(value)))
	switch {
	case node.HardLineBreak():
		w.write("#linebreak()\n")
	case node.SoftLineBreak():
		w.write("\n")
	}
}

// codeSpan returns the text of an inline code span; a line break inside it is a space.
func (w *markdownWriter) codeSpan(node *ast.CodeSpan) string {
	var content strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if text, ok := child.(*ast.Text); ok {
			value := text.Segment.Value(w.source)
			content.Write(value)
			if text.SoftLineBreak() || text.HardLineBreak() {
				content.WriteByte(' ')
			}
		}
	}
	return content.String()
}

// link writes a link to destination around the label, or only the label when the destination
// is not an absolute URL with one of linkSchemes.
func (w *markdownWriter) link(destination string, label func()) {
	parsed, err := url.Parse(destination)
	if err != nil || !linkSchemes[strings.ToLower(parsed.Scheme)] {
		label()
		return
	}
	w.write("#link(", Quote(destination), ")[")
	label()
	w.write("]")
}
