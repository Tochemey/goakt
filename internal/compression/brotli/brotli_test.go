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

package brotli

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test basic compression/decompression functionality
func TestBrotliCompression(t *testing.T) {
	testCases := []struct {
		name  string
		level int
		data  string
	}{
		{"BestSpeed", BestSpeed, generateRepeatingString("Hello World! ", 12)},
		{"DefaultCompression", DefaultCompression, "Lorem ipsum dolor sit amet, consectetur adipiscing elit."},
		{"BestCompression", BestCompression, "The quick brown fox jumps over the lazy dog."},
		{"LargeRepeatingData", DefaultCompression, generateRepeatingString("This is a test string that repeats. ", 20)},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Get compressor and decompressor
			newDecompressor, newCompressor := brotliCompressions(tc.level)

			// Compress
			var compressed bytes.Buffer
			compressor := newCompressor()
			compressor.Reset(&compressed)

			n, err := compressor.Write([]byte(tc.data))
			require.NoError(t, err)
			assert.Equal(t, len(tc.data), n)

			err = compressor.Close()
			require.NoError(t, err)

			// Verify compression actually reduced size (for larger inputs)
			if len(tc.data) > 50 {
				assert.Less(t, compressed.Len(), len(tc.data), "Compression should reduce size")
			}

			// Decompress
			decompressor := newDecompressor()
			decompressor.Reset(&compressed)

			decompressed, err := io.ReadAll(decompressor)
			require.NoError(t, err)

			err = decompressor.Close()
			require.NoError(t, err)

			// Verify data integrity
			assert.Equal(t, tc.data, string(decompressed))
		})
	}
}

// Focused memory leak detection test
func TestMemoryLeakDetection(t *testing.T) {
	t.Skip("Skipping memory leak detection test - enable when needed")
	// if testing.Short() {
	// 	t.Skip("Skipping memory leak test in short mode")
	// }

	newDecompressor, newCompressor := brotliCompressions(DefaultCompression)
	testData := generateRandomString(500)

	// Test scenario: Create many objects and ensure they don't accumulate
	t.Run("ObjectAccumulation", func(t *testing.T) {
		// Baseline measurement after warmup
		for i := 0; i < 20; i++ {
			comp := newCompressor()
			decomp := newDecompressor()
			comp.Close()
			decomp.Close()
		}

		runtime.GC()
		runtime.GC()
		var baseline runtime.MemStats
		runtime.ReadMemStats(&baseline)

		// Test phase: create many objects in cycles
		cycles := 10
		objectsPerCycle := 100

		var peakMemory uint64
		for cycle := range cycles {
			// Create many objects
			var objects []any
			for range objectsPerCycle {
				comp := newCompressor()
				decomp := newDecompressor()
				objects = append(objects, comp, decomp)
			}

			// Use and close them
			for i := 0; i < len(objects); i += 2 {
				comp := objects[i].(connect.Compressor)
				decomp := objects[i+1].(connect.Decompressor)

				var buf bytes.Buffer
				comp.Reset(&buf)
				comp.Write([]byte(testData))
				comp.Close()

				decomp.Reset(&buf)
				io.ReadAll(decomp)
				decomp.Close()
			}

			// Measure memory after each cycle
			runtime.GC()
			runtime.GC()
			var m runtime.MemStats
			runtime.ReadMemStats(&m)

			if m.HeapInuse > peakMemory {
				peakMemory = m.HeapInuse
			}

			t.Logf("Cycle %d: HeapInuse=%d bytes", cycle+1, m.HeapInuse)
		}

		// Memory should not grow excessively
		memoryGrowth := peakMemory - baseline.HeapInuse
		maxAcceptableGrowth := uint64(2 * 1024 * 1024) // 2MB

		t.Logf("Peak memory growth: %d bytes", memoryGrowth)

		if memoryGrowth > maxAcceptableGrowth {
			t.Errorf("Excessive memory growth detected: %d bytes (max: %d)",
				memoryGrowth, maxAcceptableGrowth)
		} else {
			t.Logf("✅ Memory growth within acceptable limits")
		}
	})

	t.Run("LongRunningOperations", func(t *testing.T) {
		// Simulate long-running server scenario
		runtime.GC()
		runtime.GC()
		var start runtime.MemStats
		runtime.ReadMemStats(&start)

		// Run operations continuously and measure memory stability
		iterations := 2000
		measurementInterval := 200
		var measurements []uint64

		for i := range iterations {
			// Simulate typical request processing
			var compressed bytes.Buffer
			compressor := newCompressor()
			compressor.Reset(&compressed)
			compressor.Write([]byte(testData))
			compressor.Close()

			decompressor := newDecompressor()
			decompressor.Reset(&compressed)
			io.ReadAll(decompressor)
			decompressor.Close()

			// Take memory measurements periodically
			if i%measurementInterval == 0 && i > 0 {
				runtime.GC()
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				measurements = append(measurements, m.HeapInuse)
			}
		}

		// Analyze memory stability
		if len(measurements) < 3 {
			t.Fatal("Not enough measurements")
		}

		// Check if memory is growing consistently (leak indicator)
		growthCount := 0
		for i := 1; i < len(measurements); i++ {
			if measurements[i] > measurements[i-1] {
				growthCount++
			}
		}

		growthPercentage := float64(growthCount) / float64(len(measurements)-1) * 100
		t.Logf("Memory measurements: %v", measurements)
		t.Logf("Growth percentage: %.1f%%", growthPercentage)

		// Memory should not grow in more than 60% of measurements
		if growthPercentage > 60.0 {
			t.Errorf("Potential memory leak: %.1f%% of measurements show growth", growthPercentage)
		} else {
			t.Logf("✅ Memory appears stable over long-running operations")
		}

		// Final check: last measurement shouldn't be too much higher than first
		finalGrowth := int64(measurements[len(measurements)-1]) - int64(measurements[0])
		maxFinalGrowth := int64(1024 * 1024) // 1MB

		if finalGrowth > maxFinalGrowth {
			t.Errorf("Final memory growth too high: %d bytes", finalGrowth)
		} else {
			t.Logf("✅ Final memory growth acceptable: %d bytes", finalGrowth)
		}
	})
}

// Test compression effectiveness with data that should compress well
func TestCompressionEffectiveness(t *testing.T) {
	testCases := []struct {
		name        string
		data        string
		maxRatio    float64 // Maximum acceptable compression ratio (compressed/original)
		description string
	}{
		{
			name:        "RepeatingText",
			data:        generateRepeatingString("Hello World! ", 100),
			maxRatio:    0.1, // Should compress to less than 10% of original
			description: "Repeating text should compress very well",
		},
		{
			name:        "JSONLikeData",
			data:        generateJSONLikeData(500),
			maxRatio:    0.3, // Should compress to less than 30% of original
			description: "Structured data should compress reasonably well",
		},
		{
			name:        "LongText",
			data:        generateLongText(2000),
			maxRatio:    0.6, // Should compress to less than 60% of original
			description: "Long natural text should compress moderately well",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			newDecompressor, newCompressor := brotliCompressions(DefaultCompression)

			// Compress
			var compressed bytes.Buffer
			compressor := newCompressor()
			compressor.Reset(&compressed)

			n, err := compressor.Write([]byte(tc.data))
			require.NoError(t, err)
			assert.Equal(t, len(tc.data), n)

			err = compressor.Close()
			require.NoError(t, err)

			// Calculate compression ratio
			ratio := float64(compressed.Len()) / float64(len(tc.data))
			t.Logf("%s: Original=%d, Compressed=%d, Ratio=%.3f",
				tc.name, len(tc.data), compressed.Len(), ratio)

			// Verify compression effectiveness
			assert.Less(t, ratio, tc.maxRatio, tc.description)
			assert.Greater(t, compressed.Len(), 0, "Should produce output")

			// Verify decompression works correctly
			decompressor := newDecompressor()
			decompressor.Reset(&compressed)

			decompressed, err := io.ReadAll(decompressor)
			require.NoError(t, err)

			err = decompressor.Close()
			require.NoError(t, err)

			// Verify data integrity
			assert.Equal(t, tc.data, string(decompressed), "Decompressed data should match original")
		})
	}
}

// Test that pooling is working correctly by tracking object reuse
func TestPoolingBehavior(t *testing.T) {
	// Note: This test uses unsafe pointers for demonstration.
	// If you prefer to avoid unsafe, run TestPoolingBehaviorSafe instead.

	if testing.Short() {
		t.Skip("Skipping unsafe pointer test in short mode")
	}

	newDecompressor, newCompressor := brotliCompressions(DefaultCompression)
	testData := "This is test data for pooling verification - it needs to be long enough to ensure proper compression and decompression behavior while testing the pooling mechanism."

	t.Run("CompressorPooling", func(t *testing.T) {
		// Track compressor instances by their pointer addresses
		compressorAddresses := make(map[uintptr]int)

		// Phase 1: Create several compressors and track their addresses
		var compressors []*pooledBrotliCompressor
		for i := 0; i < 5; i++ {
			comp := newCompressor()

			// Try to cast to our pooled type - if it fails, skip this test
			pooledComp, ok := comp.(*pooledBrotliCompressor)
			if !ok {
				t.Skip("Compressor is not pooled type - skipping unsafe pointer test")
				return
			}

			compressors = append(compressors, pooledComp)

			// Track the underlying brotli.Writer address
			writerAddr := uintptr(unsafe.Pointer(pooledComp.Writer))
			compressorAddresses[writerAddr]++

			t.Logf("Created compressor %d with Writer address: %x", i, writerAddr)
		}

		// All addresses should be unique initially (no reuse yet)
		assert.Equal(t, 5, len(compressorAddresses), "Should have 5 unique compressor instances")

		// Phase 2: Close all compressors (return to pool)
		for i, comp := range compressors {
			var buf bytes.Buffer
			comp.Reset(&buf)
			comp.Write([]byte(testData))
			err := comp.Close()
			assert.NoError(t, err, "Compressor %d should close without error", i)
		}

		// Phase 3: Create new compressors - should reuse from pool
		var newCompressors []*pooledBrotliCompressor
		reusedAddresses := 0

		for i := range 3 {
			comp := newCompressor().(*pooledBrotliCompressor)
			newCompressors = append(newCompressors, comp)

			writerAddr := uintptr(unsafe.Pointer(comp.Writer))
			if _, existed := compressorAddresses[writerAddr]; existed {
				reusedAddresses++
				t.Logf("Reused compressor %d with Writer address: %x", i, writerAddr)
			} else {
				t.Logf("New compressor %d with Writer address: %x", i, writerAddr)
			}
		}

		// Should reuse at least some compressors from the pool
		assert.Greater(t, reusedAddresses, 0, "Should reuse at least one compressor from pool")
		t.Logf("Reused %d out of 3 compressors from pool", reusedAddresses)

		// Clean up
		for _, comp := range newCompressors {
			comp.Close()
		}
	})

	t.Run("DecompressorPooling", func(t *testing.T) {
		// Create some compressed data first
		compressor := newCompressor()
		var compressedData bytes.Buffer
		compressor.Reset(&compressedData)
		compressor.Write([]byte(testData))
		compressor.Close()

		// Track decompressor instances by their Reader addresses
		decompressorAddresses := make(map[uintptr]int)

		// Phase 1: Create several decompressors
		var decompressors []*pooledBrotliDecompressor
		for i := 0; i < 5; i++ {
			decomp := newDecompressor()

			// Try to cast to our pooled type
			pooledDecomp, ok := decomp.(*pooledBrotliDecompressor)
			if !ok {
				t.Skip("Decompressor is not pooled type - skipping unsafe pointer test")
				return
			}

			decompressors = append(decompressors, pooledDecomp)

			readerAddr := uintptr(unsafe.Pointer(pooledDecomp.Reader))
			decompressorAddresses[readerAddr]++

			t.Logf("Created decompressor %d with Reader address: %x", i, readerAddr)
		}

		assert.Equal(t, 5, len(decompressorAddresses), "Should have 5 unique decompressor instances")

		// Phase 2: Use and close all decompressors
		for i, decomp := range decompressors {
			decomp.Reset(bytes.NewReader(compressedData.Bytes()))
			result, err := io.ReadAll(decomp)
			assert.NoError(t, err, "Decompressor %d should read without error", i)
			assert.Equal(t, testData, string(result), "Decompressed data should match original")

			err = decomp.Close()
			assert.NoError(t, err, "Decompressor %d should close without error", i)
		}

		// Phase 3: Create new decompressors - should reuse from pool
		reusedAddresses := 0
		for i := range 3 {
			decomp := newDecompressor().(*pooledBrotliDecompressor)

			readerAddr := uintptr(unsafe.Pointer(decomp.Reader))
			if _, existed := decompressorAddresses[readerAddr]; existed {
				reusedAddresses++
				t.Logf("Reused decompressor %d with Reader address: %x", i, readerAddr)
			} else {
				t.Logf("New decompressor %d with Reader address: %x", i, readerAddr)
			}

			// Test that the reused decompressor works
			decomp.Reset(bytes.NewReader(compressedData.Bytes()))
			result, err := io.ReadAll(decomp)
			assert.NoError(t, err)
			assert.Equal(t, testData, string(result))
			decomp.Close()
		}

		assert.Greater(t, reusedAddresses, 0, "Should reuse at least one decompressor from pool")
		t.Logf("Reused %d out of 3 decompressors from pool", reusedAddresses)
	})

	t.Run("PoolIsolationByLevel", func(t *testing.T) {
		// Test that different compression levels use different pools
		_, newCompressor1 := brotliCompressions(BestSpeed)
		_, newCompressor2 := brotliCompressions(BestCompression)

		comp1 := newCompressor1()
		comp2 := newCompressor2()

		// Try to cast to pooled types
		pooledComp1, ok1 := comp1.(*pooledBrotliCompressor)
		pooledComp2, ok2 := comp2.(*pooledBrotliCompressor)

		if !ok1 || !ok2 {
			t.Skip("Compressors are not pooled type - skipping pool isolation test")
			return
		}

		// Different compression levels should use different writer pools
		assert.NotEqual(t, pooledComp1.pool, pooledComp2.pool, "Different compression levels should use different pools")

		pooledComp1.Close()
		pooledComp2.Close()

		// Verify they return to their respective pools
		comp1New := newCompressor1().(*pooledBrotliCompressor)
		comp2New := newCompressor2().(*pooledBrotliCompressor)

		assert.Equal(t, pooledComp1.pool, comp1New.pool, "Same compression level should use same pool")
		assert.Equal(t, pooledComp2.pool, comp2New.pool, "Same compression level should use same pool")
		assert.NotEqual(t, comp1New.pool, comp2New.pool, "Different levels should still use different pools")

		comp1New.Close()
		comp2New.Close()
	})
}

// Test concurrent usage
func TestConcurrentUsage(t *testing.T) {
	newDecompressor, newCompressor := brotliCompressions(DefaultCompression)
	testData := "Concurrent test data - " + generateRandomString(100)

	var wg sync.WaitGroup
	numGoroutines := 50
	errChan := make(chan error, numGoroutines)

	// Run concurrent compression/decompression
	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Compress
			var compressed bytes.Buffer
			compressor := newCompressor()
			compressor.Reset(&compressed)

			data := fmt.Sprintf("%s - goroutine %d", testData, id)
			_, err := compressor.Write([]byte(data))
			if err != nil {
				errChan <- fmt.Errorf("compression error in goroutine %d: %v", id, err)
				return
			}

			err = compressor.Close()
			if err != nil {
				errChan <- fmt.Errorf("compressor close error in goroutine %d: %v", id, err)
				return
			}

			// Decompress
			decompressor := newDecompressor()
			decompressor.Reset(&compressed)

			decompressed, err := io.ReadAll(decompressor)
			if err != nil {
				errChan <- fmt.Errorf("decompression error in goroutine %d: %v", id, err)
				return
			}

			err = decompressor.Close()
			if err != nil {
				errChan <- fmt.Errorf("decompressor close error in goroutine %d: %v", id, err)
				return
			}

			// Verify data integrity
			if string(decompressed) != data {
				errChan <- fmt.Errorf("data mismatch in goroutine %d", id)
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for any errors
	for err := range errChan {
		t.Error(err)
	}
}

// Memory usage test
func TestMemoryUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory test in short mode")
	}

	newDecompressor, newCompressor := brotliCompressions(DefaultCompression)
	testData := generateRandomString(1000)

	// Warm up the pools and stabilize memory
	t.Log("Warming up pools...")
	for range 50 {
		var compressed bytes.Buffer
		compressor := newCompressor()
		compressor.Reset(&compressed)
		compressor.Write([]byte(testData))
		compressor.Close()

		decompressor := newDecompressor()
		decompressor.Reset(&compressed)
		io.ReadAll(decompressor)
		decompressor.Close()
	}

	// Force multiple GC cycles to stabilize memory
	for range 3 {
		runtime.GC()
		runtime.GC() // Double GC to ensure cleanup
		time.Sleep(50 * time.Millisecond)
	}

	// Get baseline memory after warmup
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	t.Logf("Baseline memory after warmup: HeapInuse=%d, HeapAlloc=%d", m1.HeapInuse, m1.HeapAlloc)

	// Perform test operations in batches with monitoring
	iterations := 1000
	batchSize := 100
	var memSamples []uint64

	for batch := 0; batch < iterations/batchSize; batch++ {
		// Perform a batch of operations
		for range batchSize {
			var compressed bytes.Buffer
			compressor := newCompressor()
			compressor.Reset(&compressed)
			compressor.Write([]byte(testData))
			compressor.Close()

			decompressor := newDecompressor()
			decompressor.Reset(&compressed)
			io.ReadAll(decompressor)
			decompressor.Close()
		}

		// Force GC and measure memory
		runtime.GC()
		runtime.GC()
		time.Sleep(10 * time.Millisecond)

		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		memSamples = append(memSamples, m.HeapInuse)

		t.Logf("Batch %d: HeapInuse=%d bytes", batch+1, m.HeapInuse)
	}

	// Analyze memory growth pattern
	if len(memSamples) < 2 {
		t.Fatal("Not enough memory samples")
	}

	// Calculate memory growth over the test period
	firstSample := memSamples[0]
	lastSample := memSamples[len(memSamples)-1]
	totalGrowth := int64(lastSample) - int64(firstSample)

	t.Logf("Memory growth analysis:")
	t.Logf("  First sample: %d bytes", firstSample)
	t.Logf("  Last sample: %d bytes", lastSample)
	t.Logf("  Total growth: %d bytes", totalGrowth)
	t.Logf("  Growth per 1000 operations: %d bytes", totalGrowth)

	// Check for memory leak patterns
	// Look for consistently increasing memory usage
	increasingTrend := 0
	for i := 1; i < len(memSamples); i++ {
		if memSamples[i] > memSamples[i-1] {
			increasingTrend++
		}
	}

	trendPercentage := float64(increasingTrend) / float64(len(memSamples)-1) * 100
	t.Logf("  Increasing trend: %.1f%% of samples", trendPercentage)

	// More realistic thresholds based on Go runtime behavior
	maxAcceptableGrowth := int64(5 * 1024 * 1024) // 5MB total growth
	maxTrendPercentage := 70.0                    // Less than 70% increasing trend

	// Check total memory growth
	if totalGrowth > maxAcceptableGrowth {
		t.Errorf("Excessive memory growth: %d bytes (max acceptable: %d bytes)",
			totalGrowth, maxAcceptableGrowth)
	} else {
		t.Logf("✅ Memory growth within acceptable limits")
	}

	// Check for consistent memory leak pattern
	if trendPercentage > maxTrendPercentage {
		t.Errorf("Concerning memory leak pattern: %.1f%% increasing trend (max acceptable: %.1f%%)",
			trendPercentage, maxTrendPercentage)
	} else {
		t.Logf("✅ No concerning memory leak pattern detected")
	}

	// Additional check: memory should stabilize in later batches
	if len(memSamples) >= 6 {
		lastThird := memSamples[len(memSamples)*2/3:]
		var lastThirdTotal uint64
		for _, sample := range lastThird {
			lastThirdTotal += sample
		}
		lastThirdAvg := lastThirdTotal / uint64(len(lastThird))

		firstThird := memSamples[:len(memSamples)/3]
		var firstThirdTotal uint64
		for _, sample := range firstThird {
			firstThirdTotal += sample
		}
		firstThirdAvg := firstThirdTotal / uint64(len(firstThird))

		growthRate := float64(lastThirdAvg)/float64(firstThirdAvg) - 1.0
		t.Logf("  Growth rate (last third vs first third): %.1f%%", growthRate*100)

		if growthRate > 0.5 { // More than 50% growth from first to last third
			t.Errorf("Memory usage not stabilizing: %.1f%% growth rate", growthRate*100)
		} else {
			t.Logf("✅ Memory usage appears to be stabilizing")
		}
	}
}

// Benchmark compression performance
func BenchmarkCompression(b *testing.B) {
	testSizes := []int{100, 1000, 10000}
	levels := []int{BestSpeed, DefaultCompression, BestCompression}

	for _, size := range testSizes {
		for _, level := range levels {
			testData := generateRandomString(size)
			_, newCompressor := brotliCompressions(level)

			b.Run(fmt.Sprintf("Size%d_Level%d", size, level), func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					var buf bytes.Buffer
					compressor := newCompressor()
					compressor.Reset(&buf)
					compressor.Write([]byte(testData))
					compressor.Close()
				}
			})
		}
	}
}

// Benchmark decompression performance
func BenchmarkDecompression(b *testing.B) {
	testData := generateRandomString(1000)
	newDecompressor, newCompressor := brotliCompressions(DefaultCompression)

	// Pre-compress test data
	var compressed bytes.Buffer
	compressor := newCompressor()
	compressor.Reset(&compressed)
	compressor.Write([]byte(testData))
	compressor.Close()

	compressedData := compressed.Bytes()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		decompressor := newDecompressor()
		decompressor.Reset(bytes.NewReader(compressedData))
		io.ReadAll(decompressor)
		decompressor.Close()
	}
}

// Test error handling
func TestErrorHandling(t *testing.T) {
	newDecompressor, newCompressor := brotliCompressions(DefaultCompression)

	// Test writing to closed compressor
	compressor := newCompressor()
	compressor.Close()

	// This should handle gracefully
	n, err := compressor.Write([]byte("test"))
	assert.Error(t, err)
	assert.Equal(t, 0, n)

	// Test double close
	decompressor := newDecompressor()
	err = decompressor.Close()
	assert.NoError(t, err)

	err = decompressor.Close()
	assert.NoError(t, err) // Should handle double close gracefully
}

// Test with Connect options
func TestConnectIntegration(t *testing.T) {
	// Test that options can be created without errors
	option1 := WithCompression()
	assert.NotNil(t, option1)

	option2 := WithCompressionLevel(BestSpeed)
	assert.NotNil(t, option2)

	option3 := WithCompressionLevel(BestCompression)
	assert.NotNil(t, option3)

	// Verify the compression option has both client and handler options
	compressionOpt, ok := option1.(compressionOption)
	assert.True(t, ok, "Should be compressionOption type")
	assert.NotNil(t, compressionOpt.ClientOption)
	assert.NotNil(t, compressionOpt.HandlerOption)
}

// Load test to simulate high traffic
func TestHighLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping load test in short mode")
	}

	newDecompressor, newCompressor := brotliCompressions(DefaultCompression)
	testData := generateRandomString(5000)

	// Simulate high concurrent load
	var wg sync.WaitGroup
	numWorkers := 100
	operationsPerWorker := 50
	errors := make(chan error, numWorkers*operationsPerWorker)

	start := time.Now()

	for worker := 0; worker < numWorkers; worker++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for op := 0; op < operationsPerWorker; op++ {
				// Compress
				var compressed bytes.Buffer
				compressor := newCompressor()
				compressor.Reset(&compressed)

				if _, err := compressor.Write([]byte(testData)); err != nil {
					errors <- fmt.Errorf("worker %d op %d compress: %v", workerID, op, err)
					continue
				}

				if err := compressor.Close(); err != nil {
					errors <- fmt.Errorf("worker %d op %d compressor close: %v", workerID, op, err)
					continue
				}

				// Decompress
				decompressor := newDecompressor()
				decompressor.Reset(&compressed)

				if _, err := io.ReadAll(decompressor); err != nil {
					errors <- fmt.Errorf("worker %d op %d decompress: %v", workerID, op, err)
					continue
				}

				if err := decompressor.Close(); err != nil {
					errors <- fmt.Errorf("worker %d op %d decompressor close: %v", workerID, op, err)
				}
			}
		}(worker)
	}

	wg.Wait()
	close(errors)

	duration := time.Since(start)
	totalOps := numWorkers * operationsPerWorker

	// Check for errors
	errorCount := 0
	for err := range errors {
		t.Error(err)
		errorCount++
	}

	t.Logf("Completed %d operations in %v (%.2f ops/sec) with %d errors",
		totalOps, duration, float64(totalOps)/duration.Seconds(), errorCount)

	assert.Equal(t, 0, errorCount, "Should have no errors in load test")
}

// Helper function to generate random strings
//
//nolint:gosec
func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 "
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// Helper function to generate repeating strings (compresses well)
func generateRepeatingString(pattern string, repeats int) string {
	var result string
	for i := 0; i < repeats; i++ {
		result += pattern
	}
	return result
}

// Helper function to generate JSON-like data
func generateJSONLikeData(entries int) string {
	var result bytes.Buffer
	result.WriteString(`{"users":[`)

	for i := 0; i < entries; i++ {
		if i > 0 {
			result.WriteString(",")
		}
		result.WriteString(fmt.Sprintf(`{"id":%d,"name":"User%d","email":"user%d@example.com","active":true}`, i, i, i))
	}

	result.WriteString(`]}`)
	return result.String()
}

// Helper function to generate natural text
func generateLongText(words int) string {
	commonWords := []string{
		"the", "quick", "brown", "fox", "jumps", "over", "lazy", "dog", "and", "runs",
		"through", "forest", "with", "great", "speed", "while", "avoiding", "obstacles",
		"that", "appear", "along", "the", "path", "making", "sure", "to", "stay", "safe",
		"from", "any", "potential", "dangers", "that", "might", "be", "lurking", "nearby",
	}

	var result strings.Builder
	for i := 0; i < words; i++ {
		if i > 0 {
			result.WriteString(" ")
		}
		result.WriteString(commonWords[rand.Intn(len(commonWords))]) //nolint:gosec
	}

	return result.String()
}
