package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReflection(t *testing.T) {
	t.Run("With ActorFrom", func(t *testing.T) {
		tl := NewTypesLoader(nil)
		// create an instance of an actor
		actor := NewTestActor()
		// register the actor into the types registry
		tl.Register("TestActor", actor)

		// create an instance of reflection
		reflection := NewReflection(tl)

		// create an instance of test actor from the string TestActor
		actual, err := reflection.ActorFrom("TestActor")
		assert.NoError(t, err)
		assert.NotNil(t, actual)
		assert.IsType(t, new(TestActor), actual)
	})

	t.Run("With ActorOf", func(t *testing.T) {
		tl := NewTypesLoader(nil)
		// create an instance of an actor
		actor := NewTestActor()
		// register the actor into the types registry
		tl.Register("", actor)

		// create an instance of reflection
		reflection := NewReflection(tl)

		rtype, ok := tl.Type(actor)
		assert.True(t, ok)
		assert.NotNil(t, rtype)

		// create an instance of test actor from the string TestActor
		actual, err := reflection.ActorOf(rtype)

		// perform some assertions
		assert.NoError(t, err)
		assert.NotNil(t, actual)
		assert.IsType(t, new(TestActor), actual)
	})
}
