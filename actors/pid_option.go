/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package actors

import (
	"time"

	"github.com/tochemey/goakt/v2/internal/eventstream"
	"github.com/tochemey/goakt/v2/log"
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
func withInitMaxRetries(max int) pidOption {
	return func(pid *PID) {
		pid.initMaxRetries.Store(int32(max))
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

// withSupervisorStrategies defines the supervisor strategies
func withSupervisorStrategies(strategies ...*SupervisorStrategy) pidOption {
	return func(pid *PID) {
		if pid.supervisorStrategies == nil {
			pid.supervisorStrategies = newStrategiesMap()
		}

		for _, strategy := range strategies {
			pid.supervisorStrategies.Put(strategy)
		}
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
func withEventsStream(stream *eventstream.EventsStream) pidOption {
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
