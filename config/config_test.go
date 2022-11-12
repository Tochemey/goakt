package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
)

func TestConfig(t *testing.T) {
	t.Run("WithValidConfig", func(t *testing.T) {
		cfg, err := New("testSys", "localhost:0")
		require.NoError(t, err)
		assert.NotNil(t, cfg)
		assert.EqualValues(t, 5*time.Second, cfg.ReplyTimeout())
		assert.EqualValues(t, 5*time.Second, cfg.ExpireActorAfter())
		assert.EqualValues(t, 5, cfg.ActorInitMaxRetries())
		assert.Equal(t, log.DefaultLogger, cfg.Logger())
		assert.Equal(t, "testSys", cfg.Name())
		assert.Equal(t, "localhost:0", cfg.NodeHostAndPort())
	})
	t.Run("WithEmptyName", func(t *testing.T) {
		cfg, err := New("", "localhost:0")
		require.Error(t, err)
		assert.EqualError(t, err, ErrNameRequired.Error())
		assert.Nil(t, cfg)
	})
	t.Run("WithInvalidNodeAddr", func(t *testing.T) {
		cfg, err := New("Sys", "localhost")
		require.Error(t, err)
		assert.Nil(t, cfg)
	})
}
