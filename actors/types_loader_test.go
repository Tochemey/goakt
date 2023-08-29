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
		actor := NewTester()
		// register that actor
		tl.Register("Tester", actor)
		// assert the type of Tester
		_, ok := tl.TypeByName("Tester")
		assert.True(t, ok)
		_, ok = tl.Type(actor)
		assert.True(t, ok)
	})

	t.Run("With registration without name", func(t *testing.T) {
		tl := NewTypesLoader()
		// create an instance of an object
		actor := NewTester()
		// register that actor
		tl.Register("", actor)
		// assert the type of Tester
		_, ok := tl.TypeByName("Tester")
		assert.False(t, ok)
		_, ok = tl.Type(actor)
		assert.True(t, ok)
	})
}
