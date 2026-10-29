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

	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/metric"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/supervisor"
)

// pidOption represents the pid
type pidOption func(pid *PID)

// pidSettings are the settings every spawn hands to a PID: the ones a
// top-level actor inherits from its system and a child from its parent. They
// travel as one value and newPID applies them before the pidOptions, so a
// spawn does not allocate one closure per setting. A field left nil or zero
// is applied as the matching pidOption would apply it, except where the
// comment says the field is skipped when unset.
type pidSettings struct {
	actorSystem    ActorSystem
	initMaxRetries int
	// initTimeout is the explicit init timeout override, nil to inherit the
	// system default at initialization (see withInitTimeout).
	initTimeout        *time.Duration
	remoting           remoteclient.Client
	passivationManager *passivationManager
	// metricProvider is skipped when nil (see withMetricProvider).
	metricProvider *metric.Provider
	// supervisor is skipped when nil; newPID then installs the default.
	supervisor *supervisor.Supervisor
	// mailbox is the custom mailbox, skipped when nil so that newPID
	// embeds the default one.
	mailbox             Mailbox
	passivationStrategy passivation.Strategy
	// relocationDisabled clears the relocation bit that newPID sets by
	// default (see withRelocationDisabled).
	relocationDisabled bool
}

// withReliableDelivery records immutable reliable-delivery settings.
func withReliableDelivery(config *reliableDeliveryConfig) pidOption {
	return func(pid *PID) {
		pid.attachCompanion().reliableDelivery = config.clone()
	}
}

// withReliableCompanion marks the PID as the endpoint-owned reliable-delivery
// controller described by spec.
func withReliableCompanion(spec *reliableCompanionSpec) pidOption {
	return func(pid *PID) {
		pid.attachCompanion().reliableCompanion = spec
	}
}

// withDurableQueue retains the point-to-point producer endpoint's durable
// queue instance so controller creation and ReSpawn recovery can hand it to a
// fresh producer controller.
func withDurableQueue(queue DurableProducerQueue) pidOption {
	return func(pid *PID) {
		pid.attachCompanion().durableQueue = queue
	}
}

// withDurableWorkQueue retains the work-pulling producer endpoint's durable
// work queue instance so controller creation and ReSpawn recovery can hand it
// to a fresh work-pulling producer controller.
func withDurableWorkQueue(queue DurableWorkQueue) pidOption {
	return func(pid *PID) {
		pid.attachCompanion().durableWorkQueue = queue
	}
}

// withStash sets the actor's stash buffer
func withStash() pidOption {
	return func(pid *PID) {
		pid.stashState.Store(&stashState{box: NewUnboundedMailbox()})
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
		if spec != nil {
			pid.attachCompanion().singletonSpec = spec
		}
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

// withReentrancy sets the async request behavior for the actor.
func withReentrancy(reentrancy *reentrancy.Reentrancy) pidOption {
	return func(pid *PID) {
		if reentrancy != nil {
			pid.reentrancy.Store(newReentrancyState(reentrancy.Mode(), reentrancy.MaxInFlight()))
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
			pid.attachCompanion().role = new(role)
		}
	}
}

// apply sets the settings on pid, each exactly as its pidOption would.
func (s *pidSettings) apply(pid *PID) {
	pid.initMaxRetries.Store(int32(s.initMaxRetries))
	pid.setActorSystem(s.actorSystem)
	pid.initTimeout.Store(s.initTimeout)
	pid.remoting = s.remoting
	pid.passivationManager = s.passivationManager

	if s.metricProvider != nil {
		pid.attachCompanion().metricProvider = s.metricProvider
	}

	if s.supervisor != nil {
		pid.supervisor = s.supervisor
	}

	if s.mailbox != nil {
		pid.mailbox = s.mailbox
	}

	if s.relocationDisabled {
		pid.setState(relocationState, false)
	}

	pid.setPassivationStrategy(s.passivationStrategy)
}

// setActorSystem sets the actor system of the pid and caches the concrete
// *actorSystem's dispatcher on the PID for the hot-path enqueue. Remote PIDs
// pass a nil dispatcher because remoting has its own delivery path that never
// calls the local dispatcher.
func (pid *PID) setActorSystem(sys ActorSystem) {
	pid.actorSystem = sys
	if as, ok := sys.(*actorSystem); ok {
		pid.dispatcher = as.dispatcher
	}
}

// setPassivationStrategy sets the PID passivation strategy and records
// whether it is message-count based, which the message path reads instead of
// a type switch.
func (pid *PID) setPassivationStrategy(strategy passivation.Strategy) {
	pid.passivationStrategy = strategy
	_, isMsgCount := strategy.(*passivation.MessagesCountBasedStrategy)
	pid.msgCountPassivation.Store(isMsgCount)
}
