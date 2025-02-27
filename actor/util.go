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

	"github.com/tochemey/goakt/v3/internal/size"
	"github.com/tochemey/goakt/v3/internal/timer"
)

type nameType int

const (
	// DefaultPassivationTimeout defines the default passivation timeout
	DefaultPassivationTimeout = 2 * time.Minute
	// DefaultInitMaxRetries defines the default value for retrying actor initialization
	DefaultInitMaxRetries = 5
	// DefaultShutdownTimeout defines the default shutdown timeout
	DefaultShutdownTimeout = time.Minute
	// DefaultInitTimeout defines the default init timeout
	DefaultInitTimeout = time.Second
	// DefaultPeerStateLoopInterval defines the default peer state loop interval
	DefaultPeerStateLoopInterval = 10 * time.Second
	// DefaultAskTimeout defines the default ask timeout
	DefaultAskTimeout = 5 * time.Second
	// DefaultMaxReadFrameSize defines the default HTTP maximum read frame size
	DefaultMaxReadFrameSize = 16 * size.MB

	// DefaultJanitorInterval defines the default GC interval
	// This helps cleanup dead actors from the given actor system
	DefaultJanitorInterval = 30 * time.Millisecond
	// eventsTopic defines the events topic
	eventsTopic = "topic.events"

	systemNamePrefix = "GoAkt"
	routeeNamePrefix = "GoAktRoutee"

	routerType nameType = iota
	rebalancerType
	rootGuardianType
	userGuardianType
	systemGuardianType
	deathWatchType
	deadletterType
	singletonManagerType
)

var (
	// 1<<63-1 nanoseconds equals about 292 years, which isn't truly "forever" but is the longest valid time.Duration value in Golang.
	longLived time.Duration = 1<<63 - 1
	// NoSender means that there is no sender
	NoSender *PID
	// DefaultSupervisorDirective defines the default supervisory strategy directive
	DefaultSupervisorDirective = StopDirective
	timers                     = timer.NewPool()

	systemNames = map[nameType]string{
		routerType:           "GoAktRouter",
		rebalancerType:       "GoAktRebalancer",
		rootGuardianType:     "GoAktRootGuardian",
		userGuardianType:     "GoAktUserGuardian",
		systemGuardianType:   "GoAktSystemGuardian",
		deathWatchType:       "GoAktDeathWatch",
		deadletterType:       "GoAktDeadletter",
		singletonManagerType: "GoAktSingletonManager",
	}
)
