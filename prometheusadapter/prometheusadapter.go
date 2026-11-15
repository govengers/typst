// Package prometheusadapter exports the compiles of a typst.Compiler as Prometheus metrics on a
// registry the caller passes in (LIB-011). Metrics implements typst.Observer; put it into
// typst.Options.Observers.
package prometheusadapter

import (
	"context"
	"errors"
	"fmt"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/govengers/typst"
)

const subsystem = "typst"

var (
	// durationBuckets reach from 25 ms to about 100 s: a one-page receipt to a long report.
	durationBuckets = prometheus.ExponentialBuckets(0.025, 2.5, 10)
	// sizeBuckets reach from 4 KiB to 64 MiB, the default output cap.
	sizeBuckets = prometheus.ExponentialBuckets(4<<10, 4, 8)
)

// Options configures Metrics.
type Options struct {
	Registerer prometheus.Registerer // required; the caller's registry, never the default one
	Namespace  string                // required: the service name, e.g. "billing" for billing_typst_compiles_total
}

// Metrics records compiles; it implements typst.Observer and is safe for concurrent use. The
// name label holds typst.Document.Name, which the application sets from its own constants, so
// its values form a closed set.
//
//	<namespace>_typst_compiles_total{name,format,result}
//	<namespace>_typst_compile_duration_seconds{name,format}
//	<namespace>_typst_output_bytes{format}
type Metrics struct {
	compiles    *prometheus.CounterVec
	durations   *prometheus.HistogramVec
	outputSizes *prometheus.HistogramVec
}

var _ typst.Observer = (*Metrics)(nil)

// New registers the metrics on options.Registerer, all or none; a failed registration, a
// duplicate for example, is returned as an error.
func New(options *Options) (*Metrics, error) {
	if options == nil || options.Registerer == nil {
		return nil, errors.New("prometheusadapter: Registerer is required")
	}
	if options.Namespace == "" {
		return nil, errors.New("prometheusadapter: Namespace is required")
	}
	metrics := &Metrics{
		compiles: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: options.Namespace, Subsystem: subsystem, Name: "compiles_total",
			Help: "Typst compiles by template name, format and result.",
		}, []string{"name", "format", "result"}),
		durations: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: options.Namespace, Subsystem: subsystem, Name: "compile_duration_seconds",
			Help: "Typst compile duration, process start to output, by template name and format.", Buckets: durationBuckets,
		}, []string{"name", "format"}),
		outputSizes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: options.Namespace, Subsystem: subsystem, Name: "output_bytes",
			Help: "Size of the output of successful typst compiles by format.", Buckets: sizeBuckets,
		}, []string{"format"}),
	}
	collectors := []prometheus.Collector{metrics.compiles, metrics.durations, metrics.outputSizes}
	for index, collector := range collectors {
		if err := options.Registerer.Register(collector); err != nil {
			for _, registered := range collectors[:index] {
				options.Registerer.Unregister(registered)
			}
			return nil, fmt.Errorf("prometheusadapter: register the %s typst metrics: %v", options.Namespace, err) //nolint:errorlint // LIB-010: client_golang errors are not part of the API
		}
	}
	return metrics, nil
}

// CompileStarted records nothing.
func (m *Metrics) CompileStarted(ctx context.Context, _ *typst.Compile) context.Context {
	return ctx
}

// CompileFinished counts the compile and its duration, and the output size of a success.
func (m *Metrics) CompileFinished(_ context.Context, compile *typst.Compile, outcome *typst.Outcome) {
	format := string(compile.Format)
	m.compiles.WithLabelValues(compile.Name, format, string(outcome.Result)).Inc()
	m.durations.WithLabelValues(compile.Name, format).Observe(outcome.Duration.Seconds())
	if outcome.Result == typst.ResultSuccess {
		m.outputSizes.WithLabelValues(format).Observe(float64(outcome.OutputSize))
	}
}
