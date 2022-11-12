package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	t.Run("WithValidConfig", func(t *testing.T) {
		cfg, err := New("testSys", "localhost:0")
		require.NoError(t, err)
		assert.NotNil(t, cfg)
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
