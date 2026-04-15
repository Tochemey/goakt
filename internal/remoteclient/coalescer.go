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

package remoteclient

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
)

// errCoalescerClosed is returned from submit when the coalescer has been shut
// down. It is an internal sentinel; RemoteTell maps it to the caller-facing
// ErrRemoteSendFailure so public surface stays stable.
var errCoalescerClosed = errors.New("remote send: coalescer is closed")

// CoalescingErrorHandler is invoked when a batched flush fails. It receives
// the destination endpoint ("host:port"), the messages that were in the failed
// batch, and the underlying error. Handlers run inline on the per-destination
// writer goroutine and must not block for long.
type CoalescingErrorHandler func(dest string, messages []*internalpb.RemoteMessage, err error)

// coalescingConfig captures the tunables for send coalescing. A zero maxBatch
// disables coalescing (the synchronous RemoteTell path is used instead).
type coalescingConfig struct {
	maxBatch   int
	errHandler CoalescingErrorHandler
}

// enabled reports whether the configuration requests coalescing.
func (c coalescingConfig) enabled() bool { return c.maxBatch > 0 }

// coalescer batches outbound RemoteTell messages bound for a single
// destination (host:port) and flushes them in a single RemoteTellRequest.
// One coalescer exists per destination; a single writer goroutine drains the
// buffer, so messages to the same destination remain in submission order.
//
// Flush policy is Nagle-style: the writer goroutine sends whatever is
// available as soon as it wakes up (no forced delay). Batching only happens
// naturally while a send is in flight — any messages enqueued during that
// window are drained into the next batch once the RPC returns. That gives
// immediate delivery on the happy path (idle destination) and automatic
// batching under load without a timer to tune.
//
// Context propagation is handled at submit time: the caller-facing RemoteTell
// snapshots the configured propagator's headers into each RemoteMessage's
// Metadata field before enqueueing, and the server applies that per-message
// metadata when dispatching. The flush goroutine therefore only needs a
// bare context with a transport deadline.
type coalescer struct {
	dest      string
	netClient *inet.Client
	in        chan *internalpb.RemoteMessage
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	maxBatch   int
	errHandler CoalescingErrorHandler
}

// newCoalescer starts a writer goroutine for the given destination.
func newCoalescer(dest string, nc *inet.Client, cfg coalescingConfig) *coalescer {
	maxBatch := cfg.maxBatch
	if maxBatch <= 0 {
		maxBatch = 64
	}

	c := &coalescer{
		dest:       dest,
		netClient:  nc,
		in:         make(chan *internalpb.RemoteMessage, maxBatch*4),
		done:       make(chan struct{}),
		maxBatch:   maxBatch,
		errHandler: cfg.errHandler,
	}

	c.wg.Add(1)
	go c.run()
	return c
}

// submit enqueues a pre-built RemoteMessage, blocking until the per-destination
// writer has room, the caller's context is cancelled, or the coalescer is shut
// down. This is the industry-standard "bounded queue + block-with-deadline +
// then-error" pattern: Erlang distribution, Akka Artery, Kafka producer, and
// gRPC streaming all converge on it. Unbounded buffering risks OOM under
// sustained overload; silent drop hides overload from operators; unconditional
// block deadlocks the sender.
//
// Returns:
//   - nil once the message has been handed to the writer.
//   - ctx.Err() if the caller's context deadline expires or is cancelled
//     before space becomes available — typically surfaced by the caller as
//     ErrRemoteSendBackpressure so the caller can retry, drop, or
//     circuit-break visibly.
//   - errCoalescerClosed if the coalescer is shut down while the caller is
//     waiting (or before the call began).
func (c *coalescer) submit(ctx context.Context, msg *internalpb.RemoteMessage) error {
	// Pre-check shutdown so a submit after close returns immediately rather
	// than racing with a context that has no deadline.
	select {
	case <-c.done:
		return errCoalescerClosed
	default:
	}

	// Fast path: space immediately available. Keeps the happy path a single
	// channel op, avoiding the cost of arming ctx.Done() / c.done selects.
	select {
	case c.in <- msg:
		return nil
	default:
	}

	// Slow path: block on whichever of (space, ctx cancel, shutdown) fires
	// first. This is the backpressure signal to the caller.
	select {
	case c.in <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return errCoalescerClosed
	}
}

// close signals the writer goroutine to flush and exit, then blocks until
// the goroutine returns. Safe to call multiple times.
func (c *coalescer) close() {
	c.closeOnce.Do(func() { close(c.done) })
	c.wg.Wait()
}

// run is the writer loop. It waits for at least one message to be available,
// then opportunistically drains any additional messages already buffered in
// the channel (up to maxBatch) without waiting, and sends the batch. While
// the send RPC is in flight, new submits accumulate in the channel; they are
// picked up on the next loop iteration as one batch. No timer is used: a
// single message against an idle destination is sent immediately.
func (c *coalescer) run() {
	defer c.wg.Done()

	batch := make([]*internalpb.RemoteMessage, 0, c.maxBatch)

	flush := func() {
		if len(batch) == 0 {
			return
		}

		req := &internalpb.RemoteTellRequest{RemoteMessages: batch}
		// Per-message propagation metadata is already carried inside each
		// RemoteMessage, so the batch context only needs a transport deadline.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		resp, err := c.netClient.SendProto(ctx, req)
		cancel()
		if err == nil {
			err = checkProtoError(resp)
		}

		if err != nil && c.errHandler != nil {
			// Hand the handler its own slice. We clear batch's entries below
			// so the underlying array can be reused for the next flush; if
			// the handler retains its argument (async logging, buffering,
			// etc.) it must not see those entries nil out from under it.
			handed := make([]*internalpb.RemoteMessage, len(batch))
			copy(handed, batch)
			c.errHandler(c.dest, handed, err)
		}

		for i := range batch {
			batch[i] = nil
		}

		batch = batch[:0]
	}

	// drainReady pulls any messages that are already sitting in the channel
	// buffer into batch without waiting. Stops at maxBatch.
	drainReady := func() {
		for len(batch) < c.maxBatch {
			select {
			case m := <-c.in:
				batch = append(batch, m)
			default:
				return
			}
		}
	}

	for {
		select {
		case <-c.done:
			// Drain anything still buffered and exit. Submit refuses new
			// enqueues once done is closed, so the channel is a bounded
			// set at this point.
			drainReady()
			flush()
			return
		case m := <-c.in:
			batch = append(batch, m)
			drainReady()
			flush()
		}
	}
}
