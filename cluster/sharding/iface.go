package sharding

import "encoding"

// Shard represents a partition that a single node is responsible for
type Shard interface {
	// ID is the shard ID. The shard ID is calculated through a shard function
	// which will map an identifier to a particular shard.
	ID() int

	// Weight represents the relative work for the shard. Some shards might
	// require more work than others, depending on the work distribution.
	// Initially this can be set to 1 for all shards but if you have hotspots
	// with higher resource requirements (like more CPU or memory) you can
	// increase the weight of a shard to balance the load across the cluster.
	Weight() int

	// NodeID returns the node responsible for the shard.
	NodeID() string

	// SetNodeID sets the node ID for the shard
	SetNodeID(nodeID string)
}

// ShardsCoordinator is a type that manages shards. The number of shards are immutable, ie
// no new shards will be added for the lifetime. (shards can be added or removed
// between invocations of the leader)
type ShardsCoordinator interface {
	// Init reinitializes the (shard) manager. This can be called one and only
	// once. Performance critical since this is part of the node
	// onboarding process. The weights parameter may be set to nil. In that
	// case the shards gets a weight of 1. If the weights parameter is specfied
	// the length of the weights parameter must match the maxShards parameter.
	// Shard IDs are assigned from 0...maxShards-1
	Init(maxShards int, weights []int) error

	// UpdateNodes syncs the nodes internally in the cluster and reshards if
	// necessary.
	UpdateNodes(nodeID ...string)

	// MapToNode returns the node (ID) responsible for the shards. Performance
	// critical since this will be used in every single call to determine
	// the home location for mutations.
	// TBD: Panic if the shard ID is > maxShards?
	MapToNode(shardID int) Shard

	// Shards returns a copy of all the shards. Not performance critical. This
	// is typically used for diagnostics.
	Shards() []Shard

	// TotalWeight is the total weight of all shards. Not performance critical
	// directly but it will be used when calculating the distribution of shards
	// so the manager should cache this value and update when a shard changes
	// its weight.
	TotalWeight() int

	// ShardCount returns the number of shards
	ShardCount() int

	// NodeList returns a list of nodes in the shard coordination
	NodeList() []string

	// ShardCountForNode returns the number of shards allocated to a particular node.
	ShardCountForNode(nodeID string) int

	// WorkerID returns the worker ID for the node
	WorkerID(nodeID string) int

	// The marshaling methods are used to save and restore the shard coordinator
	// from the Raft log.

	encoding.BinaryMarshaler
	encoding.BinaryUnmarshaler
}
