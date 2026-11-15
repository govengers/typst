// Package typst renders PDF, SVG and PNG documents from Typst templates by running the typst
// CLI, one process per render.
//
// A Compiler is built once with New, which resolves the binary and checks its version. Each
// Compile writes the template, the extra files and the data into a fresh temporary directory,
// runs typst with that directory as the project root and returns the output read from the
// process's stdout. The data reaches the template as the JSON file DataFile, never on the
// command line:
//
//	#let data = json("/data.json")
//	= Invoice #data.number
//
// User text is only ever data. A value the template reads from the JSON file and places with
// #data.field is content, never markup, so it needs no escaping. Escape and Quote cover
// templates built as Go strings, and FromMarkdown turns user-written Markdown into Typst markup
// in which every character of the input is escaped text.
//
// The process runs with an empty environment, a project root it cannot read outside of, no
// system fonts unless Options.SystemFonts asks for them, and no network: a template that imports
// a package not present in Options.PackagePath fails instead of downloading it at render time.
//
// The package does not log (LIB-003). Every compile is reported to the Observers in the options;
// the prometheusadapter and oteladapter subpackages turn those reports into metrics and spans.
package typst
