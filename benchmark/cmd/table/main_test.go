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

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const before = `goos: linux
goarch: amd64
pkg: github.com/tochemey/goakt/v4/benchmark/comparison
cpu: AMD EPYC 9R14
BenchmarkTell-16   	10000000	        91.6 ns/op	  10920000 msgs/s	      12 B/op	       0 allocs/op
BenchmarkTell-16   	10000000	        93.6 ns/op	  10700000 msgs/s	      12 B/op	       0 allocs/op
BenchmarkTell-16   	10000000	        89.6 ns/op	  11100000 msgs/s	      12 B/op	       0 allocs/op
BenchmarkSpawnStop-16   	  137000	      7278 ns/op	    137400 actors/s	    2056 B/op	      27 allocs/op
BenchmarkMemoryPerActor-16   	       1	 265400000 ns/op	      1064 B/actor	74820000 B/op	 1050000 allocs/op
BenchmarkGrainMemoryPerGrain-16   	       1	 819100000 ns/op	       398.6 B/grain	897300000 B/op	 6680000 allocs/op
BenchmarkGrainAsk-16   	 1000000	       810.9 ns/op	   1233200 msgs/s	       0 B/op	       0 allocs/op
--- FAIL: BenchmarkBroken-16
PASS
`

const after = `cpu: AMD EPYC 9R14
BenchmarkTell-16   	10000000	        69.8 ns/op	  14320000 msgs/s	      18 B/op	       0 allocs/op
BenchmarkSpawnStop-16   	  140000	      7103 ns/op	    140800 actors/s	    1928 B/op	      25 allocs/op
BenchmarkMemoryPerActor-16   	       1	 270500000 ns/op	      1065 B/actor	68720000 B/op	  950000 allocs/op
BenchmarkGrainAsk-16   	 1000000	      1021 ns/op	    979500 msgs/s	       0 B/op	       0 allocs/op
`

func writeResults(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	a, b := filepath.Join(dir, "before.txt"), filepath.Join(dir, "after.txt")
	require.NoError(t, os.WriteFile(a, []byte(before), 0o600))
	require.NoError(t, os.WriteFile(b, []byte(after), 0o600))
	return a, b
}

func TestParseResult(t *testing.T) {
	t.Run("parses a result line into its units", func(t *testing.T) {
		name, values, ok := parseResult("BenchmarkTell-8   36158917   74.97 ns/op   13337905 msgs/s   0 B/op   0 allocs/op")
		require.True(t, ok)
		assert.Equal(t, "Tell-8", name)
		assert.Equal(t, map[string]float64{"ns/op": 74.97, "msgs/s": 13337905, "B/op": 0, "allocs/op": 0}, values)
	})

	t.Run("rejects lines that are not results", func(t *testing.T) {
		for _, line := range []string{"", "PASS", "--- FAIL: BenchmarkBroken-16", "cpu: AMD EPYC 9R14", "BenchmarkTell-8 x 74.97 ns/op", "BenchmarkTell-8 10 74.97", "BenchmarkTell-8 10 fast ns/op"} {
			_, _, ok := parseResult(line)
			assert.False(t, ok, line)
		}
	})
}

func TestRun(t *testing.T) {
	a, b := writeResults(t)

	t.Run("renders the six tables with medians, factors and the best in bold", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, run([]string{"before=" + a, "after=" + b}, &out))
		text := out.String()

		assert.True(t, strings.HasPrefix(text, "Measured on AMD EPYC 9R14. Each cell is the median of all runs"))
		for _, title := range []string{"### Throughput", "### Time per operation", "### Bytes allocated per operation", "### Allocations per operation", "### Live heap per idle actor", "### Live heap per idle grain"} {
			assert.Contains(t, text, title)
		}

		// the median of 91.6, 93.6 and 89.6 is 91.6; the factor is against the first label
		assert.Contains(t, text, "| Tell-16 | 91.6 ns | **69.8 ns (0.76×)** |")
		assert.Contains(t, text, "| Tell-16 | 10.92M msgs/s | **14.32M msgs/s (1.31×)** |")
		assert.Contains(t, text, "| SpawnStop-16 | 7.28 µs | **7.10 µs (0.98×)** |")
		assert.Contains(t, text, "| SpawnStop-16 | 137K actors/s | **141K actors/s (1.02×)** |")
		assert.Contains(t, text, "| SpawnStop-16 | 2056 B | **1928 B (0.94×)** |")
		assert.Contains(t, text, "| SpawnStop-16 | 27 | **25 (0.93×)** |")
		// memory benchmarks appear only in their own table
		assert.Contains(t, text, "| MemoryPerActor-16 | **1064 B** | 1065 B (1.00×) |")
		assert.Contains(t, text, "| GrainMemoryPerGrain-16 | **399 B** | n/a |")
		assert.NotContains(t, text, "| MemoryPerActor-16 | 265.40 ms")
		assert.NotContains(t, text, "Broken")
	})

	t.Run("match and skip select benchmarks", func(t *testing.T) {
		var out bytes.Buffer
		require.NoError(t, run([]string{"-match", "^Tell", "before=" + a, "after=" + b}, &out))
		assert.Contains(t, out.String(), "Tell-16")
		assert.NotContains(t, out.String(), "SpawnStop-16")

		out.Reset()
		require.NoError(t, run([]string{"-skip", "^Grain", "before=" + a}, &out))
		assert.Contains(t, out.String(), "Tell-16")
		assert.NotContains(t, out.String(), "GrainAsk-16")
		assert.NotContains(t, out.String(), "### Live heap per idle grain")
	})

	t.Run("rejects bad arguments", func(t *testing.T) {
		var out bytes.Buffer
		for _, args := range [][]string{{}, {"before.txt"}, {"-match", "(", "before=" + a}, {"-skip", "(", "before=" + a}, {"-unknown", "before=" + a}} {
			err := run(args, &out)
			require.ErrorIs(t, err, errUsage, "%v", args)
		}

		err := run([]string{"before=" + filepath.Join(t.TempDir(), "missing.txt")}, &out)
		require.Error(t, err)
		require.NotErrorIs(t, err, errUsage)
	})
}

func TestFormat(t *testing.T) {
	assert.Equal(t, "3539.41 ms", formatDuration(3539410000, ""))
	assert.Equal(t, "7.28 µs", formatDuration(7278, ""))
	assert.Equal(t, "91.6 ns", formatDuration(91.6, ""))
	assert.Equal(t, "14.32M msgs/s", formatRate(14320000, "msgs/s"))
	assert.Equal(t, "137K actors/s", formatRate(137400, "actors/s"))
	assert.Equal(t, "900 grains/s", formatRate(900, "grains/s"))
	assert.Equal(t, 2.5, median([]float64{1, 4, 2, 3}))
	assert.Equal(t, 2.0, median([]float64{3, 1, 2}))
}
