/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actor

import (
	"time"

	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/internal/collection"
	"github.com/tochemey/goakt/v3/internal/eventstream"
	"github.com/tochemey/goakt/v3/internal/workerpool"
	"github.com/tochemey/goakt/v3/log"
)

// pidOption represents the pid
type pidOption func(pid *PID)

// withPassivationAfter sets the actor passivation time
func withPassivationAfter(duration time.Duration) pidOption {
	return func(pid *PID) {
		pid.passivateAfter.Store(duration)
	}
}

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
		pid.system = sys
	}
}

// withWorkerPool sets the worker pool
func withWorkerPool(pool *workerpool.WorkerPool) pidOption {
	return func(pid *PID) {
		pid.workerPool = pool
	}
}

// withSupervisor defines the supervisor
func withSupervisor(supervisor *Supervisor) pidOption {
	return func(pid *PID) {
		pid.supervisor = supervisor
	}
}

// withNoPassivation disable passivation
func withPassivationDisabled() pidOption {
	return func(pid *PID) {
		pid.passivateAfter.Store(-1)
	}
}

// withStash sets the actor's stash buffer
func withStash() pidOption {
	return func(pid *PID) {
		pid.stashBox = NewUnboundedMailbox()
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
func withRemoting(remoting *Remoting) pidOption {
	return func(pid *PID) {
		pid.remoting = remoting
	}
}

// asSingleton set the actor as singleton
func asSingleton() pidOption {
	return func(pid *PID) {
		pid.isSingleton.Store(true)
	}
}

// withRelocationDisabled disables the actor relocation
func withRelocationDisabled() pidOption {
	return func(pid *PID) {
		pid.relocatable.Store(false)
	}
}

// withDependencies configures an actor's PID with specified external dependencies.
// It takes a variadic list of Dependency interfaces to associate with the actor.
func withDependencies(dependencies ...extension.Dependency) pidOption {
	return func(pid *PID) {
		if pid.dependencies == nil {
			pid.dependencies = collection.NewMap[string, extension.Dependency]()
		}
		for _, dependency := range dependencies {
			pid.dependencies.Set(dependency.ID(), dependency)
		}
	}
}
