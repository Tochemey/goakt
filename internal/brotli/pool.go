/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package brotli

import (
	"io"
	"sync"

	"github.com/andybalholm/brotli"
)

// Pool for reusing Brotli readers
var readerPool = sync.Pool{
	New: func() any {
		return brotli.NewReader(nil)
	},
}

// Pool for reusing Brotli writers at different compression levels
var writerPools = make(map[int]*sync.Pool)
var writerPoolsMutex sync.RWMutex

// getWriterPool returns or creates a pool for the given compression level
func getWriterPool(level int) *sync.Pool {
	writerPoolsMutex.RLock()
	pool, exists := writerPools[level]
	writerPoolsMutex.RUnlock()

	if exists {
		return pool
	}

	writerPoolsMutex.Lock()
	defer writerPoolsMutex.Unlock()

	// Double-check in case another goroutine created it
	if pool, exists := writerPools[level]; exists {
		return pool
	}

	pool = &sync.Pool{
		New: func() any {
			return brotli.NewWriterLevel(nil, level)
		},
	}
	writerPools[level] = pool
	return pool
}

// pooledBrotliDecompressor wraps a pooled Brotli reader to satisfy connect.Decompressor.
type pooledBrotliDecompressor struct {
	*brotli.Reader
}

// Close resets and returns the Brotli reader to the pool.
func (b *pooledBrotliDecompressor) Close() error {
	if b.Reader != nil {
		_ = b.Reset(nil) // Reset the reader state
		readerPool.Put(b.Reader)
		b.Reader = nil
	}
	return nil
}

// pooledBrotliCompressor wraps a pooled Brotli writer to satisfy connect.Compressor.
type pooledBrotliCompressor struct {
	*brotli.Writer
	pool *sync.Pool
}

// Write compresses data using the underlying Brotli writer.
func (b *pooledBrotliCompressor) Write(p []byte) (n int, err error) {
	if b.Writer == nil {
		return 0, io.ErrClosedPipe
	}
	return b.Writer.Write(p)
}

// Close finalizes compression and returns the writer to the pool.
func (b *pooledBrotliCompressor) Close() error {
	if b.Writer == nil {
		return nil
	}

	// Flush and close the writer
	err := b.Writer.Close()

	// Reset and return to pool
	b.Writer.Reset(nil)
	b.pool.Put(b.Writer)
	b.Writer = nil

	return err
}

// Reset resets the writer to write to a new destination.
func (b *pooledBrotliCompressor) Reset(w io.Writer) {
	if b.Writer != nil {
		b.Writer.Reset(w)
	}
}
