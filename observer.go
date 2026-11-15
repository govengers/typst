package typst

import (
	"context"
	"errors"
	"time"
)

// Observer is told about every compile, for metrics and traces (LIB-012). Compile calls it from
// the caller's goroutine; implementations must be safe for concurrent use and fast. The interface
// may gain methods in minor versions, so an implementation outside this module should expect that.
type Observer interface {
	// CompileStarted is called before the process starts. The returned context is the one the
	// compile runs with and the one CompileFinished receives, so a tracer can put its span there.
	CompileStarted(ctx context.Context, compile *Compile) context.Context
	// CompileFinished reports how the compile ended.
	CompileFinished(ctx context.Context, compile *Compile, outcome *Outcome)
}

// Compile describes one compile to the Observers. It carries no document content.
type Compile struct {
	Name   string // Document.Name: the template's name, empty when the caller set none
	Format Format
}

// Result is the closed set of ways a compile ends, usable as a metric label.
type Result string

// The results an Outcome carries.
const (
	ResultSuccess        Result = "success"
	ResultCompileError   Result = "compile_error"    // a *CompileError or ErrPageNotFound
	ResultTimeout        Result = "timeout"          // ErrTimeout
	ResultOutputTooLarge Result = "output_too_large" // ErrOutputTooLarge
	ResultCancelled      Result = "cancelled"        // the caller's context ended
	ResultFailure        Result = "failure"          // the process could not run, or the render directory failed
)

// Outcome is one finished compile.
type Outcome struct {
	Result     Result
	Duration   time.Duration // from writing the render directory to removing it
	OutputSize int           // bytes returned; zero unless Result is ResultSuccess
	Err        error         // what Compile returned; nil on success
}

func resultOf(err error, cancelled bool) Result {
	var compileError *CompileError
	switch {
	case err == nil:
		return ResultSuccess
	case cancelled:
		return ResultCancelled
	case errors.As(err, &compileError), errors.Is(err, ErrPageNotFound):
		return ResultCompileError
	case errors.Is(err, ErrTimeout):
		return ResultTimeout
	case errors.Is(err, ErrOutputTooLarge):
		return ResultOutputTooLarge
	default:
		return ResultFailure
	}
}
