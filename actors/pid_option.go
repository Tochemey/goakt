package actors

import (
	"time"

	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/telemetry"
	"go.uber.org/atomic"
)

// pidOption represents the pid
type pidOption func(pid *pid)

// withPassivationAfter sets the actor passivation time
func withPassivationAfter(duration time.Duration) pidOption {
	return func(pid *pid) {
		pid.passivateAfter = atomic.NewDuration(duration)
	}
}

// withSendReplyTimeout sets how long in seconds an actor should reply a command
// in a receive-reply pattern
func withSendReplyTimeout(timeout time.Duration) pidOption {
	return func(pid *pid) {
		pid.sendReplyTimeout = atomic.NewDuration(timeout)
	}
}

// withInitMaxRetries sets the number of times to retry an actor init process
func withInitMaxRetries(max int) pidOption {
	return func(pid *pid) {
		pid.initMaxRetries = atomic.NewInt32(int32(max))
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
		pid.shutdownTimeout = atomic.NewDuration(duration)
	}
}

// withNoPassivation disable passivation
func withPassivationDisabled() pidOption {
	return func(pid *pid) {
		pid.passivateAfter = atomic.NewDuration(-1)
	}
}

// withTelemetry sets the custom telemetry
func withTelemetry(telemetry *telemetry.Telemetry) pidOption {
	return func(pid *pid) {
		pid.telemetry = telemetry
	}
}

// withMailboxSize sets the actor defaultMailbox size
func withMailboxSize(size uint64) pidOption {
	return func(pid *pid) {
		pid.mailboxSize = size
	}
}

// withMailbox sets the custom actor defaultMailbox
func withMailbox(box Mailbox) pidOption {
	return func(pid *pid) {
		pid.mailbox = box
	}
}
