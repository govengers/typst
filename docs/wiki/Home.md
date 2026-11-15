# govengers/typst

Server-side PDF, SVG and PNG from Typst templates. The service computes every value; the template only lays them out. This page covers the template contract, the wiring in a service, shipping the binary and the gotchas. Decisions and their reasons: [design.md](../design.md).

## The template contract

| In the render directory | Comes from | Read in the template with |
|-------------------------|------------|---------------------------|
| `/main.typ` | `Document.Source` | (it is the template) |
| `/data.json` | `Document.Data`, encoded with `encoding/json`; absent when `Data` is nil | `#let data = json("/data.json")` |
| `/images/logo.svg`, ... | `Document.Files` by slash path | `#image("images/logo.svg")`, `#include "parts/footer.typ"` |

- Paths in a template are relative to the file that names them, or absolute from the project root (`/data.json`). Nothing outside the root is readable.
- Place values with `#data.field`, loop with `#for line in data.lines [...]`. A string from the data is content, so `*`, `#` or `_` in it render as they are.
- Compute in Go, not in the template: totals, taxes, formatted dates and amounts go into the data as strings. The template stays a pure renderer that a designer can change without touching business rules.
- User Markdown goes through `typst.FromMarkdown` in Go and `#eval(data.description, mode: "markup")` in the template. `eval` of anything else that came from a user is a code injection.
- `datetime.today()` is the render host's date. Pass the document date in the data.

## Wiring in a service

```go
// main: once
compiler, err := typst.New(&typst.Options{
    Version:   "0.15.1",
    FontPaths: []string{"/app/fonts"},
    Observers: []typst.Observer{typstMetrics, typstSpans},
})

// in a govengers/jobs handler: render, store, reference
pdf, err := compiler.Compile(ctx, &typst.Document{Name: "task-report", Source: templates.TaskReport, Data: report})
var compileError *typst.CompileError
switch {
case errors.As(err, &compileError):
    return jobs.Permanent(err) // the template refuses this data; a retry renders the same
case errors.Is(err, typst.ErrInvalidDocument), errors.Is(err, typst.ErrOutputTooLarge):
    return jobs.Permanent(err)
case err != nil:
    return err // timeout or a failed process: retry with backoff
}
```

- Render in a job, not in the request: each compile is a process that uses every core for its run. The queue's concurrency bounds how many run at once.
- `Document.Name` is a metric label and a span attribute: one constant per template.
- A `*CompileError` names the template position (`main.typ:12:3: error: ...`). Its `Diagnostics` can quote data the template used, so record it on the wide event like the data itself, not in a shared channel.
- Templates live next to the code that fills them (`//go:embed templates/*.typ`); a template change ships with the binary, never through a database row that a deploy does not update.

## Shipping typst in the image

The runtime image needs the binary. Copy the static musl release in a build stage and pin its checksum:

```dockerfile
FROM alpine:3 AS typst
ARG TARGETARCH
ARG TYPST_VERSION=0.15.1
RUN apk add --no-cache curl xz \
 && case "$TARGETARCH" in \
      amd64) asset=typst-x86_64-unknown-linux-musl  sha256=a6d077d0a95eed5a2eba715b2dae06be954f624ccbf85758a03f389ded33118c ;; \
      arm64) asset=typst-aarch64-unknown-linux-musl sha256=5aa8d74a3d906e60ea12a66ac2f37f8eef1b14cbad7182a745e393a10c23dcee ;; \
    esac \
 && curl -fsSL -o /tmp/typst.tar.xz "https://github.com/typst/typst/releases/download/v${TYPST_VERSION}/${asset}.tar.xz" \
 && echo "${sha256}  /tmp/typst.tar.xz" | sha256sum -c - \
 && tar -xJf /tmp/typst.tar.xz -C /tmp && mv "/tmp/${asset}/typst" /usr/local/bin/typst

FROM gcr.io/distroless/static-debian12
COPY --from=typst /usr/local/bin/typst /usr/local/bin/typst
COPY fonts /app/fonts
```

Set `Options.Version` to the same release: a binary that is missing, of the wrong architecture or another version fails `typst.New` at startup, not the first render. The `Binary` option takes a path when the binary is not on `PATH`.

## Packages and fonts

- **Packages** are never downloaded at render time. Vendor each one into a directory laid out `<namespace>/<name>/<version>` (the content of `https://packages.typst.org/preview/<name>-<version>.tar.gz`) and set `Options.PackagePath`; `#import "@preview/<name>:<version>"` and `@local/...` resolve from there. An import that is not vendored fails with "failed to download package".
- **Fonts**: system fonts are off, so a render is the same on every host. Put the font files into a directory of `Options.FontPaths` (searched recursively; it must exist). typst's embedded fonts (Libertinus Serif, New Computer Modern, DejaVu Sans Mono) are always there. Fonts in `Document.Files` are not found.
- **Variable fonts** show only one instance in typst 0.14 and 0.15: weight and stretch do nothing. Ship the static TTFs of the family instead.

## Gotchas

- An SVG or PNG compile renders exactly one page (`Document.Page`, the first by default); call `Compile` per page for more. A page beyond the document is `ErrPageNotFound`. `Page` is refused for a PDF, which always holds every page.
- `Compile` blocks for the whole render. Pass a context with the job's deadline; the caller's cancellation kills the process and returns the context's error, `Options.Timeout` returns `ErrTimeout`.
- The render directory lives in `os.TempDir()` unless `Options.TemporaryDirectory` says otherwise. In a read-only container, mount an `emptyDir` and point the option at it.
- `CompileError.Error()` shows only the first error line; read `Diagnostics` for every message and the warnings.
