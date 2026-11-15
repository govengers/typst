package typst

import (
	"fmt"
	"strings"
	"unicode"
)

// asciiPunctuation holds every character Escape puts a backslash before. typst accepts the
// escape for each of them, so the set needs no update when typst adds markup syntax.
const asciiPunctuation = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

// Escape returns text as Typst markup that renders as exactly that text: every ASCII
// punctuation character gets a backslash, so no character can open a function call (#), a
// content block, a label, a reference, math, raw text, emphasis, a heading, a list, a comment or
// a shorthand such as -- or ~. Line breaks stay; a blank line is a paragraph break, as in any
// markup. Invalid UTF-8 becomes U+FFFD, which typst would otherwise reject.
//
// Use it where a template is built as a Go string. A template that reads its data from
// DataFile needs no escaping.
func Escape(text string) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	var escaped strings.Builder
	escaped.Grow(len(text) + len(text)/8)
	for _, character := range text {
		if strings.ContainsRune(asciiPunctuation, character) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(character)
	}
	return escaped.String()
}

// Quote returns text as a Typst string literal, quotes included, for code mode:
// #text(fill: red, <Quote(name)>). Backslashes, quotes and control characters are escaped;
// invalid UTF-8 becomes U+FFFD.
func Quote(text string) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	var quoted strings.Builder
	quoted.Grow(len(text) + 2)
	quoted.WriteByte('"')
	for _, character := range text {
		switch {
		case character == '\\' || character == '"':
			quoted.WriteByte('\\')
			quoted.WriteRune(character)
		case character == '\n':
			quoted.WriteString(`\n`)
		case character == '\r':
			quoted.WriteString(`\r`)
		case character == '\t':
			quoted.WriteString(`\t`)
		case unicode.IsControl(character):
			fmt.Fprintf(&quoted, `\u{%x}`, character)
		default:
			quoted.WriteRune(character)
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}
