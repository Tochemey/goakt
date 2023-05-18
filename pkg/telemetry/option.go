package telemetry

import (
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(config *Telemetry)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(*Telemetry)

func (f OptionFunc) Apply(c *Telemetry) {
	f(c)
}

// WithTracerProvider specifies a tracer provider to use for creating a tracer.
// If none is specified, the global provider is used.
func WithTracerProvider(provider trace.TracerProvider) Option {
	return OptionFunc(func(cfg *Telemetry) {
		cfg.TracerProvider = provider
	})
}

// WithMeterProvider specifies a tracer provider to use for creating a tracer.
// If none is specified, the global provider is used.
func WithMeterProvider(provider metric.MeterProvider) Option {
	return OptionFunc(func(cfg *Telemetry) {
		cfg.MeterProvider = provider
	})
}
