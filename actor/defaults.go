/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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
)

const (
	// DefaultPassivationTimeout defines the default passivation timeout
	DefaultPassivationTimeout = 2 * time.Minute
	// DefaultInitMaxRetries defines the default value for retrying actor initialization
	DefaultInitMaxRetries = 5
	// DefaultShutdownTimeout defines the default shutdown timeout
	DefaultShutdownTimeout = time.Minute
	// DefaultInitTimeout defines the default init timeout
	DefaultInitTimeout = time.Second
	// DefaultPeerStateSyncInterval defines the default peer state synchronization interval
	DefaultPeerStateSyncInterval = 10 * time.Second
	// DefaultAskTimeout defines the default ask timeout
	DefaultAskTimeout = 5 * time.Second
	// DefaultMaxReadFrameSize defines the default HTTP maximum read frame size
	DefaultMaxReadFrameSize = 16 * size.MB
	// DefaultClusterBootstrapTimeout defines the default cluster bootstrap timeout
	DefaultClusterBootstrapTimeout = 10 * time.Second
	// DefaultClusterStateSyncInterval defines the default cluster state synchronization interval
	DefaultClusterStateSyncInterval = time.Minute
	// DefaultGrainRequestTimeout defines the default grain request timeout
	DefaultGrainRequestTimeout = 5 * time.Second
)

var (
	// DefaultSupervisorDirective defines the default supervisory strategy directive
	DefaultSupervisorDirective = StopDirective
)
