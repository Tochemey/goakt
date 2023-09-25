package actors

import (
	"time"

	"github.com/tochemey/goakt/eventstream"
	deadletterpb "github.com/tochemey/goakt/pb/deadletter/v1"

	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/telemetry"
)

// pidOption represents the pid
type pidOption func(pid *pid)

// withPassivationAfter sets the actor passivation time
func withPassivationAfter(duration time.Duration) pidOption {
	return func(pid *pid) {
		pid.passivateAfter.Store(duration)
	}
}

// withSendReplyTimeout sets how long in seconds an actor should reply a command
// in a receive-reply pattern
func withSendReplyTimeout(timeout time.Duration) pidOption {
	return func(pid *pid) {
		pid.sendReplyTimeout.Store(timeout)
	}
}

// withInitMaxRetries sets the number of times to retry an actor init process
func withInitMaxRetries(max int) pidOption {
	return func(pid *pid) {
		pid.initMaxRetries.Store(int32(max))
	}
}

// withCustomLogger sets the log
func withCustomLogger(logger log.Logger) pidOption {
	return func(pid *pid) {
		pid.logger = logger
	}
}

// withActorSystem set the actor system of the pid
func withActorSystem(sys ActorSystem) pidOption {
	return func(pid *pid) {
		pid.system = sys
	}
}

// withSupervisorStrategy sets the supervisor strategy to used when dealing
// with child actors
func withSupervisorStrategy(strategy StrategyDirective) pidOption {
	return func(pid *pid) {
		pid.supervisorStrategy = strategy
	}
}

// withShutdownTimeout sets the shutdown timeout
func withShutdownTimeout(duration time.Duration) pidOption {
	return func(pid *pid) {
		pid.shutdownTimeout.Store(duration)
	}
}

// withNoPassivation disable passivation
func withPassivationDisabled() pidOption {
	return func(pid *pid) {
		pid.passivateAfter.Store(-1)
	}
}

// withTelemetry sets the custom telemetry
func withTelemetry(telemetry *telemetry.Telemetry) pidOption {
	return func(pid *pid) {
		pid.telemetry = telemetry
	}
}

// withMailboxSize sets the actor receiveContextBuffer size
func withMailboxSize(size uint64) pidOption {
	return func(pid *pid) {
		pid.mailboxSize = size
	}
}

// withMailbox sets the custom actor receiveContextBuffer
func withMailbox(box Mailbox) pidOption {
	return func(pid *pid) {
		pid.mailbox = box
	}
}

// withStash sets the actor's stash buffer
func withStash(capacity uint64) pidOption {
	return func(pid *pid) {
		pid.stashCapacity.Store(capacity)
	}
}

// withDeadletterStream set the deadletter stream
func withDeadletterStream(stream *eventstream.EventsStream[*deadletterpb.Deadletter]) pidOption {
	return func(pid *pid) {
		pid.deadletterStream = stream
	}
}
