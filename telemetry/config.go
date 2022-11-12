package telemetry

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	metricglobal "go.opentelemetry.io/otel/metric/global"
	"go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName = "github.com/Tochemey/goakt"
)

// Config encapsulates some settings for an actor
type Config struct {
	TracerProvider trace.TracerProvider
	Tracer         trace.Tracer

	MeterProvider metric.MeterProvider
	Meter         metric.Meter

	Metrics *Metrics
}

// NewConfig creates an instance of Config
func NewConfig(options ...Option) Config {
	// create a config instance
	cfg := Config{
		TracerProvider: otel.GetTracerProvider(),
		MeterProvider:  metricglobal.MeterProvider(),
	}

	// apply the various options
	for _, opt := range options {
		opt.Apply(&cfg)
	}

	// set the tracer
	cfg.Tracer = cfg.TracerProvider.Tracer(
		instrumentationName,
		trace.WithInstrumentationVersion(Version()),
	)

	// set the meter
	cfg.Meter = cfg.MeterProvider.Meter(
		instrumentationName,
		metric.WithInstrumentationVersion(Version()),
	)

	// set the metrics
	var err error
	if cfg.Metrics, err = NewMetrics(cfg.Meter); err != nil {
		otel.Handle(err)
	}
	return cfg
}
