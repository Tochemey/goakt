package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActorAddress(t *testing.T) {
	t.Run("testCase1", func(t *testing.T) {
		address := "goakt://sys@host:1234/abc/def/"
		expected := "goakt://sys@host:1234"
		addr := new(Address)
		actual := addr.Parse(address)
		assert.Equal(t, expected, actual.String())
	})

	t.Run("testCase2", func(t *testing.T) {
		address := "goakt://sys/abc/def"
		addr := new(Address)
		assert.Panics(t, func() {
			addr.Parse(address)
		})
	})

	t.Run("testCase3", func(t *testing.T) {
		address := "goakt://host:1234/abc/def/"
		expected := "goakt://host:1234"
		addr := new(Address)
		actual := addr.Parse(address)
		assert.Equal(t, expected, actual.String())
	})

	t.Run("testCase4", func(t *testing.T) {
		address := "goakt://sys@host:1234"
		expected := "goakt://sys@host:1234"
		addr := new(Address)
		actual := addr.Parse(address)
		assert.Equal(t, expected, actual.String())
	})

	t.Run("testCase5", func(t *testing.T) {
		address := "goakt://sys@host:1234/"
		expected := "goakt://sys@host:1234"
		addr := new(Address)
		actual := addr.Parse(address)
		assert.Equal(t, expected, actual.String())
	})

	t.Run("testCase6", func(t *testing.T) {
		address := "goakt://sys@host/abc/def/"
		addr := new(Address)
		assert.Panics(t, func() {
			addr.Parse(address)
		})
	})
}
