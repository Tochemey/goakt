package sharding

import (
	"errors"
	"fmt"
	"sync"

	shardpb "github.com/tochemey/goakt/gen/sharding/v1"
	"google.golang.org/protobuf/proto"
)

// coordinator is the shards coordinator
type coordinator struct {
	shards          []Shard
	mu              sync.RWMutex
	totalWeight     int
	nodes           map[string]*node
	maxWorkerID     int
	workerIDCounter int
}

// NewShardsCoordinator creates an instance of the shard coordinator
func NewShardsCoordinator() ShardsCoordinator {
	return &coordinator{
		shards:          make([]Shard, 0),
		mu:              sync.RWMutex{},
		totalWeight:     0,
		nodes:           make(map[string]*node, 0),
		maxWorkerID:     16383, // TODO: User-supplied parameter later on
		workerIDCounter: 1,
	}
}

// Init reinitializes the (shard) manager. This can be called one and only
// once. Performance critical since this is part of the node
// onboarding process. The weights parameter may be set to nil. In that
// case the shards gets a weight of 1. If the weights parameter is specfied
// the length of the weights parameter must match the maxShards parameter.
// Shard IDs are assigned from 0...maxShards-1
func (c *coordinator) Init(maxShards int, weights []int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if maxShards < 1 {
		return errors.New("maxShards must be > 0")
	}
	if len(c.shards) != 0 {
		return errors.New("shards already set")
	}

	if weights != nil && len(weights) != maxShards {
		return errors.New("maxShards and len(weights) must be the same")
	}

	c.shards = make([]Shard, maxShards)
	for i := range c.shards {
		weight := 1
		if weights != nil {
			weight = weights[i]
		}
		c.totalWeight += weight
		c.shards[i] = NewShard(i, weight)
		if weight == 0 {
			return fmt.Errorf("can't use weight = 0 for shard %d", i)
		}
	}
	return nil
}

// UpdateNodes syncs the nodes internally in the cluster and reshards if
// necessary.
func (c *coordinator) UpdateNodes(nodeID ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var newNodes []string
	var removedNodes []string

	// Find the new nodes
	for _, v := range nodeID {
		_, exists := c.nodes[v]
		if !exists {
			newNodes = append(newNodes, v)
		}
	}
	for k := range c.nodes {
		found := false
		for _, n := range nodeID {
			if n == k {
				// node exists, ignore it
				found = true
				break
			}
		}
		if !found {
			removedNodes = append(removedNodes, k)
		}
	}

	for _, v := range newNodes {
		c.addNode(v)
	}
	for _, v := range removedNodes {
		c.removeNode(v)
	}
}

func (c *coordinator) MapToNode(shardID int) Shard {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if shardID > len(c.shards) || shardID < 0 {
		// This might be too extreme but useful for debugging.
		// another alternative is to return a catch-all node allowing
		// the proxying to fix it but if the shard ID is invalid it is
		// probably an error with the shard function itself and warrants
		// a panic() from the library.
		panic(fmt.Sprintf("shard ID is outside range [0-%d]: %d", len(c.shards), shardID))
	}
	return c.shards[shardID]
}

// Shards returns a copy of all the shards. Not performance critical. This
// is typically used for diagnostics.
func (c *coordinator) Shards() []Shard {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ret := make([]Shard, len(c.shards))
	copy(ret, c.shards)
	return ret
}

// TotalWeight is the total weight of all shards. Not performance critical
// directly but it will be used when calculating the distribution of shards
// so the coordinator should cache this value and update when a shard changes
// its weight.
func (c *coordinator) TotalWeight() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.totalWeight
}

// ShardCount returns the number of shards
func (c *coordinator) ShardCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.shards)
}

// NodeList returns a list of nodes in the shard coordinator
func (c *coordinator) NodeList() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	res := make([]string, 0, len(c.nodes))
	for node := range c.nodes {
		res = append(res, node)
	}
	return res
}

// ShardCountForNode returns the number of shards allocated to a particular node.
func (c *coordinator) ShardCountForNode(nodeid string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	node, ok := c.nodes[nodeid]
	if !ok {
		return 0
	}
	return len(node.Shards)
}

// WorkerID returns the worker ID for the node
func (c *coordinator) WorkerID(nodeID string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	node, ok := c.nodes[nodeID]
	if !ok {
		return -1
	}
	return node.WorkerID
}

// MarshalBinary encodes the receiver into a binary form and returns the result.
func (c *coordinator) MarshalBinary() (data []byte, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.nodes) == 0 {
		return nil, errors.New("map does not contain any nodes")
	}

	msg := &shardpb.Replication{}
	nodeMap := make(map[string]int32)
	n := int32(0)
	for _, v := range c.nodes {
		msg.Nodes = append(msg.Nodes, &shardpb.Node{
			NodeId:   n,
			Name:     v.NodeID,
			WorkerId: int32(v.WorkerID),
		})
		nodeMap[v.NodeID] = n
		n++
	}

	for _, shard := range c.shards {
		msg.Shards = append(msg.Shards, &shardpb.Shard{
			Id:     int32(shard.ID()),
			Weight: int32(shard.Weight()),
			NodeId: nodeMap[shard.NodeID()],
		})
	}
	buf, err := proto.Marshal(msg)
	if err != nil {
		return nil, err
	}

	return buf, nil
}

// UnmarshalBinary must be able to decode the form generated by MarshalBinary.
// UnmarshalBinary must copy the data if it wishes to retain the data
// after returning.
func (c *coordinator) UnmarshalBinary(data []byte) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	msg := &shardpb.Replication{}
	if err := proto.Unmarshal(data, msg); err != nil {
		return err
	}
	maxWorkerID := -1
	c.totalWeight = 0
	c.nodes = make(map[string]*node)
	c.shards = make([]Shard, 0)

	nodeMap := make(map[int32]string)
	for _, v := range msg.Nodes {
		nodeMap[v.GetNodeId()] = v.GetName()
		newNode := newNode(v.GetName())
		newNode.WorkerID = int(v.WorkerId)
		if newNode.WorkerID > maxWorkerID {
			maxWorkerID = newNode.WorkerID
		}
		c.nodes[v.GetName()] = newNode
	}
	for _, v := range msg.Shards {
		newShard := NewShard(int(v.Id), int(v.Weight))
		c.nodes[nodeMap[v.GetNodeId()]].AddShard(newShard)
		c.totalWeight += int(v.Weight)
		c.shards = append(c.shards, newShard)
	}
	c.workerIDCounter = maxWorkerID
	return nil
}

func (c *coordinator) nextWorkerID() int {
	c.workerIDCounter++
	return c.workerIDCounter % c.maxWorkerID
}

func (c *coordinator) addNode(nodeID string) {
	newNode := newNode(nodeID)
	newNode.WorkerID = c.nextWorkerID()
	// Invariant: First node
	if len(c.nodes) == 0 {
		for i := range c.shards {
			newNode.AddShard(c.shards[i])
		}
		c.nodes[nodeID] = newNode
		return
	}

	//Invariant: Node # 2 or later
	targetCount := c.totalWeight / (len(c.nodes) + 1)

	for k, v := range c.nodes {
		for v.TotalWeights > targetCount && v.TotalWeights > 0 {
			shardToMove := v.RemoveShard(targetCount - v.TotalWeights)
			newNode.AddShard(shardToMove)
		}
		c.nodes[k] = v
	}
	c.nodes[nodeID] = newNode
}

func (c *coordinator) removeNode(nodeID string) {
	nodeToRemove, exists := c.nodes[nodeID]
	if !exists {
		// TODO logger
		panic(fmt.Sprintf("Unknown node ID: %s", nodeID))
	}
	delete(c.nodes, nodeID)
	// Invariant: This is the last node in the cluster. No point in
	// generating transfers
	if len(c.nodes) == 0 {
		for i := range c.shards {
			c.shards[i].SetNodeID("")
		}
		return
	}

	targetCount := c.totalWeight / len(c.nodes)
	for k, v := range c.nodes {
		//		fmt.Printf("Removing node %s: Node %s w=%d target=%d\n", nodeID, k, v.TotalWeights, targetCount)
		for v.TotalWeights <= targetCount && nodeToRemove.TotalWeights > 0 {
			shardToMove := nodeToRemove.RemoveShard(targetCount - v.TotalWeights)
			v.AddShard(shardToMove)
		}
		c.nodes[k] = v
	}
}
