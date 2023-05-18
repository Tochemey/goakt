package cluster

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	messagespb "github.com/tochemey/goakt/messages/v1"
	"google.golang.org/protobuf/proto"
)

func TestCodec(t *testing.T) {
	// create an instance of wire actor
	actor := &goaktpb.WireActor{
		ActorName: "account-1",
		ActorAddress: &messagespb.Address{
			Host: "localhost",
			Port: 2345,
			Name: "account-1",
			Id:   uuid.NewString(),
		},
	}

	// encode the actor
	actual, err := encode(actor)
	require.NoError(t, err)
	assert.NotEmpty(t, actual)

	// decode the actor
	decoded, err := decode(actual)
	require.NoError(t, err)
	assert.True(t, proto.Equal(actor, decoded))
}
