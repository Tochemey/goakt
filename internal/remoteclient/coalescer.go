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

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
)

// coalescerFlushTimeout bounds each batched flush RPC so a wedged peer fails
// the flush instead of stalling the writer goroutine indefinitely. The
// context and timer it requires are per flush, amortized across the whole
// batch.
const coalescerFlushTimeout = 5 * time.Second

// defaultCoalescerBufferedBytes keeps direct coalescer construction (primarily
// tests) bounded when no client credit window is supplied.
const defaultCoalescerBufferedBytes uint64 = 16 * 1024 * 1024

// coalescerLinger is how long a dry writer parks for the next message before
// exiting. Request/response traffic (a message every few microseconds to
// milliseconds) keeps one hot writer and pays a channel wake instead of a
// goroutine spawn per message; a destination idle past the linger drops to
// zero goroutines.
const coalescerLinger = 10 * time.Millisecond

// errCoalescerClosed is returned from submit when the coalescer has been shut
// down. It is an internal sentinel; RemoteTell maps it to the caller-facing
// ErrRemoteSendFailure so public surface stays stable.
var errCoalescerClosed = errors.New("remote send: coalescer is closed")

// TellFailureHandler is invoked when a fire-and-forget tell cannot be
// delivered: a coalesced legacy batch flush fails, or a duplex-admitted tell
// fails on dial/encode/write (see tell_pump.go). It receives the destination
// endpoint ("host:port"), the failed messages, and the underlying error.
// Handlers run inline on the coalescer writer or lane-pump goroutine and must
// not block for long.
type TellFailureHandler func(dest string, messages []*internalpb.RemoteMessage, err error)

// coalescingConfig captures the tunables for send coalescing. A zero maxBatch
// disables coalescing (the synchronous RemoteTell path is used instead).
// Failure fan-out uses the client-level [TellFailureHandler], passed into
// each coalescer at construction.
type coalescingConfig struct {
	maxBatch         int
	maxBufferedBytes uint64
	flushTimeout     time.Duration
}

// enabled reports whether the configuration requests coalescing.
func (c coalescingConfig) enabled() bool { return c.maxBatch > 0 }

// coalescerKey identifies one coalescer shard in the client's cache: a
// destination endpoint plus the ordinary-lane shard its receivers hash to.
// Keying the cache by struct instead of a formatted string keeps the
// per-message lookup on the RemoteTell path allocation-free.
type coalescerKey struct {
	host string
	port int
	lane uint32
}

// coalescer batches outbound RemoteTell messages bound for a single
// destination lane shard and flushes them in a single RemoteTellRequest.
// One coalescer exists per (destination endpoint, ordinary-lane shard); at
// most one writer goroutine drains the buffer at a time, so messages to the
// same receiver remain in submission order.
//
// The writer is transient: it is spawned on demand by submit, exists only
// while messages are buffered or a flush is in flight, and exits when the
// buffer runs dry. An idle destination therefore costs zero goroutines.
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
	// dest is the destination endpoint ("host:port") handed to the failure
	// handler; it carries no shard information so operators see one stable
	// endpoint format across every fan-out path.
	dest      string
	done      chan types.Unit
	closeOnce sync.Once
	wg        sync.WaitGroup

	// mu guards the queue, the writer state and the byte budget, so a submit
	// admits, enqueues and spawns or wakes the writer under one acquisition.
	// It also keeps a writer spawn from racing close's final Wait: every
	// wg.Add happens under mu before closed is observed true, and close only
	// Waits after setting closed under mu.
	mu sync.Mutex
	// queue holds the messages submitted and not yet taken by the writer, in
	// submission order, as a ring of maxBatch*4 slots: head is the oldest
	// message and queued the number held. A ring under mu instead of a
	// channel costs a submit one lock and lets the writer take a whole batch
	// under one more, where a channel takes its lock once per send and once
	// per receive.
	queue   []*internalpb.RemoteMessage
	head    int
	queued  int
	running bool
	// lingering is set while a dry writer waits for the next message; the
	// submit that clears it sends the writer a ready signal.
	lingering bool
	closed    bool
	// bufferedBytes counts payloads owned by this coalescer, including the
	// batch currently being flushed. Keeping in-flight bytes charged prevents
	// a blocked transport from freeing the admission budget merely because
	// the writer removed messages from the channel.
	bufferedBytes uint64

	maxBatch         int
	maxBufferedBytes uint64
	flushTimeout     time.Duration
	// linger is how long a dry writer parks for the next message
	// (coalescerLinger); tests lengthen it to hold the writer lingering.
	linger time.Duration
	// ready wakes a lingering writer. A token left behind by a submit that
	// raced the writer's linger timeout costs the next linger one empty pass.
	ready chan types.Unit
	// space is a coalesced wakeup, not a fair semaphore. It is signalled when
	// a flush returns bytes to the budget and when the writer takes from a
	// full queue. A waiter that wakes and is admitted relays the token, so
	// waiters are admitted one after another while room remains; a waiter
	// that wakes and still does not fit drops it. The next completed flush
	// emits another wakeup, and once retained bytes reach zero whichever
	// waiter wakes can always reserve, avoiding both deadlock and wake storms.
	space      chan types.Unit
	errHandler TellFailureHandler
	// flushBatch delivers one drained batch to the destination, choosing the
	// duplex or legacy transport per the peer's negotiated protocol. Injected
	// by the owning client so the coalescer stays transport-agnostic. On
	// error it returns the suffix of req.RemoteMessages that was not
	// delivered, so failure fan-out covers each message exactly once even
	// when the transport split the batch and delivered a prefix.
	flushBatch func(ctx context.Context, req *internalpb.RemoteTellRequest) ([]*internalpb.RemoteMessage, error)
}

// newCoalescer creates the per-shard buffer. No goroutine starts here; the
// first submit spawns the transient writer.
func newCoalescer(dest string, cfg coalescingConfig, failureHandler TellFailureHandler, flushBatch func(ctx context.Context, req *internalpb.RemoteTellRequest) ([]*internalpb.RemoteMessage, error)) *coalescer {
	maxBatch := cfg.maxBatch
	if maxBatch <= 0 {
		maxBatch = 64
	}

	maxBufferedBytes := cfg.maxBufferedBytes
	if maxBufferedBytes == 0 {
		maxBufferedBytes = defaultCoalescerBufferedBytes
	}

	flushTimeout := cfg.flushTimeout
	if flushTimeout <= 0 {
		flushTimeout = coalescerFlushTimeout
	}

	return &coalescer{
		dest:             dest,
		queue:            make([]*internalpb.RemoteMessage, maxBatch*4),
		done:             make(chan types.Unit),
		maxBatch:         maxBatch,
		maxBufferedBytes: maxBufferedBytes,
		flushTimeout:     flushTimeout,
		linger:           coalescerLinger,
		ready:            make(chan types.Unit, 1),
		space:            make(chan types.Unit, 1),
		errHandler:       failureHandler,
		flushBatch:       flushBatch,
	}
}

// submit enqueues a pre-built RemoteMessage, blocking until the per-destination
// writer has room, the caller's context is cancelled, or the coalescer is shut
// down. This is the industry-standard "bounded queue + block-with-deadline +
// then-error" pattern: Erlang distribution, Akka Artery, Kafka producer, and
// gRPC streaming all converge on it. Unbounded buffering risks OOM under
// sustained overload; silent drop hides overload from operators; unconditional
// block deadlocks the sender.
//
// A submit that finds room takes only c.mu and touches neither its context
// nor the space channel (it sends on ready only to wake a lingering writer):
// waiting on ctx.Done() costs a lock on that channel, and a context shared
// by many senders turns it into one lock every sender contends on.
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
	cost := coalescedMessageBytes(msg)
	waited := false

	for {
		accepted, err := c.enqueue(msg, cost)
		if err != nil {
			return err
		}

		if accepted {
			// Relay the wakeup this submit consumed so the next waiter
			// checks for the room that may remain.
			if waited {
				c.signalSpace()
			}

			return nil
		}

		select {
		case <-c.space:
			waited = true
		case <-ctx.Done():
			return ctx.Err()
		case <-c.done:
			return errCoalescerClosed
		}
	}
}

// enqueue appends msg to the queue when a slot is free and the byte budget
// admits cost, then makes sure a writer drains it: it spawns the writer when
// none runs and wakes it when it lingers. It reports false when the caller
// must wait for space, and errCoalescerClosed once close has begun. Admission,
// the append and the writer check share one critical section, so a message
// accepted before close always has a running writer to flush it.
func (c *coalescer) enqueue(msg *internalpb.RemoteMessage, cost uint64) (bool, error) {
	c.mu.Lock()

	if c.closed {
		c.mu.Unlock()
		return false, errCoalescerClosed
	}

	if c.queued == len(c.queue) || !c.admits(cost) {
		c.mu.Unlock()
		return false, nil
	}

	c.push(msg)
	c.bufferedBytes += cost

	switch {
	case !c.running:
		c.running = true
		c.wg.Add(1)
		c.mu.Unlock()

		go c.run()
	case c.lingering:
		c.lingering = false
		c.mu.Unlock()

		select {
		case c.ready <- types.Unit{}:
		default:
		}
	default:
		c.mu.Unlock()
	}

	return true, nil
}

// push appends msg at the tail of the queue. The caller holds c.mu and has
// checked that a slot is free.
func (c *coalescer) push(msg *internalpb.RemoteMessage) {
	c.queue[(c.head+c.queued)%len(c.queue)] = msg
	c.queued++
}

// admits reports whether cost fits the coalescer's retained-memory budget.
// One message larger than the window is admitted only when the coalescer owns
// no other bytes, so valid large messages still make progress without allowing
// an unbounded train of oversized payloads to accumulate. The caller holds
// c.mu.
func (c *coalescer) admits(cost uint64) bool {
	return c.bufferedBytes == 0 ||
		(c.bufferedBytes <= c.maxBufferedBytes && cost <= c.maxBufferedBytes-c.bufferedBytes)
}

// releaseBytes returns retained-memory budget and wakes one blocked submitter.
func (c *coalescer) releaseBytes(cost uint64) {
	c.mu.Lock()
	if cost >= c.bufferedBytes {
		c.bufferedBytes = 0
	} else {
		c.bufferedBytes -= cost
	}
	c.mu.Unlock()

	c.signalSpace()
}

// signalSpace wakes one submitter waiting for room, if any. The token is
// coalesced: with no waiter it stays until the next submit that has to wait.
func (c *coalescer) signalSpace() {
	select {
	case c.space <- types.Unit{}:
	default:
	}
}

// coalescedMessageBytes estimates the bytes retained by one queued message,
// including its enclosing repeated-field tag and length prefix.
func coalescedMessageBytes(msg *internalpb.RemoteMessage) uint64 {
	return uint64(proto.Size(msg)) + tellBatchPerMessageOverhead
}

// close prevents new submissions, ensures a writer drains whatever is still
// buffered, and blocks until the writer exits. Safe to call multiple times.
// A submit admits its message under the same lock that sets closed, so every
// message accepted before close has a running writer covered by the Wait here.
func (c *coalescer) close() {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true

		// A live writer drains until the buffer is dry on its own. Only when
		// none is running does close spawn the final drain itself. submit
		// never leaves messages queued without a writer; messages placed
		// with push alone (test fixtures) are still flushed here.
		needDrain := !c.running && c.queued > 0
		if needDrain {
			c.running = true
			c.wg.Add(1)
		}

		c.mu.Unlock()
		close(c.done)

		if needDrain {
			go c.run()
		}
	})

	c.wg.Wait()
}

// sendBatch flushes one batch through flushBatch and fans failures out to
// the error handler. It returns the batch reset to length zero with its
// entries cleared, so the caller reuses the backing array for the next batch.
func (c *coalescer) sendBatch(batch []*internalpb.RemoteMessage) []*internalpb.RemoteMessage {
	if len(batch) == 0 {
		return batch
	}

	req := &internalpb.RemoteTellRequest{}
	req.SetRemoteMessages(batch)
	// Per-message propagation metadata is already carried inside each
	// RemoteMessage, so the batch context only needs a transport deadline.
	ctx, cancel := context.WithTimeout(context.Background(), c.flushTimeout)
	undelivered, err := c.flushBatch(ctx, req)
	cancel()

	if err != nil && c.errHandler != nil {
		if undelivered == nil {
			undelivered = batch
		}

		// Hand the handler its own slice. We clear batch's entries below
		// so the underlying array can be reused for the next flush; if
		// the handler retains its argument (async logging, buffering,
		// etc.) it must not see those entries nil out from under it.
		handed := make([]*internalpb.RemoteMessage, len(undelivered))
		copy(handed, undelivered)
		c.errHandler(c.dest, handed, err)
	}

	var released uint64
	for i := range batch {
		released += coalescedMessageBytes(batch[i])
		batch[i] = nil
	}
	c.releaseBytes(released)

	return batch[:0]
}

// take moves up to maxBatch of the oldest queued messages into batch without
// waiting. When it frees slots in a full queue it signals a submitter waiting
// for room.
func (c *coalescer) take(batch []*internalpb.RemoteMessage) []*internalpb.RemoteMessage {
	c.mu.Lock()

	wasFull := c.queued == len(c.queue)
	n := min(c.queued, c.maxBatch-len(batch))
	first := min(n, len(c.queue)-c.head)

	batch = append(batch, c.queue[c.head:c.head+first]...)
	batch = append(batch, c.queue[:n-first]...)
	clear(c.queue[c.head : c.head+first])
	clear(c.queue[:n-first])
	c.head = (c.head + n) % len(c.queue)
	c.queued -= n

	c.mu.Unlock()

	if wasFull && n > 0 {
		c.signalSpace()
	}

	return batch
}

// drainUntilDry flushes buffered messages until the buffer is observably
// empty under mu, then hands the writer role back. The caller must own the
// role (running set true under mu) so at most one drainer touches the buffer
// and submission order is preserved.
func (c *coalescer) drainUntilDry() {
	batch := make([]*internalpb.RemoteMessage, 0, c.maxBatch)

	for {
		batch = c.take(batch)
		batch = c.sendBatch(batch)

		c.mu.Lock()

		if c.queued == 0 {
			c.running = false
			c.mu.Unlock()
			return
		}

		c.mu.Unlock()
	}
}

// lingerWhenDry reports whether the queue is empty and, when it is, marks the
// writer as lingering under the same lock, so the next submit sees the mark
// and wakes it.
func (c *coalescer) lingerWhenDry() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.queued > 0 {
		return false
	}

	c.lingering = true
	return true
}

// run is the transient writer loop. It drains queued messages (up to maxBatch
// per batch), sends each batch, and exits once the queue runs dry. While a
// send RPC is in flight, new submits accumulate in the queue and are picked
// up as the next batch. No timer delays a send: a single message against an
// idle destination is sent immediately, and an idle destination holds no
// goroutine at all.
func (c *coalescer) run() {
	defer c.wg.Done()

	batch := make([]*internalpb.RemoteMessage, 0, c.maxBatch)

	linger := time.NewTimer(c.linger)
	defer linger.Stop()

	for {
		batch = c.take(batch)
		batch = c.sendBatch(batch)

		if !c.lingerWhenDry() {
			continue
		}

		// Dry: park briefly for the next message before handing the writer
		// role back, so steady request/response traffic pays a channel wake
		// instead of a goroutine spawn per message.
		if !linger.Stop() {
			select {
			case <-linger.C:
			default:
			}
		}

		linger.Reset(c.linger)

		select {
		case <-c.ready:
			continue
		case <-c.done:
			// Drain fully before exiting: the queue can hold several
			// batches worth of messages when close races a burst of
			// submits, and close promises everything buffered is flushed.
			c.drainUntilDry()
			return
		case <-linger.C:
		}

		// Exit only via the running flag under mu: either the queue is dry
		// and the writer hands its role back (a later submit re-spawns), or
		// a message raced in and this writer keeps draining. Submit appends
		// and checks running under the same lock, so a message never lands
		// unseen.
		c.mu.Lock()
		c.lingering = false

		if c.queued == 0 {
			c.running = false
			c.mu.Unlock()
			return
		}

		c.mu.Unlock()
	}
}
