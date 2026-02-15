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
	"compress/gzip"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

// GzipConnWrapper wraps connections with Gzip compression.
// Writer and reader instances are pooled for reuse.
//
// Gzip is widely supported and offers good compatibility with external systems.
// For higher throughput and lower CPU usage, prefer [ZstdConnWrapper].
type GzipConnWrapper struct {
	level      int
	writerPool sync.Pool
	readerPool sync.Pool
}

// NewGzipConnWrapper creates a [GzipConnWrapper] with the given options.
// The default compression level is [gzip.DefaultCompression].
func NewGzipConnWrapper(opts ...GzipOption) (*GzipConnWrapper, error) {
	cfg := gzipConfig{
		level: gzip.DefaultCompression,
	}
	for _, o := range opts {
		o(&cfg)
	}

	// Validate the level eagerly by creating one writer.
	w, err := gzip.NewWriterLevel(nil, cfg.level)
	if err != nil {
		return nil, ErrGzipInvalidLevel
	}

	wrapper := &GzipConnWrapper{level: cfg.level}

	// Seed the writer pool with the validated instance.
	wrapper.writerPool.Put(w)
	wrapper.writerPool.New = func() any {
		gw, err := gzip.NewWriterLevel(nil, wrapper.level)
		if err != nil {
			return nil
		}
		return gw
	}

	wrapper.readerPool.New = func() any {
		// Readers are initialised lazily via Reset, so we return a zero reader.
		return new(gzip.Reader)
	}

	return wrapper, nil
}

// Wrap applies Gzip compression to conn.
// The returned [net.Conn] compresses writes and decompresses reads.
//
// Unlike Brotli and Zstd, the stdlib gzip.Reader.Reset eagerly reads the gzip
// header from the stream. On a fresh TCP connection where no data has been sent
// yet, this would block indefinitely (both sides waiting for the other's header).
// To avoid this deadlock, the reader is initialised lazily on the first Read call
// via [gzipLazyReader].
func (g *GzipConnWrapper) Wrap(conn net.Conn) (net.Conn, error) {
	gw, ok := g.writerPool.Get().(*gzip.Writer)
	if !ok || gw == nil {
		return nil, ErrGzipWriterInit
	}

	gr, ok := g.readerPool.Get().(*gzip.Reader)
	if !ok || gr == nil {
		g.writerPool.Put(gw)
		return nil, ErrGzipReaderInit
	}

	gw.Reset(conn)
	// Do NOT call gr.Reset(conn) here — it would block reading the gzip header.
	// Instead wrap in a lazy reader that defers Reset to the first Read.
	lazyReader := &gzipLazyReader{raw: conn, gr: gr}

	closer := func() error {
		closeErr := gw.Close()
		gw.Reset(nil)
		g.writerPool.Put(gw)
		// Only close the reader if it was actually initialised.
		if lazyReader.initialised.Load() {
			_ = gr.Close()
		}
		g.readerPool.Put(gr)
		return closeErr
	}

	return getCompressedConn(conn, lazyReader, &gzipFlushWriter{w: gw}, closer), nil
}

// gzipLazyReader wraps a [gzip.Reader] with lazy initialisation. The first call
// to Read triggers gzip.Reader.Reset (which reads the gzip header from the wire).
// This avoids a deadlock when both sides of a TCP connection try to read the
// header before either has written anything.
type gzipLazyReader struct {
	raw         io.Reader
	gr          *gzip.Reader
	initialised atomic.Bool
	initErr     error
	once        sync.Once
}

func (r *gzipLazyReader) Read(p []byte) (int, error) {
	r.once.Do(func() {
		r.initErr = r.gr.Reset(r.raw)
		if r.initErr == nil {
			r.initialised.Store(true)
		}
	})
	if r.initErr != nil {
		return 0, r.initErr
	}
	return r.gr.Read(p)
}

// gzipFlushWriter adapts a [gzip.Writer] to the [flushWriter] interface
// required by [compressedConn]. Each Write is followed by a Flush to ensure
// the compressed data is immediately available on the wire — critical for
// the request/response framing used by [ProtoServer] and [Client].
type gzipFlushWriter struct {
	w *gzip.Writer
}

func (f *gzipFlushWriter) Write(p []byte) (int, error) { return f.w.Write(p) }
func (f *gzipFlushWriter) Flush() error                { return f.w.Flush() }

type gzipConfig struct {
	level int
}

// GzipOption configures [NewGzipConnWrapper].
type GzipOption func(*gzipConfig)

// WithGzipLevel sets the Gzip compression level.
// Valid values range from gzip.BestSpeed (1) to gzip.BestCompression (9),
// or use gzip.DefaultCompression (-1).
func WithGzipLevel(level int) GzipOption {
	return func(c *gzipConfig) { c.level = level }
}

var _ ConnWrapper = (*GzipConnWrapper)(nil)
var _ flushWriter = (*gzipFlushWriter)(nil)
var _ io.Reader = (*gzipLazyReader)(nil)
