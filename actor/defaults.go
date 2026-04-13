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

	"github.com/tochemey/goakt/v4/internal/size"
	"github.com/tochemey/goakt/v4/supervisor"
)

const (
	// DefaultPassivationTimeout defines the default passivation timeout
	DefaultPassivationTimeout = 2 * time.Minute
	// DefaultInitMaxRetries defines the default value for retrying actor initialization
	DefaultInitMaxRetries = 5
	// DefaultShutdownTimeout defines the default shutdown timeout
	DefaultShutdownTimeout = 5 * time.Minute
	// DefaultInitTimeout defines the default init timeout
	DefaultInitTimeout = time.Second
	// DefaultPublishStateTimeout defines the default state publication timeout
	// This is the maximum time to wait for a state to be published to the cluster
	// before timing out when the actor system is shutting down
	DefaultPublishStateTimeout = time.Minute
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

	// DefaultClusterBalancerInterval defines the default cluster balancer interval
	DefaultClusterBalancerInterval = time.Second
	kindRoleSeparator              = "::"

	// remoteSendCoalescingMaxBatch caps the number of fire-and-forget RemoteTell
	// messages the internal outbound coalescer packs into a single
	// RemoteTellRequest. Internal tuning; not exposed on remote.Config.
	//
	// Benchmarked on Apple M1 with 20 senders / 10 engines / 2000 actors and
	// default zstd compression: throughput scales roughly linearly with batch
	// size through 256, then diminishes (64→~680k msgs/sec, 256→~890k,
	// 512→~980k). 256 is the sweet spot — meaningfully better than 64 at
	// trivial memory cost (at most 256 pointers plus their payloads queued
	// per destination during a burst).
	remoteSendCoalescingMaxBatch = 256
)

var (
	// DefaultSupervisorDirective defines the default supervisory strategy directive
	DefaultSupervisorDirective = supervisor.StopDirective
)
