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

package zstd

import "sync"

// Pool for reusing decompressors to reduce allocations
var decompressorPool = sync.Pool{
	New: func() any {
		return &zstdDecompressor{}
	},
}

// Pool for reusing compressors to reduce allocations
var compressorPool = sync.Pool{
	New: func() any {
		return &zstdCompressor{}
	},
}

// pooledDecompressor wraps zstdDecompressor to return to pool on close
type pooledDecompressor struct {
	*zstdDecompressor
	returned bool
	mu       sync.Mutex
}

func (p *pooledDecompressor) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.returned {
		return nil
	}

	err := p.zstdDecompressor.Close()

	// Return to pool for reuse
	decompressorPool.Put(p.zstdDecompressor)
	p.returned = true

	return err
}

// pooledCompressor wraps zstdCompressor to return to pool on close
type pooledCompressor struct {
	*zstdCompressor
	returned bool
	mu       sync.Mutex
}

func (p *pooledCompressor) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.returned {
		return nil
	}

	err := p.zstdCompressor.Close()

	// Return to pool for reuse
	compressorPool.Put(p.zstdCompressor)
	p.returned = true

	return err
}
