package discovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetString(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		meta := Meta{
			"key-1": "value-1",
			"key-2": "value-2",
		}
		key := "key-1"
		actual, err := meta.GetString(key)
		assert.NoError(t, err)
		assert.NotEmpty(t, actual)
		expected := "value-1"
		assert.Equal(t, expected, actual)
	})
	t.Run("with key not found", func(t *testing.T) {
		meta := Meta{
			"key-1": "value-1",
			"key-2": "value-2",
		}
		key := "key-3"
		actual, err := meta.GetString(key)
		assert.Error(t, err)
		assert.EqualError(t, err, "key=key-3 not found")
		assert.Empty(t, actual)
	})
	t.Run("with key value not of a type string", func(t *testing.T) {
		meta := Meta{
			"key-1": "value-1",
			"key-2": 13,
		}
		key := "key-2"
		actual, err := meta.GetString(key)
		assert.Error(t, err)
		assert.EqualError(t, err, "the key value is not a string")
		assert.Empty(t, actual)
	})
}

func TestGetMapString(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		meta := Meta{
			"key-1": map[string]string{
				"key-11": "value-11",
				"key-12": "value-12",
			},
		}
		key := "key-1"
		actual, err := meta.GetMapString(key)
		assert.NoError(t, err)
		assert.NotNil(t, actual)
		expected := map[string]string{
			"key-11": "value-11",
			"key-12": "value-12",
		}
		assert.Equal(t, expected, actual)
	})
	t.Run("with key not found", func(t *testing.T) {
		meta := Meta{
			"key-1": map[string]string{
				"key-11": "value-11",
				"key-12": "value-12",
			},
		}
		key := "key-3"
		actual, err := meta.GetMapString(key)
		assert.Error(t, err)
		assert.EqualError(t, err, "key=key-3 not found")
		assert.Empty(t, actual)
	})
	t.Run("with key value not of a type map[string]string", func(t *testing.T) {
		meta := Meta{
			"key-2": 13,
		}
		key := "key-2"
		actual, err := meta.GetMapString(key)
		assert.Error(t, err)
		assert.EqualError(t, err, "the key value is not a map[string]string")
		assert.Empty(t, actual)
	})
}
