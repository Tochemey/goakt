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
	"net"
	"sync"
)

// readBufferSize is the size of the pooled buffered reader attached to a
// connection's read side. Small frames coalesce into one kernel read
// (a frame header and body arrive together, and back-to-back frames drain
// from the buffer), while reads larger than the buffer bypass it entirely
// via bufio's large-read passthrough, so big frames pay no extra copy.
const readBufferSize = 32 * 1024

// readerPool recycles buffered readers across connections so establishing a
// connection does not allocate a fresh read buffer.
var readerPool = sync.Pool{
	New: func() any { return bufio.NewReaderSize(nil, readBufferSize) },
}

// getPooledReader returns a pooled buffered reader reset to read from r.
func getPooledReader(r net.Conn) *bufio.Reader {
	reader := readerPool.Get().(*bufio.Reader)
	reader.Reset(r)

	return reader
}

// putPooledReader detaches reader from its source and returns it to the pool.
func putPooledReader(reader *bufio.Reader) {
	reader.Reset(nil)
	readerPool.Put(reader)
}

// bufferedConn is a [net.Conn] whose reads go through a pooled buffered
// reader; writes go directly to the underlying connection. The reader is
// bound to the connection for the connection's whole lifetime, so read-ahead
// bytes are never stranded when the connection sits idle in a pool between
// exchanges.
//
// A bufferedConn must have a single reader at a time (the connection pool
// hands it to one goroutine per exchange). Close may race an in-flight Read
// safely: the pooled reader is only reclaimed once that Read has returned,
// and later Reads fail with [net.ErrClosed].
type bufferedConn struct {
	net.Conn
	mu     sync.Mutex
	reader *bufio.Reader
}

// newBufferedConn wraps conn with a pooled buffered read side.
func newBufferedConn(conn net.Conn) *bufferedConn {
	return &bufferedConn{
		Conn:   conn,
		reader: getPooledReader(conn),
	}
}

// Read reads via the buffered reader. A Read after Close returns
// [net.ErrClosed] instead of touching a reader that may already serve
// another connection.
func (b *bufferedConn) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.reader == nil {
		return 0, net.ErrClosed
	}

	return b.reader.Read(p)
}

// Close closes the underlying connection first, which unblocks any in-flight
// Read, then reclaims the pooled reader once that Read has released the lock.
// The reader is therefore never recycled while still referenced. Repeat Close
// calls only close the underlying connection again.
func (b *bufferedConn) Close() error {
	err := b.Conn.Close()

	b.mu.Lock()
	if b.reader != nil {
		putPooledReader(b.reader)
		b.reader = nil
	}
	b.mu.Unlock()

	return err
}
