package oteladapter_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/govengers/typst"
	"github.com/govengers/typst/oteladapter"
)

func TestNewRequiresATracerProvider(t *testing.T) {
	_, err := oteladapter.New(nil)
	require.Error(t, err)
	_, err = oteladapter.New(&oteladapter.Options{})
	require.Error(t, err)
}

func newSpans(t *testing.T) (*oteladapter.Spans, *tracetest.SpanRecorder, trace.Tracer) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	spans, err := oteladapter.New(&oteladapter.Options{TracerProvider: provider})
	require.NoError(t, err)
	return spans, recorder, provider.Tracer("test")
}

func attributes(span sdktrace.ReadOnlySpan) map[string]string {
	values := map[string]string{}
	for _, recorded := range span.Attributes() {
		values[string(recorded.Key)] = recorded.Value.String()
	}
	return values
}

func TestACompileIsASpanInTheCallersTrace(t *testing.T) {
	spans, recorder, tracer := newSpans(t)
	ctx, request := tracer.Start(t.Context(), "GET /invoices/{id}.pdf")
	compile := &typst.Compile{Name: "invoice", Format: typst.FormatPDF}

	compileContext := spans.CompileStarted(ctx, compile)
	require.NotEqual(t, trace.SpanContextFromContext(ctx).SpanID(), trace.SpanContextFromContext(compileContext).SpanID())
	spans.CompileFinished(compileContext, compile, &typst.Outcome{Result: typst.ResultSuccess, OutputSize: 1234})
	request.End()

	ended := recorder.Ended()
	require.Len(t, ended, 2)
	span := ended[0]
	require.Equal(t, oteladapter.SpanName, span.Name())
	require.Equal(t, trace.SpanKindInternal, span.SpanKind())
	require.Equal(t, request.SpanContext().SpanID(), span.Parent().SpanID())
	require.Equal(t, map[string]string{"typst.name": "invoice", "typst.format": "pdf", "typst.result": "success", "typst.output_bytes": "1234"}, attributes(span))
	require.Equal(t, codes.Unset, span.Status().Code)
}

func TestAFailedCompileIsAnErrorWithoutItsText(t *testing.T) {
	spans, recorder, _ := newSpans(t)
	compile := &typst.Compile{Format: typst.FormatPNG}

	ctx := spans.CompileStarted(t.Context(), compile)
	spans.CompileFinished(ctx, compile, &typst.Outcome{
		Result: typst.ResultCompileError,
		Err:    &typst.CompileError{ExitCode: 1, Diagnostics: "main.typ:1:1: error: panicked with: \"jane@example.com\""},
	})

	span := recorder.Ended()[0]
	require.Equal(t, map[string]string{"typst.format": "png", "typst.result": "compile_error", "error.type": "compile_error"}, attributes(span))
	require.Equal(t, codes.Error, span.Status().Code)
	require.Equal(t, "compile_error", span.Status().Description)
	require.Empty(t, span.Events(), "no recorded error text")
}
