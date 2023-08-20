package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
	mockhasher "github.com/tochemey/goakt/goaktmocks/hash"
)

func TestHasher(t *testing.T) {
	// define the key
	key := []byte("some-key")
	// mock the hasher
	hasher := new(mockhasher.Hasher)
	expected := uint64(20)
	hasher.EXPECT().HashCode(key).Return(20)

	wrapper := &hasherWrapper{hasher: hasher}
	actual := wrapper.Sum64(key)
	assert.EqualValues(t, expected, actual)
}
