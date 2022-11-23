package sharding

type shard struct {
	id     int
	weight int
	nodeID string
}

// NewShard creates a new shard with a weight
func NewShard(id, weight int) Shard {
	return &shard{
		id:     id,
		weight: weight,
		nodeID: "",
	}
}

func (s *shard) ID() int {
	return s.id
}

func (s *shard) Weight() int {
	return s.weight
}

func (s *shard) NodeID() string {
	return s.nodeID
}

func (s *shard) SetNodeID(nodeID string) {
	s.nodeID = nodeID
}
