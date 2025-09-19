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

import (
	"bytes"
	"errors"
	"io"
	"runtime"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nolint:staticcheck
func TestZstdCompressionRoundTrip(t *testing.T) {
	newDecompressor, newCompressor := zstdCompressions()

	input := strings.Repeat("zstd keeps memory tidy! ", 32)

	var compressed bytes.Buffer
	compressor := newCompressor()

	pooledCompressor, ok := compressor.(*pooledCompressor)
	if !ok {
		errComp, okErr := compressor.(*errorCompressor)
		if okErr {
			t.Fatalf("newZstdCompressor returned error: %v", errComp.err)
		}
		t.Fatalf("unexpected compressor type: %T", compressor)
	}

	compressor.Reset(&compressed)

	n, err := compressor.Write([]byte(input))
	require.NoError(t, err)
	assert.Equal(t, len(input), n)

	require.NoError(t, compressor.Close())

	pooledCompressor.mu.Lock()
	assert.True(t, pooledCompressor.returned)
	assert.Nil(t, pooledCompressor.zstdCompressor)
	pooledCompressor.mu.Unlock()

	decompressor := newDecompressor()

	pooledDecompressor, ok := decompressor.(*pooledDecompressor)
	require.True(t, ok)

	require.NoError(t, decompressor.Reset(&compressed))

	decompressed, err := io.ReadAll(decompressor)
	require.NoError(t, err)
	assert.Equal(t, input, string(decompressed))

	require.NoError(t, decompressor.Close())

	pooledDecompressor.mu.Lock()
	assert.True(t, pooledDecompressor.returned)
	assert.Nil(t, pooledDecompressor.zstdDecompressor)
	pooledDecompressor.mu.Unlock()
}

// nolint
func TestZstdCompressionLargePayload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large payload test in short mode")
	}

	const maxExpectedGrowth = 10 << 20 // 10MB slack for allocator noise
	baseline := currentHeapAlloc()

	newDecompressor, newCompressor := zstdCompressions()
	input := strings.Repeat("payload-", 1<<17) // ~1MB of highly compressible data

	var compressed bytes.Buffer
	compressor := newCompressor()

	pooledCompressor, ok := compressor.(*pooledCompressor)
	if !ok {
		errComp, okErr := compressor.(*errorCompressor)
		if okErr {
			t.Fatalf("newZstdCompressor returned error: %v", errComp.err)
		}
		t.Fatalf("unexpected compressor type: %T", compressor)
	}

	compressor.Reset(&compressed)

	n, err := compressor.Write([]byte(input))
	require.NoError(t, err)
	assert.Equal(t, len(input), n)

	require.NoError(t, compressor.Close())

	pooledCompressor.mu.Lock()
	assert.True(t, pooledCompressor.returned)
	assert.Nil(t, pooledCompressor.zstdCompressor)
	pooledCompressor.mu.Unlock()

	assert.Less(t, compressed.Len(), len(input), "compression should reduce large payload size")

	decompressor := newDecompressor()

	pooledDecompressor, ok := decompressor.(*pooledDecompressor)
	require.True(t, ok)

	require.NoError(t, decompressor.Reset(&compressed))

	decompressed, err := io.ReadAll(decompressor)
	require.NoError(t, err)
	assert.Equal(t, input, string(decompressed))

	require.NoError(t, decompressor.Close())

	pooledDecompressor.mu.Lock()
	assert.True(t, pooledDecompressor.returned)
	assert.Nil(t, pooledDecompressor.zstdDecompressor)
	pooledDecompressor.mu.Unlock()

	post := currentHeapAlloc()
	if post > baseline {
		assert.LessOrEqual(t, post-baseline, uint64(maxExpectedGrowth), "heap should be near baseline after pooled cleanup")
	}
}

func TestZstdDecompressorReuseAfterClose(t *testing.T) {
	newDecompressor, newCompressor := zstdCompressions()

	firstPayload := "first payload"
	secondPayload := "second payload"

	var compressed bytes.Buffer
	compressor := newCompressor()
	compressor.Reset(&compressed)
	_, err := compressor.Write([]byte(firstPayload))
	require.NoError(t, err)
	require.NoError(t, compressor.Close())

	decompressor, ok := newDecompressor().(*pooledDecompressor)
	require.True(t, ok)

	require.NoError(t, decompressor.Reset(&compressed))
	decoded, err := io.ReadAll(decompressor)
	require.NoError(t, err)
	assert.Equal(t, firstPayload, string(decoded))
	require.NoError(t, decompressor.Close())

	compressed.Reset()
	compressor = newCompressor()
	compressor.Reset(&compressed)
	_, err = compressor.Write([]byte(secondPayload))
	require.NoError(t, err)
	require.NoError(t, compressor.Close())

	require.NoError(t, decompressor.Reset(&compressed))
	decoded, err = io.ReadAll(decompressor)
	require.NoError(t, err)
	assert.Equal(t, secondPayload, string(decoded))
	require.NoError(t, decompressor.Close())
}

func TestZstdCompressorReuseAfterClose(t *testing.T) {
	_, newCompressor := zstdCompressions()

	var buf bytes.Buffer
	compressor := newCompressor()

	compressor.Reset(&buf)
	_, err := compressor.Write([]byte("initial"))
	require.NoError(t, err)
	require.NoError(t, compressor.Close())

	buf.Reset()
	compressor.Reset(&buf)
	_, err = compressor.Write([]byte("reused"))
	require.NoError(t, err)
	require.NoError(t, compressor.Close())

	pooled, ok := compressor.(*pooledCompressor)
	require.True(t, ok)

	pooled.mu.Lock()
	assert.True(t, pooled.returned)
	assert.Nil(t, pooled.zstdCompressor)
	pooled.mu.Unlock()
}

func TestZstdCompressorWritePropagatesInitError(t *testing.T) {
	_, newCompressor := zstdCompressions()

	compressor := newCompressor()
	compressor.Reset(io.Discard)

	pooled, ok := compressor.(*pooledCompressor)
	require.True(t, ok)

	pooled.zstdCompressor.mu.Lock()
	sentinel := errors.New("encoder boom")
	pooled.zstdCompressor.initErr = sentinel
	pooled.zstdCompressor.mu.Unlock()

	_, err := compressor.Write([]byte("data"))
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)

	err = compressor.Close()
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel)
}

func TestZstdDecompressorReadAfterClose(t *testing.T) {
	newDecompressor, newCompressor := zstdCompressions()

	var compressed bytes.Buffer
	compressor := newCompressor()
	compressor.Reset(&compressed)
	_, err := compressor.Write([]byte("payload"))
	require.NoError(t, err)
	require.NoError(t, compressor.Close())

	decompressor, ok := newDecompressor().(*pooledDecompressor)
	require.True(t, ok)
	require.NoError(t, decompressor.Reset(&compressed))

	require.NoError(t, decompressor.zstdDecompressor.Close())

	buf := make([]byte, 4)
	n, err := decompressor.Read(buf)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)

	require.NoError(t, decompressor.Close())
}

// nolint
func TestWithCompressionOption(t *testing.T) {
	var opt connect.Option = WithCompression()

	compressionOpt, ok := opt.(compressionOption)
	require.True(t, ok)

	require.NotNil(t, compressionOpt.ClientOption)
	require.NotNil(t, compressionOpt.HandlerOption)
}

func currentHeapAlloc() uint64 {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}
