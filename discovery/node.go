package discovery

// Node represents a discovered node
type Node struct {
	// Name specifies the discovered node's Name
	Name string
	// Host specifies the discovered node's Host
	Host string
	// Specifies the start time
	StartTime int64
	// Ports specifies the list of Ports
	Ports map[string]int32
	// IsRunning specifies whether the node is up and running
	IsRunning bool
}
