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

import "net"

// prependConn replays a previously peeked prefix before reading from the
// underlying connection. Used by the dual-protocol sniff so the first byte
// examined for routing is not lost from the byte stream.
type prependConn struct {
	net.Conn
	prefix []byte
}

// Read satisfies [net.Conn]. Bytes from prefix are returned first; once
// exhausted, reads pass through to the underlying connection.
func (x *prependConn) Read(p []byte) (int, error) {
	if len(x.prefix) == 0 {
		return x.Conn.Read(p)
	}

	n := copy(p, x.prefix)
	x.prefix = x.prefix[n:]
	if len(x.prefix) == 0 {
		x.prefix = nil
	}

	if n == len(p) {
		return n, nil
	}

	m, err := x.Conn.Read(p[n:])
	return n + m, err
}
