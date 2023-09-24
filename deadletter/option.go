package deadletter

import (
	"time"

	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/telemetry"
)

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(stream *Stream)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(stream *Stream)

func (f OptionFunc) Apply(stream *Stream) {
	f(stream)
}

// WithLogger sets the custom log
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(stream *Stream) {
		stream.logger = logger
	})
}

// WithTelemetry sets the custom telemetry
func WithTelemetry(telemetry *telemetry.Telemetry) Option {
	return OptionFunc(func(stream *Stream) {
		stream.telemetry = telemetry
	})
}

// WithStopTimeout sets the stop timeout
func WithStopTimeout(timeout time.Duration) Option {
	return OptionFunc(func(stream *Stream) {
		stream.stopTimeout = timeout
	})
}

// WithCapacity specifies a fixed capacity for a queue.
func WithCapacity(capacity int) Option {
	return OptionFunc(func(stream *Stream) {
		stream.capacity = capacity
	})
}
