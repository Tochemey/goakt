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
	"io"
	"net"
	"sync"
	"time"
)

// ConnWrapper transforms a [net.Conn] — typically by adding a compression
// or framing layer. Implementations must be safe to call from multiple
// goroutines.
type ConnWrapper interface {
	Wrap(conn net.Conn) (net.Conn, error)
}

// compressedConn is a [net.Conn] that compresses writes and decompresses reads
// through a pooled codec.
//
// Close may run while other goroutines are inside Read or Write: a duplex
// connection is closed while its read loop is parked in the decoder and its
// writer may be inside the encoder. The codec state behind reader and writer
// is pooled and must never be reset or handed to another connection while a
// goroutine is still inside it, so the connection keeps this invariant: closer
// runs exactly once, and only when closed is set and active is zero. Read and
// Write register in active before they touch the codec and refuse to start
// once closed is set, so after that moment no goroutine can enter the codec
// again. A compressedConn is not reused after Close; only the codec state
// returns to its pool.
type compressedConn struct {
	raw    net.Conn
	reader io.Reader
	writer flushWriter
	// closer flushes the codec and returns its state to the wrapper pools.
	closer func() error

	// mu guards active and closed.
	mu sync.Mutex
	// active counts the goroutines currently inside Read or Write.
	active int
	// closed is set by the first Close and never cleared.
	closed bool
}

type flushWriter interface {
	io.Writer
	Flush() error
}

// Read decompresses into p. It fails with [net.ErrClosed] once the connection
// is closed.
func (x *compressedConn) Read(p []byte) (int, error) {
	if !x.enter() {
		return 0, net.ErrClosed
	}

	n, err := x.reader.Read(p)
	x.exit()
	return n, err
}

// Write compresses p and flushes it to the wire. It fails with
// [net.ErrClosed] once the connection is closed.
func (x *compressedConn) Write(p []byte) (int, error) {
	if !x.enter() {
		return 0, net.ErrClosed
	}

	n, err := x.writeAndFlush(p)
	x.exit()
	return n, err
}

// writeAndFlush runs one write through the codec and flushes it.
func (x *compressedConn) writeAndFlush(p []byte) (int, error) {
	n, err := x.writer.Write(p)
	if err != nil {
		return n, err
	}
	if ferr := x.writer.Flush(); ferr != nil {
		return n, ferr
	}
	return n, nil
}

// Close closes the connection and releases the codec. It is idempotent: only
// the first call acts, later calls return nil.
//
// When no Read or Write is in flight (the single-owner case of the legacy
// path) the codec is closed first, so its final bytes still reach the wire,
// and the raw connection is closed after it. When a Read or Write is in
// flight, the raw connection is closed first to unblock it, and the codec is
// released by the last of those calls as it returns (see [compressedConn.exit]).
func (x *compressedConn) Close() error {
	x.mu.Lock()
	if x.closed {
		x.mu.Unlock()
		return nil
	}

	x.closed = true
	idle := x.active == 0
	x.mu.Unlock()

	if !idle {
		return x.raw.Close()
	}

	cerr := x.closer()
	nerr := x.raw.Close()

	if cerr != nil {
		return cerr
	}
	return nerr
}

// enter registers the calling goroutine as being inside the codec. It reports
// false, without registering, once the connection is closed.
func (x *compressedConn) enter() bool {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.closed {
		return false
	}

	x.active++
	return true
}

// exit unregisters the calling goroutine. When Close ran while calls were in
// flight, the last one to leave releases the codec: at that point closed bars
// any new entry, so no goroutine can be inside the codec.
func (x *compressedConn) exit() {
	x.mu.Lock()
	x.active--
	release := x.closed && x.active == 0
	x.mu.Unlock()

	if release {
		_ = x.closer()
	}
}

func (x *compressedConn) LocalAddr() net.Addr                { return x.raw.LocalAddr() }
func (x *compressedConn) RemoteAddr() net.Addr               { return x.raw.RemoteAddr() }
func (x *compressedConn) SetDeadline(t time.Time) error      { return x.raw.SetDeadline(t) }
func (x *compressedConn) SetReadDeadline(t time.Time) error  { return x.raw.SetReadDeadline(t) }
func (x *compressedConn) SetWriteDeadline(t time.Time) error { return x.raw.SetWriteDeadline(t) }

// getCompressedConn builds the compressed connection over raw. closer must
// flush the codec behind r and w and return its state to the wrapper pools.
func getCompressedConn(raw net.Conn, r io.Reader, w flushWriter, closer func() error) *compressedConn {
	return &compressedConn{
		raw:    raw,
		reader: r,
		writer: w,
		closer: closer,
	}
}
