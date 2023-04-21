package discovery

// Event contract
type Event interface {
	IsEvent()
}

// NodeAdded discovery lifecycle event
type NodeAdded struct {
	// Node specifies the added node
	Node *Node
}

func (e NodeAdded) IsEvent() {}

// NodeRemoved discovery lifecycle event
type NodeRemoved struct {
	// Node specifies the removed node
	Node *Node
}

func (e NodeRemoved) IsEvent() {}

// NodeModified discovery lifecycle event
type NodeModified struct {
	// Node specifies the modified node
	Node *Node
	// Current specifies the existing nde
	Current *Node
}

func (e NodeModified) IsEvent() {}
