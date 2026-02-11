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

package tcp

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

type compressedConn struct {
	raw    net.Conn
	reader io.Reader
	writer flushWriter
	closer func() error
}

type flushWriter interface {
	io.Writer
	Flush() error
}

func (c *compressedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *compressedConn) Write(p []byte) (int, error) {
	n, err := c.writer.Write(p)
	if err != nil {
		return n, err
	}
	if ferr := c.writer.Flush(); ferr != nil {
		return n, ferr
	}
	return n, nil
}

func (c *compressedConn) Close() error {
	cerr := c.closer()
	nerr := c.raw.Close()

	// Clear all references and return to pool.
	c.raw = nil
	c.reader = nil
	c.writer = nil
	c.closer = nil
	compressedConnPool.Put(c)

	if cerr != nil {
		return cerr
	}
	return nerr
}

func (c *compressedConn) LocalAddr() net.Addr                { return c.raw.LocalAddr() }
func (c *compressedConn) RemoteAddr() net.Addr               { return c.raw.RemoteAddr() }
func (c *compressedConn) SetDeadline(t time.Time) error      { return c.raw.SetDeadline(t) }
func (c *compressedConn) SetReadDeadline(t time.Time) error  { return c.raw.SetReadDeadline(t) }
func (c *compressedConn) SetWriteDeadline(t time.Time) error { return c.raw.SetWriteDeadline(t) }

var compressedConnPool = sync.Pool{
	New: func() any {
		return &compressedConn{}
	},
}

func getCompressedConn(raw net.Conn, r io.Reader, w flushWriter, closer func() error) *compressedConn {
	cc := compressedConnPool.Get().(*compressedConn)
	cc.raw = raw
	cc.reader = r
	cc.writer = w
	cc.closer = closer
	return cc
}
