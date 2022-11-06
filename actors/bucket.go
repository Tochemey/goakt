package actors

import "sync"

// Bucket represent a collection of actors on a given node
type Bucket struct {
	// Specifies the bucket unique identifier
	bucketID string
	// Specifies the node where the bucket is located
	nodeRef string
	// map of actors in the bucket
	actorRefs sync.Map
}
