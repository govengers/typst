// Package oteladapter traces the compiles of a typst.Compiler with OpenTelemetry: one internal
// span per compile in the caller's trace. Spans implements typst.Observer; put it into
// typst.Options.Observers. The package depends on the OpenTelemetry API only; the service builds
// the SDK provider (github.com/govengers/telemetry/tracing).
//
// No template, data or diagnostic text reaches a span: the data is the user's, and the
// diagnostics can quote it (TRC-006).
package oteladapter

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/govengers/typst"
)

// ScopeName is the instrumentation scope of the spans.
const ScopeName = "github.com/govengers/typst/oteladapter"

// SpanName is the name of every compile span; the template's name is an attribute.
const SpanName = "typst.compile"

// The attributes the spans carry.
const (
	AttributeName        attribute.Key = "typst.name"         // typst.Document.Name, when set
	AttributeFormat      attribute.Key = "typst.format"       // pdf, svg or png
	AttributeResult      attribute.Key = "typst.result"       // a typst.Result
	AttributeOutputBytes attribute.Key = "typst.output_bytes" // the output size of a success
	AttributeErrorType   attribute.Key = "error.type"         // the typst.Result of a failed compile
)

// Options configures Spans.
type Options struct {
	// TracerProvider starts the spans; required. Pass the provider main builds, or
	// otel.GetTracerProvider() when main installed it globally.
	TracerProvider trace.TracerProvider
}

// Spans traces compiles; it implements typst.Observer and is safe for concurrent use.
type Spans struct {
	tracer trace.Tracer
}

var _ typst.Observer = (*Spans)(nil)

// New returns the observer.
func New(options *Options) (*Spans, error) {
	if options == nil || options.TracerProvider == nil {
		return nil, errors.New("oteladapter: Options.TracerProvider is required")
	}
	return &Spans{tracer: options.TracerProvider.Tracer(ScopeName)}, nil
}

// spanKey keeps the span of one Spans in the context. CompileFinished receives the context of
// the last observer, whose current span may be another observer's.
type spanKey struct{ spans *Spans }

// CompileStarted starts the compile's span and returns the context that carries it.
func (s *Spans) CompileStarted(ctx context.Context, compile *typst.Compile) context.Context {
	attributes := []attribute.KeyValue{AttributeFormat.String(string(compile.Format))}
	if compile.Name != "" {
		attributes = append(attributes, AttributeName.String(compile.Name))
	}
	ctx, span := s.tracer.Start(ctx, SpanName, trace.WithSpanKind(trace.SpanKindInternal), trace.WithAttributes(attributes...))
	return context.WithValue(ctx, spanKey{s}, span)
}

// CompileFinished records the result on the span and ends it. A failure is recorded as its
// typst.Result, never its error text.
func (s *Spans) CompileFinished(ctx context.Context, _ *typst.Compile, outcome *typst.Outcome) {
	span, ok := ctx.Value(spanKey{s}).(trace.Span)
	if !ok {
		return // a context CompileStarted of this Spans did not return
	}
	span.SetAttributes(AttributeResult.String(string(outcome.Result)))
	if outcome.Result == typst.ResultSuccess {
		span.SetAttributes(AttributeOutputBytes.Int(outcome.OutputSize))
	} else {
		span.SetAttributes(AttributeErrorType.String(string(outcome.Result)))
		span.SetStatus(codes.Error, string(outcome.Result))
	}
	span.End()
}
