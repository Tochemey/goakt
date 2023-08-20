package cluster

import (
	"github.com/buraksezer/olric/hasher"
	"github.com/tochemey/goakt/hash"
)

type hasherWrapper struct {
	hasher hash.Hasher
}

// enforce compilation error
var _ hasher.Hasher = &hasherWrapper{}

// Sum64 implementation
func (x hasherWrapper) Sum64(key []byte) uint64 {
	return x.hasher.HashCode(key)
}
