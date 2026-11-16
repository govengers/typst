//go:build e2e

// Package e2e runs the compiler against the pinned typst release that `just install-typst`
// puts into sandbox/typst; `just test-e2e` passes its path in TYPST_BINARY.
package e2e

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/govengers/typst"
)

// version is the release the justfile installs.
const version = "0.15.1"

//go:embed plain.typ
var plainTemplate []byte

func newCompiler(t *testing.T, options typst.Options) *typst.Compiler {
	t.Helper()
	options.Binary = os.Getenv("TYPST_BINARY")
	require.NotEmpty(t, options.Binary, "TYPST_BINARY names the typst binary; run the suite with just test-e2e")
	options.Version = version
	compiler, err := typst.New(&options)
	require.NoError(t, err)
	return compiler
}

func TestRendersEveryFormat(t *testing.T) {
	compiler := newCompiler(t, typst.Options{})
	require.Equal(t, version, compiler.Version())
	source := []byte(`#let data = json("/data.json")
#set page(width: 10cm, height: 6cm)
= Invoice #data.number
#image("images/dot.svg", width: 1cm)
#pagebreak()
Second page`)
	document := typst.Document{
		Name:   "invoice",
		Source: source,
		Data:   map[string]any{"number": "2026-0042"},
		Files:  map[string][]byte{"images/dot.svg": []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><circle cx="5" cy="5" r="5"/></svg>`)},
	}

	pdf, err := compiler.Compile(t.Context(), &document)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(pdf, []byte("%PDF-")))

	document.Format = typst.FormatSVG
	svg, err := compiler.Compile(t.Context(), &document)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(svg, []byte("<svg")))

	document.Format, document.Page, document.PixelsPerInch = typst.FormatPNG, 2, 72
	png, err := compiler.Compile(t.Context(), &document)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")))

	document.Page = 3
	_, err = compiler.Compile(t.Context(), &document)
	require.ErrorIs(t, err, typst.ErrPageNotFound)
}

func TestUserTextIsOnlyData(t *testing.T) {
	compiler := newCompiler(t, typst.Options{})
	hostile := "#panic(\"x\") ] [ *b* _e_ $m$ <l> @r // c /* c */ \\ \" `r` = h\n- l\n+ e\n/ t: d\n1. n -- ~ ... #{1+1} #read(\"/data.json\")"
	markdown := "# Title #panic(\"x\")\n\nSome **bold** and `#raw` and [link](https://example.com) and [js](javascript:alert(1)).\n\n" +
		"- one ]\n- two\n  1. nested $m$\n\n> quote <b>html</b>\n\n```typ\n#panic(\"in code\") ]\n```\n\n| a | b |\n|---|:-:|\n| #x | ]) |\n\n![alt #x](/etc/passwd)"
	cases := []map[string]string{
		{"name": "escape", "markup": typst.Escape(hostile), "expected": normalize(hostile)},
		{"name": "quote", "markup": "#" + typst.Quote(hostile), "expected": normalize(hostile)},
		{"name": "markdown", "markup": typst.FromMarkdown(markdown), "expected": "Title #panic(\"x\") Some bold and #raw and link and js. one ] two nested $m$ quote <b>html</b> #panic(\"in code\") ] a b #x ]) alt #x"},
		{"name": "markdown with invalid utf-8", "markup": typst.FromMarkdown("a \xff b"), "expected": "a � b"},
	}

	pdf, err := compiler.Compile(t.Context(), &typst.Document{Source: plainTemplate, Data: map[string]any{"cases": cases}})
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(pdf, []byte("%PDF-")))
}

func TestMarkdownStaysWithinTypstLimits(t *testing.T) {
	compiler := newCompiler(t, typst.Options{})
	link := func(length int) string {
		return "https://example.com/" + strings.Repeat("a", length-len("https://example.com/"))
	}
	inputs := map[string]string{
		"8 quotes":              strings.Repeat(">", 8) + " x",
		"9 quotes":              strings.Repeat(">", 9) + " x",
		"200 quotes":            strings.Repeat(">", 200) + " x",
		"quotes and lists":      strings.Repeat("> - ", 30) + "x",
		"40 lists":              strings.Repeat("- ", 40) + "x",
		"40 enums":              strings.Repeat("1. ", 40) + "x",
		"7 quotes and a table":  strings.Repeat("> ", 7) + "| ***~~[x](https://example.com)~~*** |\n" + strings.Repeat("> ", 7) + "|---|",
		"emphasis":              strings.Repeat("*a **b ", 60) + "x" + strings.Repeat("** a*", 60),
		"link of 8000 bytes":    "[x](" + link(8000) + ")",
		"link of 8001 bytes":    "[x](" + link(8001) + ")",
		"bare url of 8001":      link(8001),
		"repeated long target":  "[a]: " + link(7000) + "\n\n" + strings.Repeat("[a] ", 6000),
		"wide table, many rows": "|" + strings.Repeat(" a |", 1000) + "\n|" + strings.Repeat("---|", 1000) + "\n" + strings.Repeat("|x\n", 100),
	}
	markups := make([]string, 0, len(inputs))
	for _, input := range inputs {
		markups = append(markups, typst.FromMarkdown(input))
	}
	// The template's own nesting takes from the same depth budget as the converted markup.
	source := []byte(`#let data = json("/data.json")
#table(columns: 1, list(quote(block: true, box(for markup in data.markups { eval(markup, mode: "markup") }))))`)

	pdf, err := compiler.Compile(t.Context(), &typst.Document{Source: source, Data: map[string]any{"markups": markups}})
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(pdf, []byte("%PDF-")))
}

func normalize(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func TestTheTemplateCannotReadOutsideTheProject(t *testing.T) {
	compiler := newCompiler(t, typst.Options{})
	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte(`#read("/../../../../etc/passwd")`)})

	var compileError *typst.CompileError
	require.ErrorAs(t, err, &compileError)
	require.Contains(t, compileError.Diagnostics, "escape the project root")
}

func TestPackagesComeOnlyFromThePackagePath(t *testing.T) {
	compiler := newCompiler(t, typst.Options{})
	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte(`#import "@preview/cetz:0.3.4"`)})
	var compileError *typst.CompileError
	require.ErrorAs(t, err, &compileError)
	require.Contains(t, compileError.Diagnostics, "failed to download package")

	packages := t.TempDir()
	greet := filepath.Join(packages, "preview", "greet", "0.1.0")
	require.NoError(t, os.MkdirAll(greet, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(greet, "typst.toml"), []byte("[package]\nname = \"greet\"\nversion = \"0.1.0\"\nentrypoint = \"lib.typ\"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(greet, "lib.typ"), []byte("#let greet(name) = [Hello #name]\n"), 0o600))
	compiler = newCompiler(t, typst.Options{PackagePath: packages})
	pdf, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte("#import \"@preview/greet:0.1.0\": greet\n#greet(\"tasks\")")})
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(pdf, []byte("%PDF-")))
}

func TestCompileErrorsNameTheTemplatePosition(t *testing.T) {
	compiler := newCompiler(t, typst.Options{})
	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte("ok\n#unknown-function()")})

	var compileError *typst.CompileError
	require.ErrorAs(t, err, &compileError)
	require.Equal(t, 1, compileError.ExitCode)
	require.Contains(t, err.Error(), "main.typ:2:1: error: unknown variable: unknown-function")
}

func TestLimits(t *testing.T) {
	compiler := newCompiler(t, typst.Options{Timeout: 100 * time.Millisecond})
	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte("#let n = 0\n#for i in range(1000000000) { n += i }\n#n")})
	require.ErrorIs(t, err, typst.ErrTimeout)

	compiler = newCompiler(t, typst.Options{MaximumOutputSize: 512})
	_, err = compiler.Compile(t.Context(), &typst.Document{Source: []byte("Hello")})
	require.True(t, errors.Is(err, typst.ErrOutputTooLarge), "a PDF is larger than 512 bytes: %v", err)
}

// warnings records the warnings of every compile.
type warnings struct{ seen []string }

func (w *warnings) CompileStarted(ctx context.Context, _ *typst.Compile) context.Context { return ctx }

func (w *warnings) CompileFinished(_ context.Context, _ *typst.Compile, outcome *typst.Outcome) {
	w.seen = append(w.seen, outcome.Warnings)
}

func TestAMissingFontIsAWarningOfASuccess(t *testing.T) {
	observer := &warnings{}
	compiler := newCompiler(t, typst.Options{Observers: []typst.Observer{observer}})
	_, err := compiler.Compile(t.Context(), &typst.Document{Source: []byte(`#set text(font: "Inter")
Hello`)})
	require.NoError(t, err)
	require.Contains(t, observer.seen[0], "main.typ:1:16: warning: unknown font family: inter")
}
