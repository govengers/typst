package typst_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/govengers/typst"
)

const asciiPunctuation = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

func TestEscape(t *testing.T) {
	cases := map[string]string{
		"":                          "",
		"plain words 123 äöü 漢字":    "plain words 123 äöü 漢字",
		"#import \"x\": *":          `\#import \"x\"\: \*`,
		"= heading\n- list\n1. one": "\\= heading\n\\- list\n1\\. one",
		"*bold* _emph_ `raw` $m$":   "\\*bold\\* \\_emph\\_ \\`raw\\` \\$m\\$",
		"<label> @ref // c /* c */": `\<label\> \@ref \/\/ c \/\* c \*\/`,
		"a -- b --- c ~ d ...":      `a \-\- b \-\-\- c \~ d \.\.\.`,
		`back\slash \u{41}`:         `back\\slash \\u\{41\}`,
		"[content] #{code}":         `\[content\] \#\{code\}`,
		"bad \xff utf8":             "bad \uFFFD utf8",
	}
	for input, want := range cases {
		require.Equal(t, want, typst.Escape(input), "Escape(%q)", input)
	}
}

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"":                 `""`,
		"plain äöü":        `"plain äöü"`,
		`say "hi" \ there`: `"say \"hi\" \\ there"`,
		"tab\tline\ncr\r":  `"tab\tline\ncr\r"`,
		"nul\x00 bell\a":   `"nul\u{0} bell\u{7}"`,
		"bad \xff":         "\"bad \uFFFD\"",
		"#code [x] $m$":    `"#code [x] $m$"`,
	}
	for input, want := range cases {
		require.Equal(t, want, typst.Quote(input), "Quote(%q)", input)
	}
}

func FuzzEscape(f *testing.F) {
	for _, seed := range []string{"", "#x", "\\", "a\n\nb", "\xff", asciiPunctuation} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		escaped := typst.Escape(text)
		checker := &markupChecker{input: escaped}
		require.NoError(t, checker.check(), "Escape(%q) = %q", text, escaped)
		require.Equal(t, strings.ToValidUTF8(text, "\uFFFD"), unescape(escaped))
	})
}

func FuzzQuote(f *testing.F) {
	for _, seed := range []string{"", `"`, `\`, "a\nb\x00", "\xff", asciiPunctuation} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		quoted := typst.Quote(text)
		checker := &markupChecker{input: quoted}
		decoded, err := checker.stringLiteral()
		require.NoError(t, err, "Quote(%q) = %q", text, quoted)
		require.Equal(t, len(quoted), checker.position, "Quote(%q) = %q ends early", text, quoted)
		require.Equal(t, strings.ToValidUTF8(text, "\uFFFD"), decoded)
	})
}

// unescape reverses Escape.
func unescape(escaped string) string {
	var text strings.Builder
	for index := 0; index < len(escaped); index++ {
		if escaped[index] == '\\' {
			index++
		}
		text.WriteByte(escaped[index])
	}
	return text.String()
}

// markupChecker parses the subset of Typst that Escape, Quote and FromMarkdown produce and
// fails on anything else: markup in which every ASCII punctuation character is escaped, except
// the # of a call to one of markupFunctions and the ] that closes a content block. Fuzzing the
// converters against it shows that no input reaches code.
type markupChecker struct {
	input    string
	position int
}

// markupFunctions are the calls FromMarkdown emits; markupValues the identifiers among their
// arguments.
var (
	markupFunctions = map[string]bool{
		"heading": true, "line": true, "raw": true, "quote": true, "list": true, "enum": true, "table": true,
		"table.header": true, "emph": true, "strong": true, "strike": true, "link": true, "linebreak": true,
	}
	markupValues = map[string]bool{"true": true, "false": true, "left": true, "right": true, "center": true, "auto": true}
)

func (c *markupChecker) check() error {
	if err := c.markup(false); err != nil {
		return err
	}
	if c.position != len(c.input) {
		return c.fail("unexpected ]")
	}
	return nil
}

func (c *markupChecker) fail(format string, arguments ...any) error {
	return fmt.Errorf("at %d of %q: %s", c.position, c.input, fmt.Sprintf(format, arguments...))
}

func (c *markupChecker) peek() byte {
	if c.position < len(c.input) {
		return c.input[c.position]
	}
	return 0
}

func (c *markupChecker) expect(literal string) error {
	if !strings.HasPrefix(c.input[c.position:], literal) {
		return c.fail("want %q", literal)
	}
	c.position += len(literal)
	return nil
}

// markup reads markup up to the end of the input, or up to the ] that closes a content block.
func (c *markupChecker) markup(inBlock bool) error {
	for c.position < len(c.input) {
		character, size := utf8.DecodeRuneInString(c.input[c.position:])
		switch {
		case character == '\\':
			if c.position+1 >= len(c.input) || !strings.ContainsRune(asciiPunctuation, rune(c.input[c.position+1])) {
				return c.fail("backslash before no punctuation")
			}
			c.position += 2
		case character == '#':
			c.position++
			if err := c.call(); err != nil {
				return err
			}
		case character == ']' && inBlock:
			return nil
		case strings.ContainsRune(asciiPunctuation, character):
			return c.fail("unescaped %q", character)
		case character == utf8.RuneError && size == 1:
			return c.fail("invalid UTF-8")
		default:
			c.position += size
		}
	}
	if inBlock {
		return c.fail("unclosed content block")
	}
	return nil
}

func (c *markupChecker) identifier() string {
	start := c.position
	for c.position < len(c.input) {
		character := c.input[c.position]
		if character != '.' && (character < 'a' || character > 'z') {
			break
		}
		c.position++
	}
	return c.input[start:c.position]
}

// call reads a call after its #: the function, its arguments and its trailing content blocks.
func (c *markupChecker) call() error {
	if name := c.identifier(); !markupFunctions[name] {
		return c.fail("call of %q", name)
	}
	if c.peek() == '(' {
		if err := c.arguments(); err != nil {
			return err
		}
	}
	for c.peek() == '[' {
		if err := c.contentBlock(); err != nil {
			return err
		}
	}
	return nil
}

func (c *markupChecker) contentBlock() error {
	c.position++
	if err := c.markup(true); err != nil {
		return err
	}
	return c.expect("]")
}

// arguments reads (name: value, value, ...), also as a tuple of values.
func (c *markupChecker) arguments() error {
	if err := c.expect("("); err != nil {
		return err
	}
	for c.peek() != ')' {
		if err := c.argument(); err != nil {
			return err
		}
		if c.peek() == ',' {
			if err := c.expect(", "); err != nil {
				return err
			}
		} else if c.peek() != ')' {
			return c.fail("want , or )")
		}
	}
	c.position++
	return nil
}

func (c *markupChecker) argument() error {
	start := c.position
	if name := c.identifier(); name != "" && c.peek() == ':' {
		if err := c.expect(": "); err != nil {
			return err
		}
	} else {
		c.position = start
	}
	return c.value()
}

func (c *markupChecker) value() error {
	switch character := c.peek(); {
	case character == '"':
		_, err := c.stringLiteral()
		return err
	case character == '[':
		return c.contentBlock()
	case character == '(':
		return c.arguments()
	case character >= '0' && character <= '9':
		for character := c.peek(); character >= '0' && character <= '9'; character = c.peek() {
			c.position++
		}
		if c.peek() == '%' {
			c.position++
		}
		return nil
	}
	name := c.identifier()
	switch {
	case markupValues[name]:
		return nil
	case markupFunctions[name]:
		return c.arguments()
	}
	return c.fail("value %q", name)
}

// stringLiteral reads a string literal and returns its value.
func (c *markupChecker) stringLiteral() (string, error) {
	if err := c.expect(`"`); err != nil {
		return "", err
	}
	var value strings.Builder
	for c.position < len(c.input) {
		character, size := utf8.DecodeRuneInString(c.input[c.position:])
		c.position += size
		switch {
		case character == '"':
			return value.String(), nil
		case character == '\\':
			decoded, err := c.stringEscape()
			if err != nil {
				return "", err
			}
			value.WriteRune(decoded)
		case character < ' ' || character == utf8.RuneError && size == 1:
			return "", c.fail("raw control character or invalid UTF-8 in a string")
		default:
			value.WriteRune(character)
		}
	}
	return "", c.fail("unclosed string")
}

func (c *markupChecker) stringEscape() (rune, error) {
	escapes := map[byte]rune{'\\': '\\', '"': '"', 'n': '\n', 'r': '\r', 't': '\t'}
	character := c.peek()
	c.position++
	if decoded, found := escapes[character]; found {
		return decoded, nil
	}
	if character != 'u' || c.peek() != '{' {
		return 0, c.fail("unknown string escape")
	}
	end := strings.IndexByte(c.input[c.position:], '}')
	if end < 0 {
		return 0, c.fail("unclosed unicode escape")
	}
	code, err := strconv.ParseInt(c.input[c.position+1:c.position+end], 16, 32)
	if err != nil {
		return 0, c.fail("unicode escape: %v", err)
	}
	c.position += end + 1
	return rune(code), nil
}
