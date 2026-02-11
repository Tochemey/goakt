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

	"github.com/klauspost/compress/zstd"
)

// ZstdConnWrapper wraps connections with Zstandard compression.
// Encoder and decoder instances are pooled for reuse.
type ZstdConnWrapper struct {
	encoderOpts []zstd.EOption
	decoderOpts []zstd.DOption
	encoderPool sync.Pool
	decoderPool sync.Pool
}

// NewZstdConnWrapper creates a [ZstdConnWrapper] with the given options.
// It eagerly validates the encoder and decoder configuration and returns
// an error if the options are invalid.
func NewZstdConnWrapper(opts ...ZstdOption) (*ZstdConnWrapper, error) {
	cfg := zstdConfig{
		level:  zstd.SpeedDefault,
		window: 512 << 10, // 512 KB
		maxMem: 64 << 20,  // 64 MB
	}
	for _, o := range opts {
		o(&cfg)
	}

	encOpts := []zstd.EOption{
		zstd.WithEncoderLevel(cfg.level),
		zstd.WithWindowSize(cfg.window),
		zstd.WithEncoderConcurrency(1),
		zstd.WithLowerEncoderMem(true),
		zstd.WithZeroFrames(true),
	}

	decOpts := []zstd.DOption{
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxMemory(cfg.maxMem),
	}

	// Validate by eagerly creating one encoder and one decoder.
	enc, err := zstd.NewWriter(nil, encOpts...)
	if err != nil {
		return nil, errors.Join(ErrZstdInvalidEncoderOpts, err)
	}

	dec, err := zstd.NewReader(nil, decOpts...)
	if err != nil {
		enc.Close()
		return nil, errors.Join(ErrZstdInvalidDecoderOpts, err)
	}

	w := &ZstdConnWrapper{
		encoderOpts: encOpts,
		decoderOpts: decOpts,
	}

	// Seed the pools with the validated instances.
	w.encoderPool.Put(enc)
	w.decoderPool.Put(dec)

	w.encoderPool.New = func() any {
		e, err := zstd.NewWriter(nil, w.encoderOpts...)
		if err != nil {
			return nil
		}
		return e
	}
	w.decoderPool.New = func() any {
		d, err := zstd.NewReader(nil, w.decoderOpts...)
		if err != nil {
			return nil
		}
		return d
	}

	return w, nil
}

// Wrap applies Zstandard compression to conn.
// The returned [net.Conn] compresses writes and decompresses reads.
func (z *ZstdConnWrapper) Wrap(conn net.Conn) (net.Conn, error) {
	enc, ok := z.encoderPool.Get().(*zstd.Encoder)
	if !ok || enc == nil {
		return nil, ErrZstdEncoderInit
	}

	dec, ok := z.decoderPool.Get().(*zstd.Decoder)
	if !ok || dec == nil {
		z.encoderPool.Put(enc)
		return nil, ErrZstdDecoderInit
	}

	enc.Reset(conn)
	if err := dec.Reset(conn); err != nil {
		enc.Reset(nil)
		z.encoderPool.Put(enc)
		z.decoderPool.Put(dec)
		return nil, err
	}

	closer := func() error {
		closeErr := enc.Close()
		enc.Reset(nil)
		z.encoderPool.Put(enc)
		_ = dec.Reset(nil)
		z.decoderPool.Put(dec)
		return closeErr
	}

	return getCompressedConn(conn, dec, &zstdFlushWriter{enc: enc}, closer), nil
}

type zstdFlushWriter struct {
	enc *zstd.Encoder
}

func (w *zstdFlushWriter) Write(p []byte) (int, error) { return w.enc.Write(p) }
func (w *zstdFlushWriter) Flush() error                { return w.enc.Flush() }

type zstdConfig struct {
	level  zstd.EncoderLevel
	window int
	maxMem uint64
}

// ZstdOption configures [NewZstdConnWrapper].
type ZstdOption func(*zstdConfig)

// WithZstdLevel sets the Zstandard compression level.
func WithZstdLevel(level zstd.EncoderLevel) ZstdOption {
	return func(c *zstdConfig) { c.level = level }
}

// WithZstdWindow sets the maximum window size for the encoder.
func WithZstdWindow(size int) ZstdOption {
	return func(c *zstdConfig) { c.window = size }
}

// WithZstdDecoderMaxMemory sets the decoder memory limit.
func WithZstdDecoderMaxMemory(n uint64) ZstdOption {
	return func(c *zstdConfig) { c.maxMem = n }
}

var _ ConnWrapper = (*ZstdConnWrapper)(nil)
var _ flushWriter = (*zstdFlushWriter)(nil)
var _ io.Reader = (*zstd.Decoder)(nil)
