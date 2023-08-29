package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	pb "github.com/tochemey/goakt/pb/v1"
)

func TestReflection(t *testing.T) {
	t.Run("With ActorFrom", func(t *testing.T) {
		t.Run("With happy path", func(t *testing.T) {
			tl := NewTypesLoader()
			// create an instance of an actor
			actor := NewTester()
			// register the actor into the types registry
			tl.Register("Tester", actor)

			// create an instance of reflection
			reflection := NewReflection(tl)

			// create an instance of test actor from the string Tester
			actual, err := reflection.ActorFrom("Tester")
			assert.NoError(t, err)
			assert.NotNil(t, actual)
			assert.IsType(t, new(Tester), actual)
		})
		t.Run("With actor not found", func(t *testing.T) {
			tl := NewTypesLoader()
			// create an instance of reflection
			reflection := NewReflection(tl)
			actual, err := reflection.ActorFrom("fakeActor")
			assert.Error(t, err)
			assert.Nil(t, actual)
		})
		t.Run("With actor interface not implemented", func(t *testing.T) {
			tl := NewTypesLoader()
			nonActor := new(pb.Address)
			// register the actor into the types registry
			tl.Register("fakeActor", nonActor)
			// create an instance of reflection
			reflection := NewReflection(tl)
			actual, err := reflection.ActorFrom("fakeActor")
			assert.Error(t, err)
			assert.Nil(t, actual)
		})
	})

	t.Run("With ActorOf", func(t *testing.T) {
		t.Run("With happy path", func(t *testing.T) {
			tl := NewTypesLoader()
			// create an instance of an actor
			actor := NewTester()
			// register the actor into the types registry
			tl.Register("", actor)

			// create an instance of reflection
			reflection := NewReflection(tl)

			rtype, ok := tl.Type(actor)
			assert.True(t, ok)
			assert.NotNil(t, rtype)

			// create an instance of test actor from the string Tester
			actual, err := reflection.ActorOf(rtype)

			// perform some assertions
			assert.NoError(t, err)
			assert.NotNil(t, actual)
			assert.IsType(t, new(Tester), actual)
		})
		t.Run("With unhappy path", func(t *testing.T) {
			tl := NewTypesLoader()
			// create an instance of reflection
			reflection := NewReflection(tl)
			nonActor := new(pb.Address)
			// register the actor into the types registry
			tl.Register("", nonActor)

			rtype, ok := tl.Type(nonActor)
			assert.True(t, ok)
			assert.NotNil(t, rtype)
			actual, err := reflection.ActorOf(rtype)
			assert.Error(t, err)
			assert.Nil(t, actual)
		})
	})
}
