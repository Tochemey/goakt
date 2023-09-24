package deadletter

import (
	"testing"

	"github.com/stretchr/testify/require"
	deadletterpb "github.com/tochemey/goakt/pb/deadletter/v1"
	"google.golang.org/protobuf/proto"
)

func TestStream(t *testing.T) {
	t.Run("With Publication/Subscription", func(t *testing.T) {
		// create an instance of stream
		stream := NewStream()
		require.NotNil(t, stream)

		// create a subscriber
		sub := stream.Subscribe()

		// create two dead letters to publish
		dl1 := new(deadletterpb.Deadletter)
		dl2 := new(deadletterpb.Deadletter)

		// publish the dead letters
		stream.Publish(dl1)
		stream.Publish(dl2)

		// shutdown the stream
		stream.Close()

		var items []*deadletterpb.Deadletter
		for entry := range sub {
			items = append(items, entry)
		}

		require.Len(t, items, 2)
		require.True(t, proto.Equal(items[0], dl1))
		require.True(t, proto.Equal(items[0], dl1))
	})
	t.Run("With Publication/Unsubscription", func(t *testing.T) {
		// create an instance of stream
		stream := NewStream()
		require.NotNil(t, stream)
		defer stream.Close()

		// create a subscriber
		sub := stream.Subscribe()
		// create two dead letters to publish
		dl1 := new(deadletterpb.Deadletter)
		dl2 := new(deadletterpb.Deadletter)

		// publish the dead letters
		stream.Publish(dl1)
		stream.Publish(dl2)

		stream.Unsubscribe(sub)

		var items []*deadletterpb.Deadletter
		for entry := range sub {
			items = append(items, entry)
		}

		require.Len(t, items, 2)
		require.True(t, proto.Equal(items[0], dl1))
		require.True(t, proto.Equal(items[0], dl1))
	})
}
