package typst

import (
	"errors"
	"strings"
)

// The errors Compile returns for the conditions a caller branches on. Match them with errors.Is;
// a failed compilation of the template is a *CompileError, matched with errors.As.
var (
	// ErrInvalidDocument reports a Document that cannot be compiled as given: no source, an
	// unknown format, a file path outside the project, data that does not encode as JSON.
	ErrInvalidDocument = errors.New("typst: invalid document")
	// ErrTimeout reports a compile that ran longer than Options.Timeout and was killed.
	ErrTimeout = errors.New("typst: compile timed out")
	// ErrOutputTooLarge reports a compile whose output grew beyond Options.MaximumOutputSize; the
	// process was killed and no output is returned.
	ErrOutputTooLarge = errors.New("typst: output exceeds the maximum size")
	// ErrPageNotFound reports an SVG or PNG compile whose Document.Page is beyond the document's
	// last page.
	ErrPageNotFound = errors.New("typst: page not found")
)

// CompileError reports that typst ran and refused the document: a syntax error in the template,
// an unknown variable, a missing file, a panic the template raised. A typst process ended by a
// signal (an OOM kill) is not a CompileError but a plain error wrapping *exec.ExitError, because
// a retry can render. Diagnostics holds what typst wrote to stderr in its short format
// (path:line:column: error: message), cut at maximumDiagnosticsSize. The diagnostics quote the
// template and can quote values the template took from the data, so treat them like the data
// when logging.
type CompileError struct {
	ExitCode    int
	Diagnostics string
}

// Error returns the first error line of the diagnostics, which names the template position.
func (e *CompileError) Error() string {
	return "typst: compile failed: " + firstError(e.Diagnostics)
}

func firstError(diagnostics string) string {
	lines := strings.Split(diagnostics, "\n")
	for _, line := range lines {
		if strings.Contains(line, "error:") {
			return strings.TrimSpace(line)
		}
	}
	if first := strings.TrimSpace(lines[0]); first != "" {
		return first
	}
	return "no diagnostics"
}
