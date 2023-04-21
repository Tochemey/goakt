package actors

import (
	"time"

	"github.com/tochemey/goakt/internal/telemetry"

	"github.com/tochemey/goakt/log"
)

// pidOption represents the pid
type pidOption func(pid *pid)

// withPassivationAfter sets the actor passivation time
func withPassivationAfter(duration time.Duration) pidOption {
	return func(pid *pid) {
		pid.passivateAfter = duration
	}
}

// withSendReplyTimeout sets how long in seconds an actor should reply a command
// in a receive-reply pattern
func withSendReplyTimeout(timeout time.Duration) pidOption {
	return func(pid *pid) {
		pid.sendReplyTimeout = timeout
	}
}

// withInitMaxRetries sets the number of times to retry an actor init process
func withInitMaxRetries(max int) pidOption {
	return func(pid *pid) {
		pid.initMaxRetries = max
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
		pid.shutdownTimeout = duration
	}
}

// withNoPassivation disable passivation
func withPassivationDisabled() pidOption {
	return func(pid *pid) {
		pid.passivateAfter = -1
	}
}

// withTelemetry sets the custom telemetry
func withTelemetry(telemetry *telemetry.Telemetry) pidOption {
	return func(pid *pid) {
		pid.telemetry = telemetry
	}
}
