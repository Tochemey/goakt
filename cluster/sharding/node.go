package sharding

type node struct {
	NodeID       string
	TotalWeights int
	Shards       []Shard
	WorkerID     int
}

// newNode create an instance of node
func newNode(nodeID string) *node {
	return &node{
		NodeID:       nodeID,
		TotalWeights: 0,
		Shards:       make([]Shard, 0),
		WorkerID:     0,
	}
}

// checkTotalWeight checks the total weight of the node.
// this is done whenever a new shard is added/removed to the node
func (n *node) checkTotalWeight() {
	tot := 0
	for _, n := range n.Shards {
		tot += n.Weight()
	}
}

// AddShard adds a shard to the given node
func (n *node) AddShard(shard Shard) {
	shard.SetNodeID(n.NodeID)
	n.TotalWeights += shard.Weight()
	n.Shards = append(n.Shards, shard)
	n.checkTotalWeight()
}

// RemoveShard removes a given shard from the node
func (n *node) RemoveShard(preferredWeight int) Shard {
	for i, v := range n.Shards {
		if v.Weight() <= preferredWeight {
			n.Shards = append(n.Shards[:i], n.Shards[i+1:]...)
			n.TotalWeights -= v.Weight()
			n.checkTotalWeight()
			return v
		}
	}
	if len(n.Shards) == 0 {
		panic("no shards remaining")
	}
	// This will cause a panic if there's no shards left. That's OK.
	ret := n.Shards[0]
	if len(n.Shards) >= 1 {
		n.Shards = n.Shards[1:]
	}
	n.TotalWeights -= ret.Weight()
	n.checkTotalWeight()
	return ret
}
