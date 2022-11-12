package actors

import (
	"time"

	"github.com/tochemey/goakt/log"
)

// ActorRefOption represents the actor ref
type ActorRefOption func(ref *ActorRef)

// WithPassivationAfter sets the actor passivation time
func WithPassivationAfter(duration time.Duration) ActorRefOption {
	return func(ref *ActorRef) {
		ref.passivateAfter = duration
	}
}

// WithSendReplyTimeout sets how long in seconds an actor should reply a command
// in a receive-reply pattern
func WithSendReplyTimeout(timeout time.Duration) ActorRefOption {
	return func(ref *ActorRef) {
		ref.sendRecvTimeout = timeout
	}
}

// WithInitMaxRetries sets the number of times to retry an actor init process
func WithInitMaxRetries(max int) ActorRefOption {
	return func(ref *ActorRef) {
		ref.initMaxRetries = max
	}
}

// WithLogger sess the logger
func WithLogger(logger log.Logger) ActorRefOption {
	return func(ref *ActorRef) {
		ref.logger = logger
	}
}

// WithAddress sets the address of the actor ref
func WithAddress(addr Address) ActorRefOption {
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
