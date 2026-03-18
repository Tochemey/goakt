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

package stream

import (
	"time"

	"github.com/tochemey/goakt/v4/actor"
)

const (
	// defaultBufferSize is the default capacity of a stage's output buffer.
	// Stages hold at most this many elements before applying backpressure.
	defaultBufferSize = 256
	// defaultInitialDemand is the first batch of demand sent by a sink to its upstream.
	// It equals 87.5 % of defaultBufferSize, leaving room for in-flight elements.
	defaultInitialDemand = 224
	// defaultRefillThreshold is the consumed-count at which a sink requests a refill.
	// When consumed >= defaultRefillThreshold, the sink sends Request(consumed) upstream.
	defaultRefillThreshold = 64
	// defaultPullTimeout is the maximum time to wait for a pull response from an actor source.
	defaultPullTimeout = 5 * time.Second
)

// RetryConfig controls retry behaviour when ErrorStrategy is Retry.
type RetryConfig struct {
	// MaxAttempts is the maximum number of times an element is processed before
	// the error is escalated. Must be ≥ 1. Zero is treated as 1.
	MaxAttempts int
}

// defaultRetryConfig returns a RetryConfig with sensible defaults.
func defaultRetryConfig() RetryConfig {
	return RetryConfig{MaxAttempts: 1}
}

// StageConfig carries per-stage configuration.
// All fields have sensible defaults provided by defaultStageConfig().
type StageConfig struct {
	// InitialDemand is the first batch of demand the sink sends upstream.
	// Default: defaultInitialDemand (224).
	InitialDemand int64
	// RefillThreshold is the number of elements consumed before requesting a refill.
	// Default: defaultRefillThreshold (64).
	RefillThreshold int64
	// ErrorStrategy controls element-level error handling.
	// Default: FailFast.
	ErrorStrategy ErrorStrategy
	// RetryConfig is used when ErrorStrategy is Retry. Controls max attempts.
	// Default: RetryConfig{MaxAttempts: 1}.
	RetryConfig RetryConfig
	// OverflowStrategy controls what happens when the source buffer is full.
	// Default: DropTail.
	OverflowStrategy OverflowStrategy
	// PullTimeout is the timeout for pulls from actor sources.
	// Default: defaultPullTimeout (5s).
	PullTimeout time.Duration
	// System is set by the materializer; allows composite source actors (Merge, Combine)
	// to spawn sub-pipelines at wire time. Not set by defaultStageConfig.
	System actor.ActorSystem
	// Metrics is an optional shared metrics collector injected by the materializer
	// for the source and sink stages so that StreamHandle.Metrics() reflects real counts.
	// When nil, stage actors use a local, unobserved stageMetrics.
	Metrics *stageMetrics
	// BufferSize is the capacity for this stage's mailbox (BoundedMailbox(BufferSize*2)).
	// Default: 256.
	BufferSize int
	// Mailbox overrides the default actor mailbox for this stage.
	// When nil, a BoundedMailbox(BufferSize*2) is used.
	Mailbox actor.Mailbox
	// Name overrides the auto-generated actor name for this stage.
	// When empty, the materializer generates a name.
	Name string
	// Tags are propagated to metrics and traces.
	Tags map[string]string
	// Tracer is an optional hook for distributed tracing.
	Tracer Tracer
	// OnDrop is called when an element is dropped (overflow, exhausted retries).
	// Use this to route dropped elements to a dead-letter store.
	OnDrop func(value any, reason string)
	// Fusion controls whether this stage may be fused with adjacent stateless stages.
	// Default: true.
	Fusion bool
}

// defaultStageConfig returns a StageConfig with production-safe defaults.
func defaultStageConfig() StageConfig {
	return StageConfig{
		InitialDemand:    defaultInitialDemand,
		RefillThreshold:  defaultRefillThreshold,
		ErrorStrategy:    FailFast,
		RetryConfig:      defaultRetryConfig(),
		OverflowStrategy: DropTail,
		PullTimeout:      defaultPullTimeout,
		BufferSize:       256,
		Fusion:           true,
	}
}
