package actors

import (
	"time"

	actorsv1 "github.com/tochemey/goakt/gen/actors/v1"
	"github.com/tochemey/goakt/log"
)

// pidOption represents the pid
type pidOption func(ref *pid)

// withPassivationAfter sets the actor passivation time
func withPassivationAfter(duration time.Duration) pidOption {
	return func(ref *pid) {
		ref.passivateAfter = duration
	}
}

// withSendReplyTimeout sets how long in seconds an actor should reply a command
// in a receive-reply pattern
func withSendReplyTimeout(timeout time.Duration) pidOption {
	return func(ref *pid) {
		ref.sendRecvTimeout = timeout
	}
}

// withInitMaxRetries sets the number of times to retry an actor init process
func withInitMaxRetries(max int) pidOption {
	return func(ref *pid) {
		ref.initMaxRetries = max
	}
}

// withCustomLogger sets the logger
func withCustomLogger(logger log.Logger) pidOption {
	return func(ref *pid) {
		ref.logger = logger
	}
}

// withAddress sets the address of the pid
func withAddress(addr Address) pidOption {
	return func(ref *pid) {
		ref.addr = addr
	}
}

// withActorSystem set the actor system of the pid
func withActorSystem(sys ActorSystem) pidOption {
	return func(ref *pid) {
		ref.system = sys
	}
}

// withLocalID set the kind of actor represented by the pid
func withLocalID(kind, id string) pidOption {
	return func(ref *pid) {
		ref.id = &LocalID{
			kind: kind,
			id:   id,
		}
	}
}

// withSupervisorStrategy sets the supervisor strategy to used when dealing
// with child actors
func withSupervisorStrategy(strategy actorsv1.Strategy) pidOption {
	return func(ref *pid) {
		ref.supervisorStrategy = strategy
	}
}

// withShutdownTimeout sets the shutdown timeout
func withShutdownTimeout(duration time.Duration) pidOption {
	return func(ref *pid) {
		ref.shutdownTimeout = duration
	}
}
