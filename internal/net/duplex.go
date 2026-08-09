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
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
)

// ErrDuplexClosed is returned when submitting to or reading from a closed
// duplex connection.
var ErrDuplexClosed = errors.New("tcp: duplex connection is closed")

// ErrDuplexBackpressure is returned when the outbound queue cannot accept a
// frame within the caller's deadline.
var ErrDuplexBackpressure = errors.New("tcp: duplex outbound queue full")

// duplexWriteBatchMax is the maximum number of frames the writer coalesces
// into one vectored WriteFrames call per wakeup.
const duplexWriteBatchMax = 32

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
	// lastInbound records the latest successfully read frame timestamp.
	lastInbound atomic.Int64

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
	writeWG    sync.WaitGroup
	readWG     sync.WaitGroup
	livenessWG sync.WaitGroup
	writeErr   atomic.Pointer[error]
	readErr    atomic.Pointer[error]
}

// duplexCloseDrainGrace is used when writeTimeout is unset so Close cannot
// block forever on a peer that stopped reading.
const duplexCloseDrainGrace = 5 * time.Second

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
	}
}

// withDuplexSenderResolver installs the actor-layer hook used to materialize
// opaque sender handles on path table hits.
func withDuplexSenderResolver(resolve func(path string) any) duplexConnOption {
	return func(x *duplexConn) {
		x.senderResolver = resolve
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

	x.lastInbound.Store(time.Now().UnixNano())

	x.writeWG.Add(1)
	x.readWG.Add(1)
	go x.writeLoop()
	go x.readLoop()
	if x.readIdleTimeout > 0 {
		x.livenessWG.Add(1)
		go x.livenessLoop()
	}
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

		if x.outBytes+cost <= x.maxOutBytes {
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
		x.livenessWG.Wait()

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
// vectored WriteFrames call per wakeup. On shutdown it flushes any frames
// already admitted so connection-scoped ERROR reaches the peer.
func (x *duplexConn) writeLoop() {
	defer x.writeWG.Done()

	batch := make([]Frame, 0, duplexWriteBatchMax)
	costs := make([]int64, 0, duplexWriteBatchMax)

	for {
		batch = batch[:0]
		costs = costs[:0]

		select {
		case <-x.closed:
			x.drainOutbound()
			return
		case frame := <-x.out:
			batch = append(batch, frame)
			costs = append(costs, int64(FrameHeaderSize)+int64(frame.Length))
		}

		for len(batch) < duplexWriteBatchMax {
			select {
			case frame := <-x.out:
				batch = append(batch, frame)
				costs = append(costs, int64(FrameHeaderSize)+int64(frame.Length))
			default:
				goto flush
			}
		}

	flush:
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
	}
}

// drainOutbound writes every frame already admitted to the outbound queue.
// Called once during shutdown after [signalClose] so no new Submits succeed.
func (x *duplexConn) drainOutbound() {
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
			x.release(int64(FrameHeaderSize) + int64(frame.Length))
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

	for {
		if x.connIdleTimeout > 0 {
			_ = x.framed.NetConn().SetReadDeadline(time.Now().Add(x.connIdleTimeout))
		}

		frame, err := x.framed.ReadFrame()
		if err != nil {
			if !x.closing.Load() {
				x.readErr.Store(&err)
			}

			x.failTransport()
			return
		}

		x.lastInbound.Store(time.Now().UnixNano())

		if x.enforceLane && frame.Lane != x.lane {
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
				_ = x.pending.complete(frame.Correlation, frame)
				continue
			}
		}

		select {
		case <-x.closed:
			return
		case x.inbound <- frame:
		}
	}
}

// rejectWrongLane emits a best-effort connection-scoped ERROR then tears down
// the transport. Called from the reader: admit via trySubmit, signal writer
// drain, then close the socket so the ERROR can leave before teardown.
func (x *duplexConn) rejectWrongLane() {
	payload, err := proto.Marshal(&internalpb.Error{
		Code:    internalpb.Code_CODE_FAILED_PRECONDITION,
		Message: "frame lane does not match connection lane",
	})
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

// livenessLoop probes a silent peer and closes the transport after two missed
// PONGs: two admitted probes that each passed a full idle interval with no
// inbound traffic of any kind. A miss is counted only one interval after its
// probe was admitted, so a peer always gets a full interval to answer the
// second probe before the transport fails.
func (x *duplexConn) livenessLoop() {
	defer x.livenessWG.Done()

	timer := time.NewTimer(x.readIdleTimeout)
	defer timer.Stop()

	var misses int
	var probeOutstanding bool
	lastObserved := x.lastInbound.Load()

	for {
		select {
		case <-x.closed:
			return
		case <-timer.C:
			lastInbound := x.lastInbound.Load()
			elapsed := time.Since(time.Unix(0, lastInbound))
			if elapsed < x.readIdleTimeout {
				timer.Reset(x.readIdleTimeout - elapsed)
				continue
			}

			if lastInbound != lastObserved {
				// Inbound traffic (including a PONG) proves the peer is
				// alive; restart the idle window without probing.
				misses = 0
				probeOutstanding = false
				lastObserved = lastInbound
				timer.Reset(x.readIdleTimeout)
				continue
			}

			if probeOutstanding {
				misses++

				if misses >= 2 {
					x.failTransport()
					return
				}
			}

			// Track admission only: a backpressure drop must not punish a
			// peer that never received the PING.
			probeOutstanding = x.trySubmit(Frame{
				Version:     ProtocolVersion,
				Type:        FrameTypePing,
				Lane:        x.lane,
				Correlation: x.nextCorrelation(),
			})
			timer.Reset(x.readIdleTimeout)
		}
	}
}

// trySubmit admits frame only when capacity and the writer queue are
// immediately available. It never blocks the reader or liveness goroutine.
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

	if x.isClosed() || x.outBytes+cost > x.maxOutBytes {
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

	if x.outBytes+cost > x.maxOutBytes {
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
