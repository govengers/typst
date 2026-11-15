package typst_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/govengers/typst"
)

func TestFromMarkdown(t *testing.T) {
	cases := []struct {
		name     string
		markdown string
		want     string
	}{
		{"empty", "", ""},
		{"paragraphs", "one\ntwo\n\nthree", "one\ntwo\n\nthree"},
		{"text is escaped", "#import x: *y* = $z$ <l> @r // c", `\#import x\: #emph[y] \= \$z\$ \<l\> \@r \/\/ c`},
		{"markdown escapes and entities resolve", `\*not emph\* &amp; &copy; &#35;`, `\*not emph\* \& © \#`},
		{"hard line break", "one  \ntwo\\\nthree", "one#linebreak()\ntwo#linebreak()\nthree"},
		{"headings", "# One\n\n### Three *x*", "#heading(level: 1)[One]\n\n#heading(level: 3)[Three #emph[x]]"},
		{"emphasis", "*a* **b** ***c*** ~~d~~", "#emph[a] #strong[b] #emph[#strong[c]] #strike[d]"},
		{"inline code", "use `a \"b\" \\ c` here", `use #raw("a \"b\" \\ c") here`},
		{"fenced code", "```go\nfunc main() {}\n\t]) #x\n```", "#raw(\"func main() {}\\n\\t]) #x\", block: true, lang: \"go\")"},
		{"fenced code without language", "```\nplain\n```", `#raw("plain", block: true)`},
		{"indented code", "    code ]\n    more", "#raw(\"code ]\\nmore\", block: true)"},
		{"thematic break", "a\n\n---\n\nb", "a\n\n#line(length: 100%)\n\nb"},
		{"block quote", "> quoted\n>\n> # inner", "#quote(block: true)[quoted\n\n#heading(level: 1)[inner]]"},
		{"bullet list", "- a\n- b\n  - c", "#list([a], [b\n#list([c])])"},
		{"loose list", "- a\n\n- b", "#list(tight: false, [a], [b])"},
		{"ordered list", "3. a\n4. b", "#enum(start: 3, [a], [b])"},
		{"task list", "- [x] done\n- [ ] open", `#list([\[x\] done], [\[ \] open])`},
		{"link", `[a *b*](https://example.com/x?y=1&z="q")`, `#link("https://example.com/x?y=1&z=\"q\"")[a #emph[b]]`},
		{"mailto link", "[mail](mailto:a@example.com)", `#link("mailto:a@example.com")[mail]`},
		{"javascript link keeps only its text", "[click](javascript:alert(1))", "click"},
		{"relative link keeps only its text", "[file](../etc/passwd)", "file"},
		{"file link keeps only its text", "[f](FILE:///etc/passwd)", "f"},
		{"bare url", "see https://example.com/a", `see #link("https://example.com/a")[https\:\/\/example\.com\/a]`},
		{"autolink email", "<a@example.com>", `#link("mailto:a@example.com")[a\@example\.com]`},
		{"autolink url", "<https://example.com>", `#link("https://example.com")[https\:\/\/example\.com]`},
		{"image is its alternative text", "![a *logo*](/etc/passwd)", "a #emph[logo]"},
		{"inline html stays text", "a <b onclick=\"x\">b</b>", `a \<b onclick\=\"x\"\>b\<\/b\>`},
		{"html block stays text", "<div>\n#x\n</div>", `\<div\>` + "\n" + `\#x` + "\n" + `\<\/div\>`},
		{"html comment block", "<!-- a -->\n\nb", `\<\!\-\- a \-\-\>` + "\n\nb"},
		{"table", "| a | b | c | d |\n|:--|:-:|--:|---|\n| 1 | *2* |\n| w | x | y | z | extra |", "#table(columns: 4, align: (left, center, right, auto), table.header([a], [b], [c], [d]), [1], [#emph[2]], [], [], [w], [x], [y], [z])"},
		{"invalid utf-8", "a \xff b", "a \uFFFD b"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			output := typst.FromMarkdown(testCase.markdown)
			require.Equal(t, testCase.want, output)
			checker := &markupChecker{input: output}
			require.NoError(t, checker.check())
		})
	}
}

func FuzzFromMarkdown(f *testing.F) {
	for _, seed := range []string{
		"# h *e* **s** `c` [l](https://x.y) ![i](z)",
		"- a\n  - b\n\n1. c\n\n> q\n\n```go\nx]\n```\n\n| a | b |\n|---|---|\n| c |",
		"<div>#x</div> <span>]</span> &#35; \\#",
		"~~s~~ https://bare.example <a@b.c> - [x] t",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, markdown string) {
		output := typst.FromMarkdown(markdown)
		checker := &markupChecker{input: output}
		require.NoError(t, checker.check(), "FromMarkdown(%q)", markdown)
	})
}
