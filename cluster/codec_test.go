package cluster

import (
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	internalpb "github.com/tochemey/goakt/internal/v1"
	addresspb "github.com/tochemey/goakt/pb/address/v1"
	"google.golang.org/protobuf/proto"
)

func TestCodec(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		// create an instance of wire actor
		actor := &internalpb.WireActor{
			ActorName: "account-1",
			ActorAddress: &addresspb.Address{
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
	})
	t.Run("With invalid encoded actor", func(t *testing.T) {
		encoded := base64.StdEncoding.EncodeToString([]byte("invalid proto message"))
		decoded, err := decode(encoded)
		require.Error(t, err)
		assert.Nil(t, decoded)
	})
}
