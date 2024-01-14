/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

	addresspb "github.com/tochemey/goakt/pb/address/v1"
)

// StrategyDirective represents the supervisor strategy directive
type StrategyDirective int

const (
	// protocol defines the Go-Akt addressing protocol
	protocol = "goakt"
	// RestartDirective defines the restart strategy when handling actors failure
	RestartDirective StrategyDirective = iota
	// StopDirective defines the stop strategy when handling actors failure
	StopDirective

	// DefaultPassivationTimeout defines the default passivation timeout
	DefaultPassivationTimeout = 2 * time.Minute
	// DefaultReplyTimeout defines the default send/reply timeout
	DefaultReplyTimeout = 100 * time.Millisecond
	// DefaultInitMaxRetries defines the default value for retrying actor initialization
	DefaultInitMaxRetries = 5
	// DefaultSupervisoryStrategy defines the default supervisory strategy
	DefaultSupervisoryStrategy = StopDirective
	// DefaultShutdownTimeout defines the default shutdown timeout
	DefaultShutdownTimeout = 2 * time.Second
	// DefaultInitTimeout defines the default init timeout
	DefaultInitTimeout = time.Second

	defaultMailboxSize = 4096
	deadlettersTopic   = "topic.deadletters"
)

// NoSender means that there is no sender
var NoSender PID

// RemoteNoSender means that there is no sender
var RemoteNoSender = new(addresspb.Address)
