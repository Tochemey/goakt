package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTypesLoader(t *testing.T) {
	t.Run("With new instance", func(t *testing.T) {
		tl := NewTypesLoader()
		var p any = tl
		_, ok := p.(TypesLoader)
		assert.True(t, ok)
	})

	t.Run("With registration with name", func(t *testing.T) {
		tl := NewTypesLoader()
		// create an instance of an object
		actor := NewTestActor()
		// register that actor
		tl.Register("TestActor", actor)
		// assert the type of TestActor
		_, ok := tl.TypeByName("TestActor")
		assert.True(t, ok)
		_, ok = tl.Type(actor)
		assert.True(t, ok)
	})

	t.Run("With registration without name", func(t *testing.T) {
		tl := NewTypesLoader()
		// create an instance of an object
		actor := NewTestActor()
		// register that actor
		tl.Register("", actor)
		// assert the type of TestActor
		_, ok := tl.TypeByName("TestActor")
		assert.False(t, ok)
		_, ok = tl.Type(actor)
		assert.True(t, ok)
	})
}
