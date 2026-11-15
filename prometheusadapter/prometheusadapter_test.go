package prometheusadapter_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/govengers/typst"
	"github.com/govengers/typst/prometheusadapter"
)

func TestNewValidatesOptions(t *testing.T) {
	for name, options := range map[string]*prometheusadapter.Options{
		"nil options":   nil,
		"no registerer": {Namespace: "billing"},
		"no namespace":  {Registerer: prometheus.NewRegistry()},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := prometheusadapter.New(options)
			require.Error(t, err)
		})
	}
}

func TestNewReturnsADuplicateAndRegistersNothing(t *testing.T) {
	registry := prometheus.NewRegistry()
	registry.MustRegister(prometheus.NewCounter(prometheus.CounterOpts{Namespace: "billing", Subsystem: "typst", Name: "output_bytes", Help: "Taken."}))

	_, err := prometheusadapter.New(&prometheusadapter.Options{Registerer: registry, Namespace: "billing"})
	require.ErrorContains(t, err, "register the billing typst metrics")

	families, err := registry.Gather()
	require.NoError(t, err)
	require.Len(t, families, 1, "only the collector registered before")
}

func TestCompilesAreCounted(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics, err := prometheusadapter.New(&prometheusadapter.Options{Registerer: registry, Namespace: "billing"})
	require.NoError(t, err)

	invoice := &typst.Compile{Name: "invoice", Format: typst.FormatPDF}
	ctx := metrics.CompileStarted(t.Context(), invoice)
	require.Equal(t, t.Context(), ctx)
	metrics.CompileFinished(ctx, invoice, &typst.Outcome{Result: typst.ResultSuccess, Duration: 80 * time.Millisecond, OutputSize: 20_000})
	metrics.CompileFinished(context.Background(), invoice, &typst.Outcome{Result: typst.ResultTimeout, Duration: 30 * time.Second, Err: typst.ErrTimeout})
	thumbnail := &typst.Compile{Format: typst.FormatPNG}
	metrics.CompileFinished(context.Background(), thumbnail, &typst.Outcome{Result: typst.ResultFailure, Err: errors.New("boom")})

	require.NoError(t, testutil.GatherAndCompare(registry, strings.NewReader(`
# HELP billing_typst_compiles_total Typst compiles by template name, format and result.
# TYPE billing_typst_compiles_total counter
billing_typst_compiles_total{format="pdf",name="invoice",result="success"} 1
billing_typst_compiles_total{format="pdf",name="invoice",result="timeout"} 1
billing_typst_compiles_total{format="png",name="",result="failure"} 1
`), "billing_typst_compiles_total"))
	durations, err := testutil.GatherAndCount(registry, "billing_typst_compile_duration_seconds")
	require.NoError(t, err)
	require.Equal(t, 2, durations, "one series per name and format")
	require.NoError(t, testutil.GatherAndCompare(registry, strings.NewReader(`
# HELP billing_typst_output_bytes Size of the output of successful typst compiles by format.
# TYPE billing_typst_output_bytes histogram
billing_typst_output_bytes_bucket{format="pdf",le="4096"} 0
billing_typst_output_bytes_bucket{format="pdf",le="16384"} 0
billing_typst_output_bytes_bucket{format="pdf",le="65536"} 1
billing_typst_output_bytes_bucket{format="pdf",le="262144"} 1
billing_typst_output_bytes_bucket{format="pdf",le="1.048576e+06"} 1
billing_typst_output_bytes_bucket{format="pdf",le="4.194304e+06"} 1
billing_typst_output_bytes_bucket{format="pdf",le="1.6777216e+07"} 1
billing_typst_output_bytes_bucket{format="pdf",le="6.7108864e+07"} 1
billing_typst_output_bytes_bucket{format="pdf",le="+Inf"} 1
billing_typst_output_bytes_sum{format="pdf"} 20000
billing_typst_output_bytes_count{format="pdf"} 1
`), "billing_typst_output_bytes"))
}
