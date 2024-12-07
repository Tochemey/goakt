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

	"github.com/tochemey/goakt/v2/internal/timer"
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
	janitorType
)

var (
	// NoSender means that there is no sender
	NoSender *PID
	// DefaultSupervisoryStrategy defines the default supervisory strategy
	DefaultSupervisoryStrategy = NewStopDirective()
	timers                     = timer.NewPool()

	systemNames = map[nameType]string{
		routerType:         "GoAktRouter",
		rebalancerType:     "GoAktRebalancer",
		rootGuardianType:   "GoAktRootGuardian",
		userGuardianType:   "GoAktUserGuardian",
		systemGuardianType: "GoAktSystemGuardian",
		janitorType:        "GoAktJanitor",
	}
)
