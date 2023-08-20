package hash

import (
	"github.com/cespare/xxhash/v2"
)

// Hasher defines the hashcode generator interface.
type Hasher interface {
	// HashCode is responsible for generating unsigned, 64-bit hash of provided byte slice
	HashCode(key []byte) uint64
}

type xhasher struct{}

var _ Hasher = xhasher{}

// HashCode implementation
func (x xhasher) HashCode(key []byte) uint64 {
	return xxhash.Sum64(key)
}

// DefaultHasher returns the default hasher
func DefaultHasher() Hasher {
	return &xhasher{}
}
