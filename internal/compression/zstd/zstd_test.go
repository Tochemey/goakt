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

	pooledCompressor.zstdCompressor.mu.RLock()
	assert.True(t, pooledCompressor.zstdCompressor.closed)
	assert.Nil(t, pooledCompressor.zstdCompressor.encoder)
	pooledCompressor.zstdCompressor.mu.RUnlock()

	decompressor := newDecompressor()

	pooledDecompressor, ok := decompressor.(*pooledDecompressor)
	require.True(t, ok)

	require.NoError(t, decompressor.Reset(&compressed))

	decompressed, err := io.ReadAll(decompressor)
	require.NoError(t, err)
	assert.Equal(t, input, string(decompressed))

	require.NoError(t, decompressor.Close())

	pooledDecompressor.zstdDecompressor.mu.RLock()
	assert.True(t, pooledDecompressor.zstdDecompressor.closed)
	assert.Nil(t, pooledDecompressor.zstdDecompressor.decoder)
	pooledDecompressor.zstdDecompressor.mu.RUnlock()
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

	pooledCompressor.zstdCompressor.mu.RLock()
	assert.True(t, pooledCompressor.zstdCompressor.closed)
	assert.Nil(t, pooledCompressor.zstdCompressor.encoder)
	pooledCompressor.zstdCompressor.mu.RUnlock()

	assert.Less(t, compressed.Len(), len(input), "compression should reduce large payload size")

	decompressor := newDecompressor()

	pooledDecompressor, ok := decompressor.(*pooledDecompressor)
	require.True(t, ok)

	require.NoError(t, decompressor.Reset(&compressed))

	decompressed, err := io.ReadAll(decompressor)
	require.NoError(t, err)
	assert.Equal(t, input, string(decompressed))

	require.NoError(t, decompressor.Close())

	pooledDecompressor.zstdDecompressor.mu.RLock()
	assert.True(t, pooledDecompressor.zstdDecompressor.closed)
	assert.Nil(t, pooledDecompressor.zstdDecompressor.decoder)
	pooledDecompressor.zstdDecompressor.mu.RUnlock()

	post := currentHeapAlloc()
	if post > baseline {
		assert.LessOrEqual(t, post-baseline, uint64(maxExpectedGrowth), "heap should be near baseline after pooled cleanup")
	}
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
