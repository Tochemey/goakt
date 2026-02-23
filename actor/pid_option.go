// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package actor

import (
	"time"

	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/metric"
	"github.com/tochemey/goakt/v4/internal/pointer"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/supervisor"
)

// pidOption represents the pid
type pidOption func(pid *PID)

// withInitMaxRetries sets the number of times to retry an actor init process
func withInitMaxRetries(value int) pidOption {
	return func(pid *PID) {
		pid.initMaxRetries.Store(int32(value))
	}
}

// withCustomLogger sets the log
func withCustomLogger(logger log.Logger) pidOption {
	return func(pid *PID) {
		pid.logger = logger
	}
}

// withActorSystem set the actor system of the pid
func withActorSystem(sys ActorSystem) pidOption {
	return func(pid *PID) {
		pid.actorSystem = sys
	}
}

// withSupervisor defines the supervisor
func withSupervisor(supervisor *supervisor.Supervisor) pidOption {
	return func(pid *PID) {
		pid.supervisor = supervisor
	}
}

// withStash sets the actor's stash buffer
func withStash() pidOption {
	return func(pid *PID) {
		pid.stashState = &stashState{box: NewUnboundedMailbox()}
	}
}

// withMailbox sets the custom actor mailbox
func withMailbox(box Mailbox) pidOption {
	return func(pid *PID) {
		pid.mailbox = box
	}
}

// withEventsStream set the events stream
func withEventsStream(stream eventstream.Stream) pidOption {
	return func(pid *PID) {
		pid.eventsStream = stream
	}
}

// withInitTimeout sets the init timeout
func withInitTimeout(duration time.Duration) pidOption {
	return func(pid *PID) {
		pid.initTimeout.Store(duration)
	}
}

// withRemoting set the remoting feature
func withRemoting(remoting remote.Client) pidOption {
	return func(pid *PID) {
		pid.remoting = remoting
	}
}

// withPassivationManager sets the passivation manager responsible for scheduling inactivity checks.
func withPassivationManager(manager *passivationManager) pidOption {
	return func(pid *PID) {
		pid.passivationManager = manager
	}
}

// asSingleton set the actor as singleton
func asSingleton() pidOption {
	return func(pid *PID) {
		pid.setState(singletonState, true)
	}
}

// withSingletonSpec records the singleton configuration on the PID.
func withSingletonSpec(spec *singletonSpec) pidOption {
	return func(pid *PID) {
		pid.singletonSpec = spec
	}
}

// withRelocationDisabled disables the actor relocation
func withRelocationDisabled() pidOption {
	return func(pid *PID) {
		pid.setState(relocationState, false)
	}
}

// withDependencies configures an actor's PID with specified external dependencies.
// It takes a variadic list of Dependency interfaces to associate with the actor.
func withDependencies(dependencies ...extension.Dependency) pidOption {
	return func(pid *PID) {
		if pid.dependencies == nil {
			pid.dependencies = xsync.NewMap[string, extension.Dependency]()
		}
		for _, dependency := range dependencies {
			pid.dependencies.Set(dependency.ID(), dependency)
		}
	}
}

// withPassivationStrategy sets the PID passivation strategy
func withPassivationStrategy(strategy passivation.Strategy) pidOption {
	return func(pid *PID) {
		pid.passivationStrategy = strategy
		_, isMsgCount := strategy.(*passivation.MessagesCountBasedStrategy)
		pid.msgCountPassivation.Store(isMsgCount)
	}
}

// withReentrancy sets the async request behavior for the actor.
func withReentrancy(reentrancy *reentrancy.Reentrancy) pidOption {
	return func(pid *PID) {
		if reentrancy != nil {
			pid.reentrancy = newReentrancyState(reentrancy.Mode(), reentrancy.MaxInFlight())
		}
	}
}

func asSystemActor() pidOption {
	return func(pid *PID) {
		pid.setState(systemState, true)
	}
}

// withRole sets the role the actor belongs to
func withRole(role string) pidOption {
	return func(pid *PID) {
		if role != "" {
			pid.role = pointer.To(role)
		}
	}
}

// withMetricProvider sets the metric provider for the PID
func withMetricProvider(metricProvider *metric.Provider) pidOption {
	return func(pid *PID) {
		if metricProvider != nil {
			pid.metricProvider = metricProvider
		}
	}
}
