package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/config"
)

func TestNewActorSystem(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		config := &config.Config{
			Name:            "testSys",
			NodeHostAndPort: "localhost:0",
		}

		actorSys := NewActorSystem(config)
		require.NotNil(t, actorSys)
		var iface any = actorSys
		_, ok := iface.(ActorSystem)
		assert.True(t, ok)
	})
}
