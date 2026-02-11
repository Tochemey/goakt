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
	"errors"
	"io"
	"net"
	"sync"

	"github.com/andybalholm/brotli"
)

type BrotliConnWrapper struct {
	level      int
	writerPool sync.Pool
	readerPool sync.Pool
}

func NewBrotliConnWrapper(opts ...BrotliOption) *BrotliConnWrapper {
	cfg := brotliConfig{
		level: brotli.DefaultCompression,
	}
	for _, o := range opts {
		o(&cfg)
	}

	w := &BrotliConnWrapper{level: cfg.level}
	w.writerPool = sync.Pool{
		New: func() any {
			return brotli.NewWriterLevel(nil, w.level)
		},
	}
	w.readerPool = sync.Pool{
		New: func() any {
			return brotli.NewReader(nil)
		},
	}
	return w
}

// Wrap applies Brotli compression to conn.
// The returned [net.Conn] compresses writes and decompresses reads.
func (b *BrotliConnWrapper) Wrap(conn net.Conn) (net.Conn, error) {
	bw, ok := b.writerPool.Get().(*brotli.Writer)
	if !ok || bw == nil {
		return nil, ErrBrotliWriterInit
	}

	br, ok := b.readerPool.Get().(*brotli.Reader)
	if !ok || br == nil {
		b.writerPool.Put(bw)
		return nil, ErrBrotliReaderInit
	}

	bw.Reset(conn)
	if err := br.Reset(conn); err != nil {
		bw.Reset(nil)
		b.writerPool.Put(bw)
		b.readerPool.Put(br)
		return nil, err
	}

	closer := func() error {
		closeErr := bw.Close()
		bw.Reset(nil)
		b.writerPool.Put(bw)
		if err := br.Reset(nil); err != nil {
			b.readerPool.Put(br)
			return errors.Join(closeErr, err)
		}
		b.readerPool.Put(br)
		return closeErr
	}

	return getCompressedConn(conn, br, &brotliFlushWriter{w: bw}, closer), nil
}

type brotliFlushWriter struct {
	w *brotli.Writer
}

func (f *brotliFlushWriter) Write(p []byte) (int, error) { return f.w.Write(p) }
func (f *brotliFlushWriter) Flush() error                { return f.w.Flush() }

type brotliConfig struct {
	level int
}

type BrotliOption func(*brotliConfig)

func WithBrotliLevel(level int) BrotliOption {
	return func(c *brotliConfig) { c.level = level }
}

var _ ConnWrapper = (*BrotliConnWrapper)(nil)
var _ flushWriter = (*brotliFlushWriter)(nil)
var _ io.Reader = (*brotli.Reader)(nil)
