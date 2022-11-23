package sharding

import (
	"fmt"
	"math/rand"
	"testing"
)

const numTests = 100000
const numShards = 50

func TestIntegerSharding(t *testing.T) {
	calc := NewIntSharder(numShards)
	distribution := make(map[int]int)
	for i := 0; i < numTests; i++ {
		shardNo := calc(rand.Int31()) // nolint
		distribution[shardNo]++
	}
	if len(distribution) > numShards {
		t.Fatalf("Expected a maximum of %d shards but got %d", numShards, len(distribution))
	}
	for k, v := range distribution {
		if v > 4*(numTests/numShards) {
			t.Fatalf("Shard on %d is unbalanced with %d elements (typical = %d, max = %d)", k, v, numTests/numShards, 4*numTests/numShards)
		}
	}
}

func BenchmarkIntegerSharding(b *testing.B) {
	calc := NewIntSharder(numShards)
	for i := 0; i < b.N; i++ {
		calc(i)
	}
}

func TestStringSharding(t *testing.T) {
	sharder := NewStringSharder(numShards)
	distribution := make(map[int]int)

	for i := 0; i < numTests; i++ {
		randstr := make([]byte, 128)
		for p := 0; p < len(randstr); p++ {
			randstr[p] = byte(rand.Int31n(64) + '@') // nolint
		}
		shardNo := sharder(randstr)
		distribution[shardNo]++
	}
	if len(distribution) > numShards {
		t.Fatalf("Expected a maximum of %d shards but got %d", numShards, len(distribution))
	}
	for k, v := range distribution {
		t.Logf("Shard %-4d: %d", k, v)
		if v > 4*(numTests/numShards) {
			t.Fatalf("Shard on %d is unbalanced with %d elements (typical = %d, max = %d)", k, v, numTests/numShards, 4*numTests/numShards)
		}
	}
}

func BenchmarkStringSharding(b *testing.B) {
	tests := make([]string, b.N)
	for i := 0; i < b.N; i++ {
		tests[i] = fmt.Sprintf("%08x", rand.Int63()) // nolint
	}
	b.ResetTimer()
	sharder := NewStringSharder(numShards)
	for i := 0; i < b.N; i++ {
		sharder(tests[i])
	}
}
