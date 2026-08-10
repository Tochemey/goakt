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
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/tochemey/goakt/v4/internal/internalpb"
)

// framedReadBufferSize is the fixed per-connection read buffer installed by
// [tcpFramedConn.EnableReadBuffering] after negotiation. Sized so a fill
// amortizes syscalls across many small frames while staying a bounded,
// footprint-friendly per-connection cost.
const framedReadBufferSize = 64 * 1024

// LaneSpec describes which lane a dialed or accepted connection belongs to.
type LaneSpec struct {
	Role  internalpb.LaneRole
	Index uint32
}

// Transport dials and accepts framed duplex connections for a peer.
type Transport interface {
	Dial(ctx context.Context, peer string, lane LaneSpec) (FramedConn, error)
	Listen(addr string) (Acceptor, error)
}

// FramedConn moves whole duplex frames with the section-1 header.
type FramedConn interface {
	WriteFrames(frames ...Frame) error
	ReadFrame() (Frame, error)
	Close() error
	NetConn() net.Conn
	SetMaxFrameSize(maxSize uint32)
	MaxFrameSize() uint32
}

// Acceptor accepts inbound framed connections.
type Acceptor interface {
	Accept(ctx context.Context) (FramedConn, error)
	Addr() net.Addr
	Close() error
}

// TCPTransport is the TCP implementation of [Transport].
type TCPTransport struct {
	dialTimeout  time.Duration
	keepAlive    time.Duration
	maxFrameSize uint32
}

// TCPTransportOption configures a [TCPTransport].
type TCPTransportOption func(*TCPTransport)

// WithTCPTransportDialTimeout sets the dial timeout.
func WithTCPTransportDialTimeout(d time.Duration) TCPTransportOption {
	return func(x *TCPTransport) {
		x.dialTimeout = d
	}
}

// WithTCPTransportKeepAlive sets the TCP keep-alive period.
func WithTCPTransportKeepAlive(d time.Duration) TCPTransportOption {
	return func(x *TCPTransport) {
		x.keepAlive = d
	}
}

// WithTCPTransportMaxFrameSize sets the initial max frame size used before
// handshake negotiation replaces it with the pairwise minimum.
func WithTCPTransportMaxFrameSize(maxSize uint32) TCPTransportOption {
	return func(x *TCPTransport) {
		x.maxFrameSize = maxSize
	}
}

// NewTCPTransport returns a [TCPTransport] with sensible defaults.
func NewTCPTransport(opts ...TCPTransportOption) *TCPTransport {
	x := &TCPTransport{
		dialTimeout:  5 * time.Second,
		keepAlive:    15 * time.Second,
		maxFrameSize: defaultMaxFrameSize,
	}

	for _, opt := range opts {
		opt(x)
	}

	return x
}

// Dial connects to peer and returns a framed connection. Lane is recorded for
// the subsequent HELLO; it does not affect the TCP dial itself.
func (x *TCPTransport) Dial(ctx context.Context, peer string, _ LaneSpec) (FramedConn, error) {
	dialer := &net.Dialer{
		Timeout:   x.dialTimeout,
		KeepAlive: x.keepAlive,
	}

	conn, err := dialer.DialContext(ctx, "tcp", peer)
	if err != nil {
		return nil, err
	}

	return newTCPFramedConn(conn, x.maxFrameSize), nil
}

// Listen starts a TCP listener that accepts framed connections.
func (x *TCPTransport) Listen(addr string) (Acceptor, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	return &tcpAcceptor{
		ln:           ln,
		maxFrameSize: x.maxFrameSize,
	}, nil
}

// tcpAcceptor implements [Acceptor] for TCP.
type tcpAcceptor struct {
	ln           net.Listener
	maxFrameSize uint32
}

// Accept waits for the next inbound connection.
func (x *tcpAcceptor) Accept(ctx context.Context) (FramedConn, error) {
	tcpLn, isTCP := x.ln.(*net.TCPListener)
	if isTCP {
		if deadline, ok := ctx.Deadline(); ok {
			_ = tcpLn.SetDeadline(deadline)
		} else {
			_ = tcpLn.SetDeadline(time.Time{})
		}
		defer func() { _ = tcpLn.SetDeadline(time.Time{}) }()
	}

	conn, err := x.ln.Accept()
	if err != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
			return nil, err
		}
	}

	return newTCPFramedConn(conn, x.maxFrameSize), nil
}

// Addr returns the listener address.
func (x *tcpAcceptor) Addr() net.Addr {
	return x.ln.Addr()
}

// Close closes the listener.
func (x *tcpAcceptor) Close() error {
	return x.ln.Close()
}

// tcpReadPool recycles [tcpFramedConn.ReadFrame] bodies for frame types with a
// deterministic release point (CHUNK after reassembly copy; DATA/REPLY/ERROR
// after Deserialize or drop). Other frame types allocate exact-size buffers.
var tcpReadPool = NewFramePool()

// tcpFramedConn implements [FramedConn] over a [net.Conn].
// readHdr and the write-side scratch buffers are separate so the duplex reader
// and writer goroutines can run concurrently without racing. WriteFrames must
// only be called from one goroutine at a time (the duplex writer loop or a
// single handshake goroutine): writeHdrs and writeBufs are reused across
// calls, which is safe because [net.Buffers.WriteTo] completes before return.
type tcpFramedConn struct {
	// conn is written once during dial (ReplaceNetConn installs the
	// compression wrapper) and read on every frame. connMu orders that lone
	// setup write against a concurrent Close: OpenDuplex arms a
	// context-cancel AfterFunc that calls Close on its own goroutine to
	// unblock a stalled HELLO read (ClosePeer), and that can fire exactly as
	// the dial reaches ReplaceNetConn. Every other conn access is
	// goroutine-ordered (the dial goroutine before the swap, the read and
	// write loops after OpenDuplex returns), so only these two take the lock
	// and the hot path stays lock-free.
	connMu       sync.Mutex
	conn         net.Conn
	maxFrameSize uint32
	// br batches frame reads through one fixed buffer so a stream of small
	// frames costs two syscalls per buffer fill instead of two per frame.
	// Nil until [tcpFramedConn.EnableReadBuffering] runs after the handshake
	// and compression wrap, so negotiation-phase reads can never buffer ahead
	// of a connection-layer swap. Only the read goroutine touches it.
	br      *bufio.Reader
	readHdr [FrameHeaderSize]byte
	// Partial-frame resume state, owned by the read goroutine. A read
	// deadline armed for liveness probing can expire mid-frame; these fields
	// retain the bytes already consumed so the next ReadFrame call resumes
	// the same frame instead of misparsing the stream from its middle.
	readHdrN       int
	pendingFrame   Frame
	pendingPayload []byte
	pendingN       int
	pendingActive  bool
	// writeHdrs is a header arena for vectored writes: one 16-byte slot per
	// frame of a writer batch, so multi-frame writes allocate nothing.
	writeHdrs [duplexWriteBatchMax][FrameHeaderSize]byte
	// writeBufs is the reusable vector for [net.Buffers.WriteTo]. Entries are
	// nilled after each write so payload slices are not retained.
	writeBufs net.Buffers
	// readPool supplies ReadFrame payloads. Nil disables pooling (tests).
	readPool *FramePool
}

// newTCPFramedConn wraps conn as a [FramedConn].
func newTCPFramedConn(conn net.Conn, maxFrameSize uint32) *tcpFramedConn {
	return &tcpFramedConn{
		conn:         conn,
		maxFrameSize: floorMaxFrameSize(maxFrameSize),
		readPool:     tcpReadPool,
	}
}

// NetConn returns the underlying connection.
func (x *tcpFramedConn) NetConn() net.Conn {
	return x.conn
}

// SetMaxFrameSize updates the read-side frame length bound. Values below
// [minMaxFrameSize] are raised to that floor.
func (x *tcpFramedConn) SetMaxFrameSize(maxSize uint32) {
	x.maxFrameSize = floorMaxFrameSize(maxSize)
}

// MaxFrameSize returns the current frame length bound.
func (x *tcpFramedConn) MaxFrameSize() uint32 {
	return x.maxFrameSize
}

// WriteFrames encodes and writes the frames in order as one vectored write.
// Headers come from the per-connection arena and the buffer vector is reused,
// so steady-state batches allocate nothing. Batches larger than the arena
// (never produced by the duplex writer, which caps at [duplexWriteBatchMax])
// fall back to a heap header per excess frame.
func (x *tcpFramedConn) WriteFrames(frames ...Frame) error {
	if len(frames) == 0 {
		return nil
	}

	buffers := x.writeBufs[:0]

	for i := range frames {
		f := frames[i]
		if int(f.Length) != f.bodyLen() {
			return fmt.Errorf("tcp: frame length %d does not match body %d", f.Length, f.bodyLen())
		}

		if f.Version == 0 {
			f.Version = ProtocolVersion
		}

		var hdr []byte
		if i < duplexWriteBatchMax {
			hdr = x.writeHdrs[i][:]
		} else {
			hdr = make([]byte, FrameHeaderSize)
		}

		if err := encodeFrameHeader(hdr, f); err != nil {
			return err
		}

		buffers = append(buffers, hdr)
		if len(f.Prefix) > 0 {
			buffers = append(buffers, f.Prefix)
		}
		if len(f.Payload) > 0 {
			buffers = append(buffers, f.Payload)
		}
	}

	// Retain the vector's backing array for reuse before WriteTo consumes the
	// local slice header.
	x.writeBufs = buffers

	_, err := buffers.WriteTo(x.conn)

	// Drop payload references so the arena does not keep frame payloads alive
	// until the next write.
	for i := range x.writeBufs {
		x.writeBufs[i] = nil
	}
	x.writeBufs = x.writeBufs[:0]

	return err
}

// ReadFrame reads one complete frame from the connection. Protocol-version
// mismatches surface as [ErrUnsupportedProtocolVersion]; the protocol layer
// owns emitting the in-band ERROR frame.
//
// ReadFrame is resumable across read-deadline expiries: when a deadline
// armed for liveness probing fires mid-frame, the partial header or payload
// progress is retained and the next call continues the same frame. A
// terminal (non-timeout) error mid-payload returns the pending buffer to the
// read pool and resets the resume state; the connection is being torn down
// and nothing else will release that buffer.
func (x *tcpFramedConn) ReadFrame() (Frame, error) {
	src := x.readSource()

	if !x.pendingActive {
		for x.readHdrN < FrameHeaderSize {
			n, err := src.Read(x.readHdr[x.readHdrN:])
			x.readHdrN += n
			if err != nil {
				return Frame{}, err
			}
		}

		x.readHdrN = 0
		frame, err := decodeFrameHeader(x.readHdr[:], x.maxFrameSize)
		if err != nil {
			return Frame{}, err
		}

		if frame.Length == 0 {
			return frame, nil
		}

		x.pendingFrame = frame
		x.pendingPayload = x.getReadPayload(frame.Type, int(frame.Length))
		x.pendingN = 0
		x.pendingActive = true
	}

	for x.pendingN < len(x.pendingPayload) {
		n, err := src.Read(x.pendingPayload[x.pendingN:])
		x.pendingN += n

		if err != nil {
			if !isTimeoutError(err) {
				x.releaseReadPayload(x.pendingPayload)
				x.pendingFrame = Frame{}
				x.pendingPayload = nil
				x.pendingN = 0
				x.pendingActive = false
			}

			return Frame{}, err
		}
	}

	frame := x.pendingFrame
	frame.Payload = x.pendingPayload
	x.pendingFrame = Frame{}
	x.pendingPayload = nil
	x.pendingN = 0
	x.pendingActive = false
	return frame, nil
}

// AbandonPendingRead returns a partially-read frame's pooled payload and
// resets the resume state. The read-loop owner calls it on exit paths where
// the last ReadFrame returned a resumable timeout but nothing will ever
// resume the frame (liveness failure, idle reclaim); without it the pooled
// buffer is abandoned to the garbage collector. Must be called only from the
// goroutine that owns ReadFrame; a no-op when no resume state is held.
func (x *tcpFramedConn) AbandonPendingRead() {
	if !x.pendingActive {
		return
	}

	x.releaseReadPayload(x.pendingPayload)
	x.pendingFrame = Frame{}
	x.pendingPayload = nil
	x.pendingN = 0
	x.pendingActive = false
}

// readSource returns the reader frame bytes come from: the buffered reader
// once EnableReadBuffering has run, the bare connection before that.
func (x *tcpFramedConn) readSource() io.Reader {
	if x.br != nil {
		return x.br
	}

	return x.conn
}

// BufferedReadBytes reports how many already-read bytes wait in the fixed
// read buffer, zero before EnableReadBuffering. The duplex layer uses it as
// its load signal: buffered bytes after a frame read mean frames are
// arriving faster than they are consumed. Only the read goroutine may call
// it (the buffer is single-owner).
func (x *tcpFramedConn) BufferedReadBytes() int {
	if x.br != nil {
		return x.br.Buffered()
	}

	return 0
}

// EnableReadBuffering installs the fixed read buffer over the current
// connection. Call it exactly once, after the handshake and any compression
// wrap, from the goroutine that owns reads; buffering the negotiation phase
// could read ahead of a [tcpFramedConn.ReplaceNetConn] layer swap.
func (x *tcpFramedConn) EnableReadBuffering() {
	if x.br == nil {
		x.br = bufio.NewReaderSize(x.conn, framedReadBufferSize)
	}
}

// poolsReadPayload reports whether frameType bodies are drawn from [tcpReadPool].
func poolsReadPayload(frameType byte) bool {
	switch frameType {
	case FrameTypeChunk, FrameTypeData, FrameTypeReply, FrameTypeError:
		return true
	default:
		return false
	}
}

// getReadPayload returns an n-byte buffer for a frame body. Pooled types are
// listed by [poolsReadPayload]; callers must [releaseReadPayload] exactly once
// after the body is consumed or dropped.
func (x *tcpFramedConn) getReadPayload(frameType byte, n int) []byte {
	if x.readPool != nil && poolsReadPayload(frameType) {
		return x.readPool.Get(n)
	}

	return make([]byte, n)
}

// releaseReadPayload returns a ReadFrame payload to the pool. Safe to call
// with a nil pool or empty buffer.
func (x *tcpFramedConn) releaseReadPayload(buf []byte) {
	if x.readPool == nil || buf == nil {
		return
	}

	x.readPool.Put(buf)
}

// Close closes the underlying connection. It reads conn under connMu so a
// cancel-driven Close (the OpenDuplex AfterFunc) cannot race the dial's
// ReplaceNetConn swap; the close syscall runs outside the lock.
func (x *tcpFramedConn) Close() error {
	x.connMu.Lock()
	conn := x.conn
	x.connMu.Unlock()

	return conn.Close()
}

// ReplaceNetConn swaps the underlying connection, used after the handshake
// installs a negotiated compression wrapper. The swap is taken under connMu
// so a concurrent cancel-driven Close observes one whole conn value. A read
// buffer installed earlier is rebuilt over the new connection; by the
// EnableReadBuffering contract it holds no unread bytes at swap time, and only
// the dial goroutine touches br here, so its rebuild stays outside the lock.
func (x *tcpFramedConn) ReplaceNetConn(conn net.Conn) {
	x.connMu.Lock()
	x.conn = conn
	x.connMu.Unlock()

	if x.br != nil {
		x.br = bufio.NewReaderSize(conn, framedReadBufferSize)
	}
}

// encodeHelloFrame builds a HELLO or HELLO_ACK frame from msg.
func encodeHelloFrame(frameType byte, lane byte, msg []byte) Frame {
	return Frame{
		Version: ProtocolVersion,
		Type:    frameType,
		Lane:    lane,
		Length:  uint32(len(msg)),
		Payload: msg,
	}
}

// floorMaxFrameSize raises a zero or sub-minimum limit to [minMaxFrameSize].
func floorMaxFrameSize(maxSize uint32) uint32 {
	if maxSize < minMaxFrameSize {
		return minMaxFrameSize
	}

	return maxSize
}
