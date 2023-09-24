package deadletter

import (
	"testing"

	"github.com/stretchr/testify/require"
	deadletterpb "github.com/tochemey/goakt/pb/deadletter/v1"
	"google.golang.org/protobuf/proto"
)

func TestQueue(t *testing.T) {
	q := newQueue(10)
	require.NotNil(t, q)

	// create a subscriber
	sub := q.Subscribe()

	dl1 := new(deadletterpb.Deadletter)
	dl2 := new(deadletterpb.Deadletter)

	q.Publish(dl1)
	q.Publish(dl2)

	q.Shutdown()

	var items []*deadletterpb.Deadletter
	for entry := range sub {
		items = append(items, entry)
	}

	require.Len(t, items, 2)
	require.True(t, proto.Equal(items[0], dl1))
	require.True(t, proto.Equal(items[0], dl1))
}
