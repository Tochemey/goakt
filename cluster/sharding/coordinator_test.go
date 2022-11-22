package sharding

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

// These are sort-of-sensible defaults for benchmarks
const (
	benchmarkShardCount = 1000
	benchmarkNodeCount  = 9
)

func TestCoordinator(t *testing.T) {
	sc := NewShardsCoordinator()

	const maxShards = 1000
	weights := make([]int, maxShards)
	for i := range weights {
		weights[i] = int(rand.Int31n(100)) + 1
	}
	testShardCoordinator(t, sc, maxShards, weights)
}

func TestMarshalUnmarshalBinary(t *testing.T) {
	assert := require.New(t)
	weights := make([]int, benchmarkShardCount)
	for i := range weights {
		weights[i] = int(rand.Int31n(100)) + 1
	}
	sc := NewShardsCoordinator()

	assert.NoError(sc.Init(benchmarkShardCount, weights))

	var nodes []string
	for i := 0; i < benchmarkNodeCount; i++ {
		nodes = append(nodes, fmt.Sprintf("Node%04d", i))
	}
	sc.UpdateNodes(nodes...)
	buf, err := sc.MarshalBinary()
	assert.NoError(err)

	t.Logf("%d shards = %d bytes", benchmarkShardCount, len(buf))
	newManager := NewShardsCoordinator()
	assert.NoError(newManager.UnmarshalBinary(buf))

	assert.Equal(sc.TotalWeight(), newManager.TotalWeight(), "Total weight for both should be the same")

	assert.Equal(len(newManager.Shards()), len(sc.Shards()), "Number of shards should be the same")

	for i := 0; i < benchmarkShardCount; i++ {
		old := sc.MapToNode(i)
		shard := newManager.MapToNode(i)
		assert.Equalf(shard.NodeID(), old.NodeID(), "Shard %d is in a different place", i)
		assert.Equalf(shard.Weight(), old.Weight(), "Shard %d has different weight", i)
		assert.Equalf(shard.ID(), old.ID(), "Shard %d has different ID", i)
	}
}

// Benchmark the performance on add and remove node
func BenchmarkCoordinator(b *testing.B) {
	sc := NewShardsCoordinator()
	weights := make([]int, benchmarkShardCount)
	for i := range weights {
		weights[i] = int(rand.Int31n(100)) + 1
	}

	// nolint - don't care about error returns here
	_ = sc.Init(benchmarkShardCount, weights)

	var nodes []string
	for i := 0; i < benchmarkNodeCount; i++ {
		nodes = append(nodes, fmt.Sprintf("Node%04d", i))
	}
	sc.UpdateNodes(nodes...)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		nodes = nodes[2:]
		nodes = append(nodes, fmt.Sprintf("%d", i))
		nodes = append(nodes, fmt.Sprintf("%db", i))
		sc.UpdateNodes(nodes...)
	}
}

// Benchmark lookups on node
func BenchmarkMapToNode(b *testing.B) {
	sm := NewShardsCoordinator()
	weights := make([]int, benchmarkShardCount)
	for i := range weights {
		weights[i] = int(rand.Int31n(100)) + 1
	}
	// nolint - won't check error return
	_ = sm.Init(benchmarkShardCount, weights)

	var nodes []string
	for i := 0; i < benchmarkNodeCount; i++ {
		nodes = append(nodes, fmt.Sprintf("Node%04d", i))
	}
	sm.UpdateNodes(nodes...)
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		sm.MapToNode(rand.Intn(benchmarkShardCount))
	}
}

// Benchmark init function
func BenchmarkCoordinator_Init(b *testing.B) {
	weights := make([]int, benchmarkShardCount)
	for i := range weights {
		weights[i] = int(rand.Int31n(100)) + 1
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sc := NewShardsCoordinator()
		// nolint - won't check for errors
		sc.Init(benchmarkShardCount, weights)
	}
}

// Benchmark shard weight total (it should be *really* quick)
func BenchmarkCoordinator_TotalWeight(b *testing.B) {
	weights := make([]int, benchmarkShardCount)
	for i := range weights {
		weights[i] = int(rand.Int31n(100)) + 1
	}
	sc := NewShardsCoordinator()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sc.TotalWeight()
	}
}

func BenchmarkCoordinator_MarshalBinary(b *testing.B) {
	weights := make([]int, benchmarkShardCount)
	for i := range weights {
		weights[i] = int(rand.Int31n(100)) + 1
	}
	sc := NewShardsCoordinator()
	// nolint - wont't check error return
	sc.Init(benchmarkShardCount, weights)
	var nodes []string
	for i := 0; i < benchmarkNodeCount; i++ {
		nodes = append(nodes, fmt.Sprintf("Node%04d", i))
	}
	sc.UpdateNodes(nodes...)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// nolint - won't check error returns
		sc.MarshalBinary()
	}
}

func BenchmarkCoordinator_UnmarshalBinary(b *testing.B) {
	weights := make([]int, benchmarkShardCount)
	for i := range weights {
		weights[i] = int(rand.Int31n(100)) + 1
	}
	sm := NewShardsCoordinator()
	// nolint - won't check error returns
	sm.Init(benchmarkShardCount, weights)
	var nodes []string
	for i := 0; i < benchmarkNodeCount; i++ {
		nodes = append(nodes, fmt.Sprintf("Node%04d", i))
	}
	sm.UpdateNodes(nodes...)
	buf, _ := sm.MarshalBinary()

	newManager := NewShardsCoordinator()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// nolint - this is a benchmark
		newManager.UnmarshalBinary(buf)
	}
}

func verifyDistribution(t *testing.T, coordinator Coordinator) {
	assert := require.New(t)

	nodes := make(map[string]int)
	shards := coordinator.Shards()

	for i := range shards {
		w := nodes[shards[i].NodeID()]
		nodes[shards[i].NodeID()] = w + shards[i].Weight()
	}

	totalWeight := 0
	for _, v := range nodes {
		totalWeight += v
	}

	assert.Equal(totalWeight, coordinator.TotalWeight(), "Coordinator reports incorrect total weight")

	weightPerNode := float64(totalWeight) / float64(len(nodes))

	for k, v := range nodes {
		t.Logf("  node: %s w: %d", k, v)
		assert.InDelta(v, weightPerNode, 0.1*weightPerNode, "Distribution is incorrect")
	}
}

// verifyShards ensures all shards are distributed to nodes
func verifyShards(t *testing.T, coordinator Coordinator, maxShards int) {
	assert := require.New(t)
	check := make(map[int]int)
	shards := coordinator.Shards()
	assert.Equal(maxShards, len(shards), "Incorrect length of shards")
	expectedNodes := make(map[string]bool)
	for _, v := range coordinator.NodeList() {
		expectedNodes[v] = true
	}

	for i := range shards {
		check[shards[i].ID()] = 1
		assert.Contains(expectedNodes, shards[i].NodeID())
	}
	for i := 0; i < maxShards; i++ {
		assert.Contains(check, i, "Shard %d does not exist", i)
	}
}

func verifyWorkerIDs(t *testing.T, coordinator Coordinator) {
	assert := require.New(t)
	workers := make([]int, 0)
	for _, v := range coordinator.NodeList() {
		workerID := coordinator.WorkerID(v)
		assert.NotContains(workers, workerID)
		workers = append(workers, workerID)
	}
}

func testShardCoordinator(t *testing.T, coordinator Coordinator, maxShards int, weights []int) {
	assert := require.New(t)
	assert.Error(coordinator.Init(0, nil), "Expected error when maxShards = 0")
	assert.Error(coordinator.Init(len(weights), []int{}), "Expected error when weights != maxShards")

	assert.NoError(coordinator.Init(len(weights), weights), "Regular init should work")
	assert.Error(coordinator.Init(len(weights), weights), "Should not be allowed to init manager twice")

	coordinator.UpdateNodes("A")
	assert.Len(coordinator.NodeList(), 1)
	verifyDistribution(t, coordinator)
	verifyShards(t, coordinator, maxShards)
	verifyWorkerIDs(t, coordinator)

	coordinator.UpdateNodes("B", "A")
	assert.Len(coordinator.NodeList(), 2)
	verifyDistribution(t, coordinator)
	verifyShards(t, coordinator, maxShards)
	verifyWorkerIDs(t, coordinator)

	coordinator.UpdateNodes("C", "B", "A")
	assert.Len(coordinator.NodeList(), 3)
	verifyDistribution(t, coordinator)
	verifyShards(t, coordinator, maxShards)
	verifyWorkerIDs(t, coordinator)

	coordinator.UpdateNodes("B", "A", "C", "D", "E")
	assert.Len(coordinator.NodeList(), 5, "Manager should contain 5 nodes")
	verifyShards(t, coordinator, maxShards)
	verifyWorkerIDs(t, coordinator)

	coordinator.UpdateNodes("B", "C", "D")
	assert.Len(coordinator.NodeList(), 3, "Manager should contain 3 nodes")
	verifyShards(t, coordinator, maxShards)
	verifyWorkerIDs(t, coordinator)

	for i := 0; i < maxShards; i++ {
		assert.NotEqual("", coordinator.MapToNode(i).NodeID(), "Shard %d is not mapped to a node", i)
	}

	coordinator.UpdateNodes("A", "C")
	verifyDistribution(t, coordinator)
	verifyShards(t, coordinator, maxShards)
	verifyWorkerIDs(t, coordinator)

	coordinator.UpdateNodes("C")
	verifyDistribution(t, coordinator)
	verifyShards(t, coordinator, maxShards)
	verifyWorkerIDs(t, coordinator)

	coordinator.UpdateNodes([]string{}...)
	verifyDistribution(t, coordinator)
	verifyWorkerIDs(t, coordinator)

	require.Panics(t, func() { coordinator.MapToNode(-1) }, "Panics on invalid shard ID")
}
