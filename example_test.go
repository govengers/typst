package typst_test

import (
	"context"
	"fmt"
	"os"

	"github.com/govengers/typst"
)

// A template reads its data from data.json, so user text in the data is never markup.
func Example() {
	compiler, err := typst.New(&typst.Options{Version: "0.15.1"})
	if err != nil {
		fmt.Println(err) // typst is missing or another version
		return
	}
	pdf, err := compiler.Compile(context.Background(), &typst.Document{
		Name: "task-report",
		Source: []byte(`#let data = json("/data.json")
= #data.title
#eval(data.description, mode: "markup")`),
		Data: map[string]string{
			"title":       "Fix the #login [page]",
			"description": typst.FromMarkdown("Steps:\n\n1. open *Settings*\n2. click `Save`"),
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	if err := os.WriteFile("task-report.pdf", pdf, 0o600); err != nil {
		fmt.Println(err)
	}
}

func ExampleEscape() {
	fmt.Println(typst.Escape("50% off #today, *really*"))
	// Output: 50\% off \#today\, \*really\*
}

func ExampleQuote() {
	fmt.Println("#text(fill: blue, " + typst.Quote(`say "hi"`) + ")")
	// Output: #text(fill: blue, "say \"hi\"")
}

func ExampleFromMarkdown() {
	fmt.Println(typst.FromMarkdown("# Notes\n\n- **bold** and [a link](https://example.com)\n- #not-code"))
	// Output:
	// #heading(level: 1)[Notes]
	//
	// #list([#strong[bold] and #link("https://example.com")[a link]], [\#not\-code])
}
