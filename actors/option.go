package actors

import (
	"time"

	"github.com/tochemey/goakt/logging"
)

// Option represents the actor ref
type Option func(ref *ActorRef)

// WithPassivationAfter sets the actor passivation time
func WithPassivationAfter(duration time.Duration) Option {
	return func(ref *ActorRef) {
		ref.passivateAfter = duration
	}
}

// WithSendReplyTimeout set how long in seconds an actor should reply a command
// in a receive-reply pattern
func WithSendReplyTimeout(timeout time.Duration) Option {
	return func(ref *ActorRef) {
		ref.sendRecvTimeout = timeout
	}
}

// WithInitMaxRetries set the number of times to retry an actor init process
func WithInitMaxRetries(max int) Option {
	return func(ref *ActorRef) {
		ref.initMaxRetries = max
	}
}

// WithLogger set the logger
func WithLogger(logger logging.Logger) Option {
	return func(ref *ActorRef) {
		ref.logger = logger
	}
}

// WithAddress sets the address of the actor ref
func WithAddress(addr Address) Option {
	return func(ref *ActorRef) {
		ref.addr = addr
	}
}

//// WithTracerProvider specifies a tracer provider to use for creating a tracer.
//// If none is specified, the global provider is used.
//func WithTracerProvider(provider trace.TracerProvider) Option {
//	return func(ref *ActorRef) {
//		ref.telemetry.TracerProvider = provider
//	}
//}
//
//// WithMeterProvider specifies a tracer provider to use for creating a tracer.
//// If none is specified, the global provider is used.
//func WithMeterProvider(provider metric.MeterProvider) Option {
//	return func(ref *ActorRef) {
//		ref.telemetry.MeterProvider = provider
//	}
//}
