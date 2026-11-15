# typst

[![CI](https://github.com/govengers/typst/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/govengers/typst/actions/workflows/ci.yml)
[![unit tests](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/govengers/typst/badges/unit-tests.json)](https://github.com/govengers/typst/actions/workflows/ci.yml)
[![e2e tests](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/govengers/typst/badges/e2e-tests.json)](test/e2e)
[![coverage](https://img.shields.io/endpoint?url=https://raw.githubusercontent.com/govengers/typst/badges/coverage.json)](https://github.com/govengers/typst/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/govengers/typst.svg)](https://pkg.go.dev/github.com/govengers/typst)
[![License](https://img.shields.io/github/license/govengers/typst)](LICENSE)

Coding agents: start at [llms.txt](llms.txt) and [docs/wiki/Home.md](docs/wiki/Home.md); the decisions are in [docs/design.md](docs/design.md).

Render PDF, SVG and PNG documents from [Typst](https://typst.app) templates in a Go service, through the `typst` CLI: one process per render in its own directory, data as a JSON file, no network, a timeout and an output cap. Plus `Escape`, `Quote` and `FromMarkdown`, so user text stays data.

| Package | What it gives you | Depends on |
|---------|-------------------|------------|
| `typst` | `Compiler` (`New`, `Compile`, `Version`), `Document`, `Escape`, `Quote`, `FromMarkdown`, `Observer`, the errors | goldmark |
| `typst/prometheusadapter` | `Metrics`, an `Observer` exporting compile counts, durations and sizes | client_golang |
| `typst/oteladapter` | `Spans`, an `Observer` tracing every compile | OpenTelemetry API |

```sh
go get github.com/govengers/typst
```

Requires Go 1.26 and a typst binary from 0.13.0 on (the suite runs 0.15.1). The library never logs.

## Render a document

The template reads its data from `data.json`. A value placed with `#data.field` is content, never markup, so user text needs no escaping:

```go
compiler, err := typst.New(&typst.Options{
    Version:   "0.15.1",                   // the release the image ships; another version fails here
    FontPaths: []string{"/app/fonts"},     // optional; system fonts are off by default
    Observers: []typst.Observer{metrics},  // optional, see below
})
if err != nil {
    return err
}

pdf, err := compiler.Compile(ctx, &typst.Document{
    Name:   "invoice", // a metric label: the application's constant, never user input
    Source: invoiceTemplate, // //go:embed templates/invoice.typ
    Data:   invoice,         // encoded with encoding/json
    Files:  map[string][]byte{"images/logo.svg": logo},
})
```

```typst
#let data = json("/data.json")
#image("images/logo.svg", width: 3cm)
= Invoice #data.number
#for line in data.lines [ - #line.description: #line.total ]
```

`Format: typst.FormatSVG` or `typst.FormatPNG` renders one page (`Page`, counted from 1, the first by default); `PixelsPerInch` sets a PNG's resolution. A PDF always holds every page.

Errors to branch on: `typst.ErrInvalidDocument` (nothing ran), `*typst.CompileError` (typst refused the template; `Diagnostics` holds its messages), `typst.ErrTimeout`, `typst.ErrOutputTooLarge`, `typst.ErrPageNotFound`, and the context's error.

## User text

- **Data file** (preferred): put the text into `Data`, place it with `#data.field`. Nothing to escape.
- **Markdown** a user wrote: `typst.FromMarkdown(text)` returns Typst markup in which every character of the input is escaped text; headings, lists, tables, code, links (http, https, mailto only) become calls. Pass it as data and evaluate it: `#eval(data.description, mode: "markup")`. Raw HTML stays visible text, images become their alternative text.
- **Templates built as Go strings**: `typst.Escape(text)` for markup, `typst.Quote(text)` for a string literal in code.

## What a render can do

Each compile runs with an empty environment, the render directory as its project root (typst refuses any read outside it), packages only from `Options.PackagePath` (an `@preview` import that is not vendored fails instead of downloading), no system fonts unless `Options.SystemFonts`, `Options.Timeout` (30 s) and `Options.MaximumOutputSize` (64 MiB). The directory is removed before `Compile` returns.

## Metrics and traces

```go
metrics, err := prometheusadapter.New(&prometheusadapter.Options{Registerer: registry, Namespace: "billing"})
spans, err := oteladapter.New(&oteladapter.Options{TracerProvider: traces.TracerProvider()})
compiler, err := typst.New(&typst.Options{Observers: []typst.Observer{metrics, spans}})
```

```
billing_typst_compiles_total{name,format,result}
billing_typst_compile_duration_seconds{name,format}
billing_typst_output_bytes{format}
```

The span `typst.compile` carries `typst.name`, `typst.format`, `typst.result` and `typst.output_bytes`; no template, data or diagnostic text.

## Shipping the binary

The image needs the typst binary at the path the options name. Copy the static musl release in a build stage, see [docs/wiki/Home.md](docs/wiki/Home.md#shipping-typst-in-the-image).

## Development

```sh
just test          # unit tests against a fake typst, about 2 s
just install-typst # the pinned release into sandbox/typst, checksum verified
just test-e2e      # the end-to-end suite against it
just check         # lint, unit, e2e, vulncheck: what CI runs
```

Apache 2.0, see [LICENSE](LICENSE).
