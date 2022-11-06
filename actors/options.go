package actors

import (
	"time"

	"github.com/tochemey/goakt/logging"
)

// ActorOpt represents the actor ref
type ActorOpt func(ref *ActorRef)

// WithPassivationAfter sets the actor passivation time
func WithPassivationAfter(duration time.Duration) ActorOpt {
	return func(ref *ActorRef) {
		ref.passivateAfter = duration
	}
}

// WithSendReplyTimeout set how long in seconds an actor should reply a command
// in a receive-reply pattern
func WithSendReplyTimeout(timeout time.Duration) ActorOpt {
	return func(ref *ActorRef) {
		ref.sendRecvTimeout = timeout
	}
}

// WithInitMaxRetries set the number of times to retry an actor init process
func WithInitMaxRetries(max int) ActorOpt {
	return func(ref *ActorRef) {
		ref.initMaxRetries = max
	}
}

// WithLogger set the logger
func WithLogger(logger logging.Logger) ActorOpt {
	return func(ref *ActorRef) {
		ref.logger = logger
	}
}
