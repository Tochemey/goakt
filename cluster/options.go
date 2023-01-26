package cluster

// Options specifies the node options
type Options struct {
	// Unique ID of server
	ID string
	// Local address to bind to
	Address string
	// Members in the cluster
	Members []string
}
