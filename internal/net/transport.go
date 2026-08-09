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
	"fmt"
	"io"
	"net"
	"time"

	"github.com/tochemey/goakt/v4/internal/internalpb"
)

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

// tcpFramedConn implements [FramedConn] over a [net.Conn].
// readHdr and writeHdr are separate so the duplex reader and writer goroutines
// can run concurrently without racing on a shared header scratch buffer.
type tcpFramedConn struct {
	conn         net.Conn
	maxFrameSize uint32
	readHdr      [FrameHeaderSize]byte
	writeHdr     [FrameHeaderSize]byte
}

// newTCPFramedConn wraps conn as a [FramedConn].
func newTCPFramedConn(conn net.Conn, maxFrameSize uint32) *tcpFramedConn {
	return &tcpFramedConn{
		conn:         conn,
		maxFrameSize: floorMaxFrameSize(maxFrameSize),
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

// WriteFrames encodes and writes each frame in order.
func (x *tcpFramedConn) WriteFrames(frames ...Frame) error {
	for i := range frames {
		f := frames[i]
		if int(f.Length) != len(f.Payload) {
			return fmt.Errorf("tcp: frame length %d does not match payload %d", f.Length, len(f.Payload))
		}

		if f.Version == 0 {
			f.Version = ProtocolVersion
		}

		if err := encodeFrameHeader(x.writeHdr[:], f); err != nil {
			return err
		}

		if _, err := x.conn.Write(x.writeHdr[:]); err != nil {
			return err
		}

		if len(f.Payload) > 0 {
			if _, err := x.conn.Write(f.Payload); err != nil {
				return err
			}
		}
	}

	return nil
}

// ReadFrame reads one complete frame from the connection. Protocol-version
// mismatches surface as [ErrUnsupportedProtocolVersion]; the protocol layer
// owns emitting the in-band ERROR frame.
func (x *tcpFramedConn) ReadFrame() (Frame, error) {
	if _, err := io.ReadFull(x.conn, x.readHdr[:]); err != nil {
		return Frame{}, err
	}

	f, err := decodeFrameHeader(x.readHdr[:], x.maxFrameSize)
	if err != nil {
		return Frame{}, err
	}

	if f.Length == 0 {
		return f, nil
	}

	payload := make([]byte, f.Length)
	if _, err := io.ReadFull(x.conn, payload); err != nil {
		return Frame{}, err
	}

	f.Payload = payload
	return f, nil
}

// Close closes the underlying connection.
func (x *tcpFramedConn) Close() error {
	return x.conn.Close()
}

// ReplaceNetConn swaps the underlying connection, used after the handshake
// installs a negotiated compression wrapper.
func (x *tcpFramedConn) ReplaceNetConn(conn net.Conn) {
	x.conn = conn
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
