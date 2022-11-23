package sharding

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNodeUpdate(t *testing.T) {
	assert := require.New(t)
	sm := NewShardsCoordinator()
	assert.NoError(sm.Init(10000, nil))

	sm.UpdateNodes("A")
	assert.Len(sm.NodeList(), 1)
	sm.UpdateNodes("B", "A")
	assert.Len(sm.NodeList(), 2)

	sm.UpdateNodes("C", "B", "A")
	assert.Len(sm.NodeList(), 3)

	sm.UpdateNodes("A", "B", "C", "D")
	assert.Len(sm.NodeList(), 4)

	sm.UpdateNodes("E", "A", "B", "C", "D")
	assert.Len(sm.NodeList(), 5)

	sm.UpdateNodes("C", "B", "A", "E", "D", "F")
	assert.Len(sm.NodeList(), 6)

	sm.UpdateNodes("A", "B", "C")
	assert.Len(sm.NodeList(), 3)

	sm.UpdateNodes("D", "A", "B", "C")
	assert.Len(sm.NodeList(), 4)
}

func TestNode(t *testing.T) {
	nd := newNode("node1")
	nd.AddShard(NewShard(1, 1))
	nd.AddShard(NewShard(2, 2))
	nd.AddShard(NewShard(3, 3))
	nd.AddShard(NewShard(4, 4))

	if nd.TotalWeights != 10 {
		t.Fatal("Expected w=10")
	}
	s1 := nd.RemoveShard(1)
	if s1.Weight() != 1 {
		t.Fatal("expected weight 1")
	}
	if nd.TotalWeights != 9 {
		t.Fatal("Expected w=9")
	}
	s2 := nd.RemoveShard(2)
	if s2.Weight() != 2 {
		t.Fatalf("expected weight 2, got %+v", s2)
	}
	if nd.TotalWeights != 7 {
		t.Fatal("Expected w=7")
	}
	s3 := nd.RemoveShard(3)
	if s3.Weight() != 3 {
		t.Fatal("expected weight 3")
	}
	if nd.TotalWeights != 4 {
		t.Fatal("Expected w=4")
	}
	s4 := nd.RemoveShard(4)
	if s4.Weight() != 4 {
		t.Fatal("expected weight 4")
	}
	if nd.TotalWeights != 0 {
		t.Fatal("Expected w=0")
	}

	defer func() {
		// nolint
		recover()
	}()
	nd.RemoveShard(1)
	t.Fatal("no panic when zero shards left")
}
