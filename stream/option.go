package stream

import "github.com/tochemey/goakt/log"

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(config *EventsStream)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(stream *EventsStream)

func (f OptionFunc) Apply(c *EventsStream) {
	f(c)
}

// WithStorage set the storage
func WithStorage(store Storage) Option {
	return OptionFunc(func(stream *EventsStream) {
		stream.storage = store
	})
}

// WithLogger sets the logger
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(stream *EventsStream) {
		stream.logger = logger
	})
}
