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

package zstd

import (
	"io"
	"runtime"
	"sync"

	"connectrpc.com/connect"
	"github.com/klauspost/compress/zstd"
)

// Name is the identifier for ZStandard compression in Connect.
const Name = "zstd"

// WithCompression registers pooled Zstandard compressors/decoders for Connect clients and handlers.
// The pools keep a small amount of working memory alive between requests, so callers should expect
// a modest resident footprint, but every Close clears references and returns the instance to the pool.
// That GC-friendly cleanup lets Go reclaim buffers when the pool is idle, preventing unbounded growth
// that could otherwise surface as OOM pressure during bursty workloads.
func WithCompression() connect.Option {
	decompressor, compressor := zstdCompressions()
	return compressionOption{
		ClientOption:  connect.WithAcceptCompression(Name, decompressor, compressor),
		HandlerOption: connect.WithCompression(Name, decompressor, compressor),
	}
}

func newDecoder(r io.Reader) (*zstd.Decoder, error) {
	return zstd.NewReader(r,
		zstd.WithDecoderConcurrency(0),    // Auto-detect CPU cores
		zstd.WithDecoderLowmem(false),     // Use more memory for speed
		zstd.WithDecoderMaxMemory(64<<20), // 64MB max memory limit
	)
}

func newEncoder(w io.Writer) (*zstd.Encoder, error) {
	concurrency := runtime.GOMAXPROCS(0)
	if concurrency < 1 {
		concurrency = 1
	}
	return zstd.NewWriter(w,
		zstd.WithEncoderLevel(zstd.SpeedFastest), // Prioritize speed for gRPC
		zstd.WithWindowSize(4<<20),               // 4MB window for better compression on large payloads
		zstd.WithEncoderConcurrency(concurrency), // Use available cores without violating encoder requirements
		zstd.WithLowerEncoderMem(false),          // Use more memory for better performance
		zstd.WithEncoderPadding(1),               // Add padding to avoid small block inefficiencies
	)
}

// compressionOption bundles client and handler compression options.
type compressionOption struct {
	connect.ClientOption
	connect.HandlerOption
}

// zstdDecompressor is a thread-safe wrapper around a zstd Decoder with proper resource management.
type zstdDecompressor struct {
	decoder *zstd.Decoder
	mu      sync.RWMutex // Protect concurrent access to decoder
	closed  bool         // Track if decompressor has been closed
}

func (c *zstdDecompressor) Read(bytes []byte) (int, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.closed || c.decoder == nil {
		return 0, io.EOF
	}
	return c.decoder.Read(bytes)
}

func (c *zstdDecompressor) Reset(rdr io.Reader) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		c.closed = false
	}

	if c.decoder == nil {
		var err error
		c.decoder, err = newDecoder(rdr)
		return err
	}
	return c.decoder.Reset(rdr)
}

func (c *zstdDecompressor) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil // Already closed
	}

	c.closed = true
	if c.decoder != nil {
		c.decoder.Close()
		c.decoder = nil // Prevent reuse and help GC
	}
	return nil
}

// zstdCompressor wraps zstd.Encoder with proper resource management.
type zstdCompressor struct {
	encoder *zstd.Encoder
	mu      sync.RWMutex
	closed  bool
	initErr error
}

func (c *zstdCompressor) Write(p []byte) (int, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.initErr != nil {
		return 0, c.initErr
	}
	if c.closed || c.encoder == nil {
		return 0, io.ErrClosedPipe
	}
	return c.encoder.Write(p)
}

func (c *zstdCompressor) Reset(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.encoder == nil {
		enc, err := newEncoder(w)
		if err != nil {
			c.initErr = err
			c.closed = true
			return
		}
		c.encoder = enc
		c.initErr = nil
		c.closed = false
		return
	}

	c.initErr = nil
	c.closed = false
	c.encoder.Reset(w)
}

func (c *zstdCompressor) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	if c.encoder != nil {
		err := c.encoder.Close()
		c.encoder = nil // Help GC
		if err != nil {
			return err
		}
	}
	return c.initErr
}

// zstdCompressions returns factory functions for Zstd compressors and decompressors.
func zstdCompressions() (
	newDecompressor func() connect.Decompressor,
	newCompressor func() connect.Compressor,
) {
	newDecompressor = func() connect.Decompressor {
		return newZstdDecompressor()
	}

	newCompressor = func() connect.Compressor {
		return newZstdCompressor()
	}

	return
}

// newZstdDecompressor returns a new Zstd Decompressor with proper resource management.
func newZstdDecompressor() connect.Decompressor {
	// Get from pool to reduce allocations
	d := decompressorPool.Get().(*zstdDecompressor)

	// Reset state
	d.mu.Lock()
	d.closed = false
	d.decoder = nil
	d.mu.Unlock()

	return &pooledDecompressor{
		zstdDecompressor: d,
	}
}

// newZstdCompressor returns a new Zstd Compressor with proper resource management.
func newZstdCompressor() connect.Compressor {
	// Create encoder with safe defaults
	enc, err := newEncoder(nil)
	if err != nil {
		return &errorCompressor{err: err}
	}

	// Get from pool to reduce allocations
	c := compressorPool.Get().(*zstdCompressor)

	// Reset state
	c.mu.Lock()
	c.closed = false
	c.encoder = enc
	c.initErr = nil
	c.mu.Unlock()

	return &pooledCompressor{
		zstdCompressor: c,
	}
}
