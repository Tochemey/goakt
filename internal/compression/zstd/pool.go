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
	"sync"
)

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

	var err error
	if p.zstdDecompressor != nil {
		err = p.zstdDecompressor.Close()
		// Return to pool for reuse
		decompressorPool.Put(p.zstdDecompressor)
		p.zstdDecompressor = nil
	}

	p.returned = true

	return err
}

func (p *pooledDecompressor) Reset(r io.Reader) error {
	p.mu.Lock()
	if p.returned || p.zstdDecompressor == nil {
		d := decompressorPool.Get().(*zstdDecompressor)
		d.mu.Lock()
		d.closed = false
		d.decoder = nil
		d.mu.Unlock()
		p.zstdDecompressor = d
		p.returned = false
	}
	d := p.zstdDecompressor
	p.mu.Unlock()

	if d == nil {
		return io.ErrClosedPipe
	}

	return d.Reset(r)
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

	var err error
	if p.zstdCompressor != nil {
		err = p.zstdCompressor.Close()
		// Return to pool for reuse
		compressorPool.Put(p.zstdCompressor)
		p.zstdCompressor = nil
	}

	p.returned = true

	return err
}

func (p *pooledCompressor) Reset(w io.Writer) {
	p.mu.Lock()
	if p.returned || p.zstdCompressor == nil {
		c := compressorPool.Get().(*zstdCompressor)
		c.mu.Lock()
		c.closed = false
		c.encoder = nil
		c.initErr = nil
		c.mu.Unlock()
		p.zstdCompressor = c
		p.returned = false
	}
	c := p.zstdCompressor
	p.mu.Unlock()

	if c == nil {
		return
	}

	c.Reset(w)
}
