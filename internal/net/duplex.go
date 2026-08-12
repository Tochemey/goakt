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

package net

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
)

// duplexWriteBatchMax is the maximum number of frames the writer coalesces
// into one vectored WriteFrames call per wakeup.
const duplexWriteBatchMax = 32

// duplexDispatchLinger is how long a dry pipelined-dispatch drainer parks for
// the next frame before exiting. Request/response traffic keeps one hot
// drainer and pays a channel wake instead of a goroutine spawn per frame; a
// connection quiet past the linger holds no dispatch goroutine.
const duplexDispatchLinger = 10 * time.Millisecond

// duplexCloseDrainGrace is used when writeTimeout is unset so Close cannot
// block forever on a peer that stopped reading.
const duplexCloseDrainGrace = 5 * time.Second

// duplexLivenessMissLimit is the number of consecutive admitted liveness
// probes that may each pass a full idle interval unanswered before the
// transport is failed (the classic two-missed-PONG rule).
const duplexLivenessMissLimit = 2

// ErrDuplexClosed is returned when submitting to or reading from a closed
// duplex connection.
var ErrDuplexClosed = errors.New("tcp: duplex connection is closed")

// ErrDuplexBackpressure is returned when the outbound queue cannot accept a
// frame within the caller's deadline.
var ErrDuplexBackpressure = errors.New("tcp: duplex outbound queue full")

// duplexConn owns one reader goroutine, one writer goroutine, a byte-bounded
// outbound queue, and a correlation pending table over a [FramedConn].
// It implements [DuplexSession].
type duplexConn struct {
	// framed is the underlying framed connection (post-HELLO, post-compression).
	framed FramedConn
	// maxOutBytes caps admitted outbound header+payload bytes (InitialCredits).
	maxOutBytes int64
	// writeTimeout bounds Submit when the caller's context has no deadline.
	writeTimeout time.Duration
	// readIdleTimeout is the interval without inbound activity before probing.
	readIdleTimeout time.Duration
	// connIdleTimeout is the server reclaim window. When nonzero, readLoop
	// refreshes the socket read deadline on every frame (including PING/PONG)
	// so liveness traffic keeps an otherwise-idle duplex connection alive.
	connIdleTimeout time.Duration
	// lane is the negotiated connection lane.
	lane byte
	// enforceLane enables frame.Lane validation. Set by [withDuplexLane]
	// after HELLO; unit fixtures that skip negotiation leave it false.
	enforceLane bool
	// revision is the negotiated capability revision from HELLO.
	revision uint32
	// maxFrameSize is the negotiated whole-frame ceiling.
	maxFrameSize uint32
	// maxMessageSize is the negotiated reassembled logical-frame ceiling.
	maxMessageSize uint64
	// maxConcurrentLargeTransfers is the negotiated concurrent CHUNK-group cap.
	maxConcurrentLargeTransfers uint32
	// chunkSize is the local send threshold for splitting into CHUNK frames.
	// It is not negotiated; zero disables chunked sends.
	chunkSize uint32
	// reassembler rebuilds inbound CHUNK groups on this connection.
	reassembler *chunkReassembler
	// largeSem gates outbound CHUNK groups at the negotiated concurrent cap.
	largeSem chan types.Unit
	// pathSender assigns outbound actor-path table IDs when revision >= 3.
	pathSender *senderTable
	// typeSender assigns outbound type-name table IDs when revision >= 3.
	typeSender *senderTable
	// pathReceiver resolves inbound actor-path table IDs when revision >= 3.
	pathReceiver *receiverTable
	// typeReceiver resolves inbound type-name table IDs when revision >= 3.
	typeReceiver *receiverTable
	// senderResolver lazily materializes opaque sender handles for path
	// table hits. Nil leaves SenderHandle empty (clients and unit tests).
	senderResolver func(path string) any
	// creditEnabled is true when the negotiated revision supports receiver
	// grants (revision >= 4). When false the send window is unlimited.
	creditEnabled bool
	// creditWindow is the negotiated HELLO credit budget used as the full
	// window size for oversized-frame progress and grant batching.
	creditWindow int64
	// sendWindow is the remaining receiver-granted outbound budget. It is
	// decremented by the writer at write time and increased by inbound CREDIT.
	sendWindow atomic.Int64
	// creditWake wakes a writer parked on an exhausted send window.
	creditWake chan types.Unit
	// grantAccum is owned wire bytes not yet flushed as a CREDIT frame.
	// Atomic instead of mutex-guarded: the receive path records ownership
	// once per frame, so the accumulator must cost one uncontended atomic
	// add, not a lock acquisition, per delivered message.
	grantAccum atomic.Int64
	// lastInbound records the latest successfully read frame timestamp.
	lastInbound atomic.Int64
	// inboundHandler, when set before the loops start, consumes non-correlated
	// application frames instead of queueing them for [duplexConn.Recv]. It
	// owns each frame's payload release. Without pipelinedInbound it runs
	// directly on the read loop and must not block beyond mailbox-enqueue
	// work: the read loop cannot make progress until it returns. Sessions
	// with a handler must not call Recv.
	inboundHandler func(session DuplexSession, frame Frame)
	// pipelinedInbound, when set with inboundHandler, keeps the read loop
	// free of dispatch work: frames are queued on the inbound channel and a
	// transient drainer goroutine invokes the handler in arrival order. The
	// drainer exists only while frames are queued (an idle connection holds
	// no dispatch goroutine), so under sustained load the socket is read
	// concurrently with dispatch, restoring the throughput of a dedicated
	// serve goroutine at zero steady-state goroutine cost.
	pipelinedInbound bool
	// dispatchMu guards dispatchRunning: either the live drainer's dry check
	// sees a queued frame, or the read loop's wake observes running == false
	// and spawns a fresh drainer, so no queued frame is ever left unowned.
	dispatchMu sync.Mutex
	// dispatchRunning is true while a transient drainer owns the inbound
	// queue.
	dispatchRunning bool
	// dispatchWG tracks every transient drainer so the read loop can join
	// pipelined dispatch on exit. Without the join, Close and the closed
	// handler (which retires server-side accounting) could complete while a
	// drainer is still delivering frames. Add happens only on the read
	// goroutine (the sole drainer spawner), so it can never race the read
	// loop's deferred Wait.
	dispatchWG sync.WaitGroup
	// buffered exposes the framed connection's read-buffer occupancy when
	// the transport has one; nil otherwise. Pipelined dispatch reads it (on
	// the read loop only) to decide between inline and queued delivery.
	buffered interface{ BufferedReadBytes() int }
	// closedHandler, when set before the loops start, runs exactly once on the
	// read loop as it exits, on every teardown path (local Close, transport
	// failure, peer disconnect). The session is already closed when it runs,
	// so it must not call Close synchronously (Close waits for the read loop).
	closedHandler func(session DuplexSession)

	mu sync.Mutex
	// space wakes Submit waiters when outbound capacity is released.
	space *sync.Cond
	// outBytes is the currently admitted outbound byte cost under mu.
	outBytes int64
	// out is the writer queue of admitted frames.
	out chan Frame
	// inbound delivers non-correlated application frames to [Recv].
	inbound chan Frame

	// pending correlates Ask waiters by correlation ID.
	pending *pendingTable
	// nextCorr allocates nonzero correlation IDs for Ask (starts at 1).
	nextCorr atomic.Uint64

	closeOnce sync.Once
	// closed is closed exactly once to signal shutdown to loops and waiters.
	closed chan types.Unit
	// closing records that the local side initiated Close, so loop errors
	// caused by tearing down our own connection are not recorded as peer
	// failures. Errors observed while closing is false are genuine.
	closing atomic.Bool
	// closeDone ensures [Close] runs its teardown sequence at most once and
	// subsequent callers observe the same stored error.
	closeDone sync.Once
	// framedCloseOnce closes the underlying framed connection at most once
	// from either [Close] or [failTransport].
	framedCloseOnce sync.Once
	// closeResult stores the first [Close] outcome for idempotent returns.
	closeResult atomic.Pointer[error]
	// writeWG / readWG track the writer and reader loops for ordered shutdown:
	// drain writes, then close framed to unblock the reader.
	writeWG  sync.WaitGroup
	readWG   sync.WaitGroup
	writeErr atomic.Pointer[error]
	readErr  atomic.Pointer[error]
}

// duplexConnOption configures a [duplexConn].
type duplexConnOption func(*duplexConn)

// withDuplexWriteTimeout sets the bound applied by [duplexConn.Submit] when
// the caller's context carries no deadline.
func withDuplexWriteTimeout(d time.Duration) duplexConnOption {
	return func(x *duplexConn) {
		x.writeTimeout = d
	}
}

// withDuplexReadIdleTimeout sets the idle interval for correlated PING probes.
func withDuplexReadIdleTimeout(d time.Duration) duplexConnOption {
	return func(x *duplexConn) {
		x.readIdleTimeout = d
	}
}

// withDuplexLane sets the negotiated connection lane and enables inbound
// frame.Lane validation against that value.
func withDuplexLane(lane byte) duplexConnOption {
	return func(x *duplexConn) {
		x.lane = lane
		x.enforceLane = true
	}
}

// withDuplexConnIdleTimeout sets the connection reclaim window enforced via
// socket read deadlines refreshed on every inbound frame.
func withDuplexConnIdleTimeout(d time.Duration) duplexConnOption {
	return func(x *duplexConn) {
		x.connIdleTimeout = d
	}
}

// withDuplexChunkSize sets the local CHUNK send threshold. Zero disables
// chunked sends on this session.
func withDuplexChunkSize(size uint32) duplexConnOption {
	return func(x *duplexConn) {
		x.chunkSize = size
	}
}

// withDuplexNegotiated applies HELLO pairwise-effective limits and enables
// chunk reassembly and compression tables when the negotiated revision
// supports them.
func withDuplexNegotiated(hello *internalpb.Hello) duplexConnOption {
	return func(x *duplexConn) {
		if hello == nil {
			return
		}

		x.revision = hello.GetRevision()
		x.maxFrameSize = hello.GetMaxFrameSize()
		x.maxMessageSize = hello.GetMaxMessageSize()
		x.maxConcurrentLargeTransfers = hello.GetMaxConcurrentLargeTransfers()
		if x.maxConcurrentLargeTransfers == 0 {
			x.maxConcurrentLargeTransfers = defaultMaxConcurrentLargeTransfers
		}

		if x.revision >= CapabilityRevisionChunking {
			x.reassembler = newChunkReassembler(x.maxMessageSize, x.maxConcurrentLargeTransfers)
			x.largeSem = make(chan types.Unit, x.maxConcurrentLargeTransfers)
		}

		if x.revision >= CapabilityRevisionTables {
			x.pathSender = newSenderTable(DefaultTableCapacity)
			x.typeSender = newSenderTable(DefaultTableCapacity)
			x.pathReceiver = newReceiverTable(DefaultTableCapacity)
			x.typeReceiver = newReceiverTable(DefaultTableCapacity)
		}

		if x.revision >= CapabilityRevisionCredits {
			x.creditEnabled = true
		}
	}
}

// withDuplexSenderResolver installs the actor-layer hook used to materialize
// opaque sender handles on path table hits.
func withDuplexSenderResolver(resolve func(path string) any) duplexConnOption {
	return func(x *duplexConn) {
		x.senderResolver = resolve
	}
}

// withDuplexInboundHandler routes non-correlated application frames to fn on
// the read loop instead of the Recv queue. See [duplexConn.inboundHandler]
// for the handler contract.
func withDuplexInboundHandler(fn func(session DuplexSession, frame Frame)) duplexConnOption {
	return func(x *duplexConn) {
		x.inboundHandler = fn
	}
}

// withDuplexPipelinedInbound decouples the inbound handler from the read
// loop: frames queue on the inbound channel and a transient drainer invokes
// the handler in arrival order. See [duplexConn.pipelinedInbound].
func withDuplexPipelinedInbound() duplexConnOption {
	return func(x *duplexConn) {
		x.pipelinedInbound = true
	}
}

// withDuplexClosedHandler registers fn to run exactly once as the read loop
// exits on any teardown path. See [duplexConn.closedHandler] for the handler
// contract.
func withDuplexClosedHandler(fn func(session DuplexSession)) duplexConnOption {
	return func(x *duplexConn) {
		x.closedHandler = fn
	}
}

// newDuplexConn starts reader and writer goroutines for framed.
// maxOutBytes caps admitted outbound payload+header bytes; values <= 0
// default to defaultMaxFrameSize.
func newDuplexConn(framed FramedConn, maxOutBytes int64, opts ...duplexConnOption) *duplexConn {
	if maxOutBytes <= 0 {
		maxOutBytes = int64(defaultMaxFrameSize)
	}

	x := &duplexConn{
		framed:      framed,
		maxOutBytes: maxOutBytes,
		out:         make(chan Frame, 64),
		inbound:     make(chan Frame, 64),
		closed:      make(chan types.Unit),
		pending:     newPendingTable(),
		lane:        LaneControl,
	}
	x.buffered, _ = framed.(interface{ BufferedReadBytes() int })
	x.space = sync.NewCond(&x.mu)
	x.nextCorr.Store(1)

	for _, opt := range opts {
		opt(x)
	}

	// A peer may negotiate a max frame size below the local chunk threshold.
	// Clamp so chunked sends never emit a frame the peer would reject with a
	// read error; splitLogicalChunks caps chunk bodies at this value.
	if x.chunkSize > 0 && x.maxFrameSize > 0 && x.chunkSize > x.maxFrameSize {
		x.chunkSize = x.maxFrameSize
	}

	if x.creditEnabled {
		x.creditWindow = x.maxOutBytes
		x.sendWindow.Store(x.maxOutBytes)
		x.creditWake = make(chan types.Unit, 1)
	}

	x.lastInbound.Store(time.Now().UnixNano())

	x.writeWG.Add(1)
	x.readWG.Add(1)
	go x.writeLoop()
	go x.readLoop()
	return x
}

// Tell enqueues a fire-and-forget frame. Correlation is forced to zero and
// the expectsReply flag is cleared. Oversized frames are chunked when the
// negotiated revision supports it.
func (x *duplexConn) Tell(ctx context.Context, frame Frame) error {
	frame.Correlation = 0
	frame.Flags &^= FrameFlagExpectsReply
	return x.Submit(ctx, frame)
}

// Ask assigns a correlation ID, registers a waiter, submits frame with
// expectsReply, and blocks until a REPLY/ERROR arrives, ctx is done, or the
// duplex closes. Timeout abandons the waiter so a late reply is dropped.
// Oversized frames are chunked when the negotiated revision supports it; a
// soft-reject ERROR during chunk admission completes the waiter.
func (x *duplexConn) Ask(ctx context.Context, frame Frame) (Frame, error) {
	corr := x.nextCorrelation()
	frame.Correlation = corr
	frame.Flags |= FrameFlagExpectsReply
	if frame.Version == 0 {
		frame.Version = ProtocolVersion
	}

	// Channel ownership: receiving from wait transfers ownership to this
	// goroutine (pool after use). On the abandon paths, abandon pools the
	// channel only when it wins the slot; a lost race means a concurrent
	// complete may still send, so the channel is left to the GC (see
	// [pendingTable.register]).
	wait := x.pending.register(corr)
	if err := x.Submit(ctx, frame); err != nil {
		select {
		case resp := <-wait:
			putPendingWaiter(wait)
			if resp.Type == FrameTypeError {
				return resp, decodeErrorPayload(resp.Payload)
			}
			return resp, nil
		default:
			_ = x.pending.abandon(corr)
			return Frame{}, err
		}
	}

	select {
	case resp := <-wait:
		putPendingWaiter(wait)
		if resp.Type == FrameTypeError {
			return resp, decodeErrorPayload(resp.Payload)
		}
		return resp, nil
	case <-ctx.Done():
		_ = x.pending.abandon(corr)
		return Frame{}, ctx.Err()
	case <-x.closed:
		_ = x.pending.abandon(corr)
		return Frame{}, x.closedError()
	}
}

// Submit enqueues frame for writing. DATA and REPLY frames are split into
// CHUNK frames when they exceed the local chunk size and the peer supports
// chunking. It blocks until there is byte capacity, ctx is done, or the
// duplex is closed. When ctx has no deadline and a write timeout is
// configured, that timeout bounds the wait. A canceled context returns
// ctx.Err(); a deadline that expires while waiting for capacity returns
// [ErrDuplexBackpressure].
func (x *duplexConn) Submit(ctx context.Context, frame Frame) error {
	switch frame.Type {
	case FrameTypeData, FrameTypeReply:
		return x.submitLogical(ctx, frame)
	default:
		return x.submitRaw(ctx, frame)
	}
}

// submitRaw enqueues a single wire frame without chunking.
func (x *duplexConn) submitRaw(ctx context.Context, frame Frame) error {
	ctx, cancel := x.bindWriteDeadline(ctx)
	defer cancel()

	if frame.Version == 0 {
		frame.Version = ProtocolVersion
	}

	if int(frame.Length) != frame.bodyLen() {
		frame.Length = uint32(frame.bodyLen())
	}

	cost := int64(FrameHeaderSize) + int64(frame.Length)

	// The ctx wake-up hook is registered lazily, only when Submit must wait
	// for capacity, so the uncontended fast path stays allocation free.
	var stop func() bool

	defer func() {
		if stop != nil {
			stop()
		}
	}()

	x.mu.Lock()
	for {
		if x.isClosed() {
			x.mu.Unlock()
			return ErrDuplexClosed
		}

		if x.canAdmitLocked(cost, frame.Type) {
			x.outBytes += cost
			x.mu.Unlock()

			select {
			case x.out <- frame:
				return nil
			case <-x.closed:
				x.release(cost)
				return ErrDuplexClosed
			case <-ctx.Done():
				x.release(cost)
				return submitContextError(ctx)
			}
		}

		if err := ctx.Err(); err != nil {
			x.mu.Unlock()
			return submitContextError(ctx)
		}

		if stop == nil {
			stop = context.AfterFunc(ctx, func() {
				x.mu.Lock()
				x.space.Broadcast()
				x.mu.Unlock()
			})
		}

		x.space.Wait()
	}
}

// Recv returns the next inbound frame that is not a correlated REPLY/ERROR
// (those complete [Ask] waiters). Buffered inbound frames are drained before
// a closed connection error so shutdown does not drop already-delivered frames.
// Callers that drop or finish consuming the frame must [ReleasePayload].
func (x *duplexConn) Recv(ctx context.Context) (Frame, error) {
	select {
	case frame := <-x.inbound:
		return frame, nil
	default:
	}

	select {
	case frame := <-x.inbound:
		return frame, nil
	case <-ctx.Done():
		return Frame{}, ctx.Err()
	case <-x.closed:
		select {
		case frame := <-x.inbound:
			return frame, nil
		default:
			return Frame{}, x.closedError()
		}
	}
}

// ReleasePayload returns frame.Payload to the framed connection's read pool.
// Safe on non-pooled buffers and empty payloads.
func (x *duplexConn) ReleasePayload(frame Frame) {
	x.releaseFramePayload(frame.Payload)
}

// Close stops admitting new frames, drains the outbound queue (so a final
// ERROR can reach the peer), then closes the underlying framed connection.
// When the writer failed, that error is returned. Repeated calls are no-ops
// that return the first close error.
func (x *duplexConn) Close() error {
	x.closeDone.Do(func() {
		x.closing.Store(true)
		x.signalClose()

		drainBudget := x.writeTimeout
		if drainBudget <= 0 {
			drainBudget = duplexCloseDrainGrace
		}

		done := make(chan types.Unit)
		go func() {
			x.writeWG.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(drainBudget):
			// Peer stopped reading: interrupt the blocked WriteFrames so
			// shutdown cannot hang indefinitely.
			_ = x.closeFramed()
			<-done
		}

		closeErr := x.closeFramed()
		x.readWG.Wait()

		if errPtr := x.writeErr.Load(); errPtr != nil {
			closeErr = *errPtr
		}
		x.closeResult.Store(&closeErr)
	})

	if errPtr := x.closeResult.Load(); errPtr != nil {
		return *errPtr
	}
	return nil
}

// signalClose marks the duplex closed and wakes waiters without tearing down
// the framed connection. The writer drains queued frames first; [Close] then
// closes the framed connection to unblock the reader.
func (x *duplexConn) signalClose() {
	x.closeOnce.Do(func() {
		close(x.closed)
		x.mu.Lock()
		x.space.Broadcast()
		x.mu.Unlock()
		x.pending.failAll(Frame{Type: FrameTypeError})
		if x.reassembler != nil {
			x.reassembler.Close()
		}
	})
}

// release returns cost bytes to the outbound budget and wakes every waiting
// Submit so none can miss a capacity change.
func (x *duplexConn) release(cost int64) {
	x.mu.Lock()
	x.outBytes -= cost
	if x.outBytes < 0 {
		x.outBytes = 0
	}
	x.space.Broadcast()
	x.mu.Unlock()
}

// writeLoop drains the outbound queue onto the framed connection until the
// duplex closes or a write fails. Ready frames are coalesced into one
// vectored WriteFrames call per wakeup. When credits are enabled, windowed
// frames (DATA/CHUNK) wait for send-window room while exempt control frames
// bypass a parked writer. On shutdown it flushes any frames already admitted
// so connection-scoped ERROR reaches the peer.
func (x *duplexConn) writeLoop() {
	defer x.writeWG.Done()

	batch := make([]Frame, 0, duplexWriteBatchMax)
	costs := make([]int64, 0, duplexWriteBatchMax)

	var pending []Frame
	if x.creditEnabled {
		pending = make([]Frame, 0, duplexWriteBatchMax)
	}

	for {
		batch = batch[:0]
		costs = costs[:0]

		if !x.waitOutbound(&pending, &batch, &costs) {
			x.drainOutboundPending(pending)
			return
		}

		x.drainOutboundNonblocking(&pending, &batch, &costs)
		if x.creditEnabled {
			pending, batch, costs = x.fillWindowedBatch(pending, batch, costs)
		}

		if len(batch) == 0 {
			// Classification moved frames out of the queue even though nothing
			// was written; retry any deferred grant now that slots are free.
			x.flushGrants()
			continue
		}

		x.applyWriteDeadline()
		if err := x.framed.WriteFrames(batch...); err != nil {
			if !x.closing.Load() {
				x.writeErr.Store(&err)
			}

			x.failTransport()
			return
		}

		for _, cost := range costs {
			x.release(cost)
		}

		// The write freed queue slots; retry any CREDIT grant that was deferred
		// on a full queue so a parked peer is never stranded without credit.
		x.flushGrants()
	}
}

// waitOutbound blocks until at least one outbound frame is classified into
// batch/pending, a credit wake arrives while parked, or the duplex closes.
// It returns false when the connection is closed.
func (x *duplexConn) waitOutbound(pending *[]Frame, batch *[]Frame, costs *[]int64) bool {
	if x.creditEnabled && len(*pending) > 0 {
		// A pending head that fits the current window must proceed without
		// parking: the prior iteration may have stopped at the write-batch
		// frame cap, and credit wakes coalesce (capacity-1 channel), so no
		// further wake is guaranteed to arrive for the remaining backlog.
		if x.canChargeWindow(frameWireCost((*pending)[0])) {
			return true
		}

		select {
		case <-x.closed:
			return false
		case frame := <-x.out:
			*pending, *batch, *costs = x.classifyOutbound(frame, *pending, *batch, *costs)
			return true
		case <-x.creditWake:
			return true
		}
	}

	select {
	case <-x.closed:
		return false
	case frame := <-x.out:
		if x.creditEnabled {
			*pending, *batch, *costs = x.classifyOutbound(frame, *pending, *batch, *costs)
		} else {
			*batch = append(*batch, frame)
			*costs = append(*costs, frameWireCost(frame))
		}

		return true
	}
}

// drainOutboundNonblocking pulls ready frames from the writer queue without
// blocking, classifying them for credit-aware writes when enabled.
func (x *duplexConn) drainOutboundNonblocking(pending *[]Frame, batch *[]Frame, costs *[]int64) {
	for len(*batch) < duplexWriteBatchMax {
		select {
		case frame := <-x.out:
			if x.creditEnabled {
				*pending, *batch, *costs = x.classifyOutbound(frame, *pending, *batch, *costs)
			} else {
				*batch = append(*batch, frame)
				*costs = append(*costs, frameWireCost(frame))
			}
		default:
			return
		}
	}
}

// classifyOutbound routes frame into the exempt write batch or the windowed
// pending buffer. Exempt frames may overtake parked windowed frames.
func (x *duplexConn) classifyOutbound(frame Frame, pending, batch []Frame, costs []int64) ([]Frame, []Frame, []int64) {
	if !windowedFrameType(frame.Type) {
		batch = append(batch, frame)
		costs = append(costs, frameWireCost(frame))
		return pending, batch, costs
	}

	pending = append(pending, frame)
	return pending, batch, costs
}

// fillWindowedBatch moves the longest prefix of pending windowed frames that
// fit the send window into batch, charging the window at move time. Consumed
// pending slots are cleared so large payloads are not retained by the slice
// backing array after the frame has been handed to the writer batch.
func (x *duplexConn) fillWindowedBatch(pending, batch []Frame, costs []int64) ([]Frame, []Frame, []int64) {
	for len(pending) > 0 && len(batch) < duplexWriteBatchMax {
		cost := frameWireCost(pending[0])
		if !x.canChargeWindow(cost) {
			break
		}

		x.chargeWindow(cost)
		batch = append(batch, pending[0])
		costs = append(costs, cost)
		pending[0] = Frame{}
		pending = pending[1:]
	}

	return pending, batch, costs
}

// drainOutboundPending writes pending windowed frames then every remaining
// admitted frame. Shutdown ignores the send window so a best-effort ERROR can
// leave even when the peer has stopped granting.
func (x *duplexConn) drainOutboundPending(pending []Frame) {
	for i := range pending {
		frame := pending[i]
		pending[i] = Frame{}

		x.applyWriteDeadline()
		if err := x.framed.WriteFrames(frame); err != nil {
			if !x.closing.Load() {
				x.writeErr.Store(&err)
			}

			return
		}

		x.release(frameWireCost(frame))
	}

	for {
		select {
		case frame := <-x.out:
			x.applyWriteDeadline()
			if err := x.framed.WriteFrames(frame); err != nil {
				if !x.closing.Load() {
					x.writeErr.Store(&err)
				}

				return
			}

			x.release(frameWireCost(frame))
		default:
			return
		}
	}
}

// readLoop pumps inbound frames from the framed connection. Correlated
// REPLY/ERROR/PONG frames complete registered waiters; PING frames receive a
// best-effort PONG response. Late correlated frames are dropped. Everything
// else is delivered on the inbound channel for [Recv].
func (x *duplexConn) readLoop() {
	defer x.readWG.Done()

	if x.closedHandler != nil {
		// Runs before readWG.Done (LIFO): the session is fully closed on
		// every exit path below, so the handler observes IsClosed() == true.
		defer x.closedHandler(x)
	}

	// Join pipelined dispatch before the closed handler retires accounting
	// and before readWG.Done releases Close: the detached-duplex contract is
	// that both observe dispatch fully drained, not merely the socket loop
	// gone. Every exit path below fires the close signal, so a live drainer
	// always terminates; no handler on the drainer blocks on this loop, so
	// the join cannot deadlock. Runs first in the defer chain (LIFO).
	defer x.dispatchWG.Wait()

	// A read deadline that fired mid-frame leaves ReadFrame's resume state
	// holding a pooled payload; the timeout exit paths below (liveness miss
	// limit, idle reclaim) never call ReadFrame again, so hand the buffer
	// back here. This goroutine is the sole ReadFrame caller, so the resume
	// state cannot be in use. Terminal read errors already cleaned up inside
	// ReadFrame; the call is then a no-op.
	if resumable, ok := x.framed.(interface{ AbandonPendingRead() }); ok {
		defer resumable.AbandonPendingRead()
	}

	// Liveness state, folded from the retired dedicated prober: an expired
	// read deadline on an idle connection emits the PING and re-arms; two
	// admitted probes that each pass a full idle interval with no inbound
	// traffic of any kind close the transport. A miss is counted only one
	// interval after its probe was admitted, so a peer always gets a full
	// interval to answer the second probe before the transport fails. Any
	// inbound frame (including a PONG) resets the cycle.
	var livenessMisses int
	var probeOutstanding bool
	idleAt := time.Now().Add(x.readIdleTimeout)

	for {
		x.armReadDeadline(idleAt)

		frame, err := x.framed.ReadFrame()
		if err != nil {
			if x.readIdleTimeout > 0 && isTimeoutError(err) && !x.reclaimExpired() {
				if probeOutstanding {
					livenessMisses++

					if livenessMisses >= duplexLivenessMissLimit {
						x.failTransport()
						return
					}
				}

				// Track admission only: a backpressure drop must not punish
				// a peer that never received the PING.
				probeOutstanding = x.trySubmit(Frame{
					Version:     ProtocolVersion,
					Type:        FrameTypePing,
					Lane:        x.lane,
					Correlation: x.nextCorrelation(),
				})
				idleAt = time.Now().Add(x.readIdleTimeout)
				continue
			}

			if !x.closing.Load() {
				x.readErr.Store(&err)
			}

			x.failTransport()
			return
		}

		x.lastInbound.Store(time.Now().UnixNano())
		livenessMisses = 0
		probeOutstanding = false
		idleAt = time.Now().Add(x.readIdleTimeout)

		if x.enforceLane && frame.Lane != x.lane {
			x.releaseFramePayload(frame.Payload)
			x.rejectWrongLane()
			return
		}

		switch frame.Type {
		case FrameTypePing:
			_ = x.trySubmit(Frame{
				Version:     ProtocolVersion,
				Type:        FrameTypePong,
				Lane:        x.lane,
				Correlation: frame.Correlation,
			})
			continue
		case FrameTypePong:
			if frame.Correlation != 0 {
				_ = x.pending.complete(frame.Correlation, frame)
			}
			continue
		case FrameTypeCredit:
			x.handleInboundCredit(frame)
			continue
		case FrameTypeChunk:
			x.handleInboundChunk(frame)
			continue
		case FrameTypeTable:
			x.handleInboundTable(frame)
			continue
		}

		if frame.Type == FrameTypeReply || frame.Type == FrameTypeError {
			if frame.Correlation != 0 {
				// Timeout abandons the waiter; a late REPLY/ERROR must not
				// fill inbound or it will stall the reader after 64 drops.
				if !x.pending.complete(frame.Correlation, frame) {
					x.releaseFramePayload(frame.Payload)
				}

				continue
			}
		}

		if x.inboundHandler != nil {
			x.deliverInbound(frame)
			continue
		}

		select {
		case <-x.closed:
			return
		case x.inbound <- frame:
		}
	}
}

// deliverInbound routes one frame to the inbound handler. Non-pipelined
// sessions always dispatch inline on the read loop. Pipelined sessions
// dispatch inline only while the connection is quiet (no queued frames, no
// live drainer, and no further bytes already buffered), which keeps
// request/response latency free of goroutine handoffs; once frames arrive
// faster than dispatch consumes them, delivery shifts to the queue and its
// transient drainer so socket reads proceed concurrently with dispatch.
func (x *duplexConn) deliverInbound(frame Frame) {
	if !x.pipelinedInbound {
		x.inboundHandler(x, frame)
		return
	}

	if x.framedBuffered() == 0 && x.canDispatchInline() {
		x.inboundHandler(x, frame)
		return
	}

	select {
	case <-x.closed:
		// Teardown while the queue is full: drop the frame like the
		// pre-pipelining path did, returning its body to the read pool.
		x.releaseFramePayload(frame.Payload)
	case x.inbound <- frame:
		x.wakeDispatcher()
	}
}

// framedBuffered reports bytes already read into the framed connection's
// buffer, the load signal for pipelined dispatch. Zero when the transport
// carries no read buffer.
func (x *duplexConn) framedBuffered() int {
	if x.buffered == nil {
		return 0
	}

	return x.buffered.BufferedReadBytes()
}

// canDispatchInline reports whether the read loop may bypass the queue
// without reordering: nothing is queued and no drainer owns the queue. Only
// the read loop enqueues and spawns drainers, so a true result cannot be
// invalidated before the inline dispatch runs.
func (x *duplexConn) canDispatchInline() bool {
	x.dispatchMu.Lock()
	defer x.dispatchMu.Unlock()
	return !x.dispatchRunning && len(x.inbound) == 0
}

// wakeDispatcher ensures a transient drainer goroutine owns the inbound
// queue, spawning one when none is running. Called by the read loop after
// every pipelined enqueue.
func (x *duplexConn) wakeDispatcher() {
	x.dispatchMu.Lock()

	if x.dispatchRunning {
		x.dispatchMu.Unlock()
		return
	}

	x.dispatchRunning = true
	x.dispatchWG.Add(1)
	x.dispatchMu.Unlock()

	go x.dispatchLoop()
}

// dispatchLoop invokes the inbound handler for queued frames in arrival
// order until the queue runs dry and the linger expires, then exits; the
// read loop re-spawns it on the next enqueue. Exactly one drainer runs at a
// time, so pipelined dispatch preserves per-connection frame order.
func (x *duplexConn) dispatchLoop() {
	defer x.dispatchWG.Done()

	linger := time.NewTimer(duplexDispatchLinger)
	defer linger.Stop()

	for {
		select {
		case frame := <-x.inbound:
			x.inboundHandler(x, frame)
			continue
		default:
		}

		// Dry: park briefly for the next frame before handing the queue
		// back, so steady request/response traffic pays a channel wake
		// instead of a goroutine spawn per frame.
		if !linger.Stop() {
			select {
			case <-linger.C:
			default:
			}
		}

		linger.Reset(duplexDispatchLinger)

		select {
		case frame := <-x.inbound:
			x.inboundHandler(x, frame)
			continue
		case <-x.closed:
			x.drainInboundOnClose()
			return
		case <-linger.C:
		}

		x.dispatchMu.Lock()

		if len(x.inbound) == 0 {
			x.dispatchRunning = false
			x.dispatchMu.Unlock()
			return
		}

		x.dispatchMu.Unlock()
	}
}

// drainInboundOnClose dispatches whatever the read loop queued before it
// exited, then hands the queue back under the dispatch lock. Clearing
// dispatchRunning before returning is what keeps the no-stranded-frame
// invariant on teardown: the read loop may still enqueue frames it had
// already buffered, and its wakeDispatcher must find the queue unowned so a
// fresh drainer picks them up instead of the frame (and its pooled payload)
// sitting in the queue forever.
func (x *duplexConn) drainInboundOnClose() {
	for {
		select {
		case frame := <-x.inbound:
			x.inboundHandler(x, frame)
			continue
		default:
		}

		x.dispatchMu.Lock()

		if len(x.inbound) == 0 {
			x.dispatchRunning = false
			x.dispatchMu.Unlock()
			return
		}

		x.dispatchMu.Unlock()
	}
}

// armReadDeadline arms the socket read deadline to the sooner of the next
// liveness probe boundary (idleAt) and the connection reclaim boundary; no
// deadline is set when both mechanisms are disabled. Called by the read loop
// before every blocking read.
func (x *duplexConn) armReadDeadline(idleAt time.Time) {
	var deadline time.Time

	if x.readIdleTimeout > 0 {
		deadline = idleAt
	}

	if x.connIdleTimeout > 0 {
		reclaimAt := time.Unix(0, x.lastInbound.Load()).Add(x.connIdleTimeout)
		if deadline.IsZero() || reclaimAt.Before(deadline) {
			deadline = reclaimAt
		}
	}

	if deadline.IsZero() {
		return
	}

	_ = x.framed.NetConn().SetReadDeadline(deadline)
}

// reclaimExpired reports whether the connection idle reclaim window has
// passed with no inbound frame, which turns a read-deadline expiry into a
// terminal error (server idle reclaim) instead of a liveness probe.
func (x *duplexConn) reclaimExpired() bool {
	if x.connIdleTimeout <= 0 {
		return false
	}

	return time.Since(time.Unix(0, x.lastInbound.Load())) >= x.connIdleTimeout
}

// isTimeoutError reports whether err is a read-deadline expiry rather than a
// terminal transport failure.
func isTimeoutError(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// rejectWrongLane emits a best-effort connection-scoped ERROR then tears down
// the transport. Called from the reader: admit via trySubmit, signal writer
// drain, then close the socket so the ERROR can leave before teardown.
func (x *duplexConn) rejectWrongLane() {
	error2 := &internalpb.Error{}
	error2.SetCode(internalpb.Code_CODE_FAILED_PRECONDITION)
	error2.SetMessage("frame lane does not match connection lane")
	payload, err := proto.Marshal(error2)
	if err == nil {
		_ = x.trySubmit(Frame{
			Version: ProtocolVersion,
			Type:    FrameTypeError,
			Lane:    x.lane,
			Length:  uint32(len(payload)),
			Payload: payload,
		})
	}

	x.drainAndCloseFramed()
}

// drainAndCloseFramed signals shutdown, gives the writer a bounded window to
// flush a best-effort ERROR, then closes the framed connection.
func (x *duplexConn) drainAndCloseFramed() {
	x.closing.Store(true)
	x.signalClose()

	done := make(chan types.Unit, 1)
	go func() {
		x.writeWG.Wait()
		done <- types.Unit{}
	}()

	drainBudget := x.writeTimeout
	if drainBudget <= 0 {
		drainBudget = duplexCloseDrainGrace
	}

	select {
	case <-done:
	case <-time.After(drainBudget):
	}

	_ = x.closeFramed()
}

// trySubmit admits frame only when capacity and the writer queue are
// immediately available. It never blocks the reader or liveness goroutine.
// CREDIT, PING, PONG, and ERROR may exceed the admission byte cap so control
// traffic can enter the queue while windowed DATA is parked in the writer.
func (x *duplexConn) trySubmit(frame Frame) bool {
	if frame.Version == 0 {
		frame.Version = ProtocolVersion
	}
	if int(frame.Length) != frame.bodyLen() {
		frame.Length = uint32(frame.bodyLen())
	}

	cost := int64(FrameHeaderSize) + int64(frame.Length)
	if !x.mu.TryLock() {
		return false
	}
	defer x.mu.Unlock()

	if x.isClosed() || !x.canAdmitLocked(cost, frame.Type) {
		return false
	}

	select {
	case x.out <- frame:
		x.outBytes += cost
		return true
	default:
		return false
	}
}

// admitFrame enqueues frame only when byte capacity and a writer-queue slot
// are immediately available. It may block briefly on the connection mutex but
// never on backpressure, so unlike [duplexConn.trySubmit] (which must not
// block a reader goroutine and gives up on a contended mutex) its only
// failure modes are a closed connection and a genuinely full queue.
func (x *duplexConn) admitFrame(frame Frame) error {
	if frame.Version == 0 {
		frame.Version = ProtocolVersion
	}

	if int(frame.Length) != frame.bodyLen() {
		frame.Length = uint32(frame.bodyLen())
	}

	cost := int64(FrameHeaderSize) + int64(frame.Length)

	x.mu.Lock()
	defer x.mu.Unlock()

	if x.isClosed() {
		return ErrDuplexClosed
	}

	if !x.canAdmitLocked(cost, frame.Type) {
		return ErrDuplexBackpressure
	}

	select {
	case x.out <- frame:
		x.outBytes += cost
		return nil
	default:
		return ErrDuplexBackpressure
	}
}

// canAdmitLocked reports whether cost bytes of frameType may enter the
// outbound queue. The caller must hold mu. Control frames may exceed the
// admission cap; a single oversized windowed frame may enter an empty queue
// so a peer with a smaller credit window can still make progress.
func (x *duplexConn) canAdmitLocked(cost int64, frameType byte) bool {
	if admissionExemptFrameType(frameType) {
		return true
	}

	if x.outBytes+cost <= x.maxOutBytes {
		return true
	}

	return x.creditEnabled && windowedFrameType(frameType) && x.outBytes == 0
}

// nextCorrelation allocates a nonzero correlation identifier.
func (x *duplexConn) nextCorrelation() uint64 {
	corr := x.nextCorr.Add(1)
	if corr == 0 {
		corr = x.nextCorr.Add(1)
	}
	return corr
}

// failTransport marks the duplex closed and releases the underlying socket
// when the peer drops the connection without a local [Close]. Closing the
// framed connection unblocks a writer stuck in WriteFrames and prevents FD
// retention until the next outbound call.
func (x *duplexConn) failTransport() {
	x.signalClose()
	if !x.closing.Load() {
		_ = x.closeFramed()
	}
}

// closeFramed closes the underlying framed connection at most once.
func (x *duplexConn) closeFramed() error {
	var err error
	x.framedCloseOnce.Do(func() {
		err = x.framed.Close()
	})
	return err
}

// applyWriteDeadline bounds the next socket write when writeTimeout is set so
// a stalled peer cannot block the writer loop indefinitely.
func (x *duplexConn) applyWriteDeadline() {
	if x.writeTimeout <= 0 {
		return
	}
	if nc := x.framed.NetConn(); nc != nil {
		_ = nc.SetWriteDeadline(time.Now().Add(x.writeTimeout))
	}
}

// bindWriteDeadline applies writeTimeout when ctx has no deadline.
func (x *duplexConn) bindWriteDeadline(ctx context.Context) (context.Context, context.CancelFunc) {
	if x.writeTimeout <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, x.writeTimeout)
}

// IsClosed reports whether the duplex has been signaled closed. Part of the
// [DuplexSession] surface so owners can discard stale sessions before use.
func (x *duplexConn) IsClosed() bool {
	return x.isClosed()
}

// Lane returns the negotiated connection lane byte.
func (x *duplexConn) Lane() byte {
	return x.lane
}

// Revision returns the negotiated capability revision.
func (x *duplexConn) Revision() uint32 {
	return x.revision
}

// MaxFrameSize returns the negotiated whole-frame ceiling.
func (x *duplexConn) MaxFrameSize() uint32 {
	return x.maxFrameSize
}

// MaxMessageSize returns the negotiated reassembled logical-frame ceiling.
func (x *duplexConn) MaxMessageSize() uint64 {
	return x.maxMessageSize
}

// MaxConcurrentLargeTransfers returns the negotiated concurrent CHUNK-group cap.
func (x *duplexConn) MaxConcurrentLargeTransfers() uint32 {
	return x.maxConcurrentLargeTransfers
}

// ChunkSize returns the local CHUNK send threshold.
func (x *duplexConn) ChunkSize() uint32 {
	return x.chunkSize
}

// isClosed reports whether the duplex has been signaled closed. It only reads
// the closed channel, so it is safe with or without the mutex held.
func (x *duplexConn) isClosed() bool {
	select {
	case <-x.closed:
		return true
	default:
		return false
	}
}

// closedError picks the most informative terminal error: the reader's, then
// the writer's, then the generic [ErrDuplexClosed].
func (x *duplexConn) closedError() error {
	if errPtr := x.readErr.Load(); errPtr != nil {
		return *errPtr
	}

	if errPtr := x.writeErr.Load(); errPtr != nil {
		return *errPtr
	}

	return ErrDuplexClosed
}

// submitContextError maps a done context to either cancellation or
// backpressure (deadline exceeded while waiting for queue capacity).
func submitContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return ctx.Err()
	}

	return ErrDuplexBackpressure
}
