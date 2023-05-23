package actors

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddress(t *testing.T) {
	t.Run("testCase1", func(t *testing.T) {
		expected := "goakt://sys@host:1234"
		addr := NewAddress(protocol, "sys", "host", 1234)
		assert.False(t, addr.IsLocal())
		assert.True(t, addr.IsRemote())
		assert.Equal(t, "host:1234", addr.HostPort())
		assert.Equal(t, "host", addr.Host())
		assert.EqualValues(t, 1234, addr.Port())
		assert.Equal(t, expected, addr.String())
	})
	t.Run("testCase2", func(t *testing.T) {
		expected := "goakt://sys@"
		addr := NewAddress(protocol, "sys", "", -1)
		assert.False(t, addr.IsRemote())
		assert.True(t, addr.IsLocal())
		assert.Equal(t, expected, addr.String())
		actual, err := addr.WithHost("localhost")
		require.Error(t, err)
		assert.EqualError(t, err, ErrLocalAddress.Error())
		assert.Nil(t, actual)
	})
	t.Run("testCase3", func(t *testing.T) {
		expected := "goakt://sys@host:1234"
		addr := NewAddress(protocol, "sys", "host", 1234)
		assert.False(t, addr.IsLocal())
		assert.True(t, addr.IsRemote())
		assert.Equal(t, "host:1234", addr.HostPort())
		assert.Equal(t, "host", addr.Host())
		assert.EqualValues(t, 1234, addr.Port())
		assert.Equal(t, expected, addr.String())

		// change the host
		actual, err := addr.WithHost("localhost")
		require.NoError(t, err)
		expected = "goakt://sys@localhost:1234"
		assert.False(t, actual.IsLocal())
		assert.True(t, actual.IsRemote())
		assert.Equal(t, "localhost:1234", actual.HostPort())
		assert.Equal(t, "localhost", actual.Host())
		assert.EqualValues(t, 1234, actual.Port())
		assert.Equal(t, expected, actual.String())

		assert.False(t, cmp.Equal(addr, actual, cmp.AllowUnexported(Address{})))
	})
}
