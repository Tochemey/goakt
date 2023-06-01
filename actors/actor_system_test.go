package actors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
)

func TestActorSystem(t *testing.T) {
	t.Run("With Defaults", func(t *testing.T) {
		actorSys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NotNil(t, actorSys)
		var iface any = actorSys
		_, ok := iface.(ActorSystem)
		assert.True(t, ok)
		assert.Equal(t, "testSys", actorSys.Name())
		assert.Empty(t, actorSys.Actors())
	})
	t.Run("With Missing Name", func(t *testing.T) {
		sys, err := NewActorSystem("")
		assert.Error(t, err)
		assert.Nil(t, sys)
		assert.EqualError(t, err, ErrNameRequired.Error())
	})
	t.Run("With StartActor an actor when not started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		actor := NewTestActor()
		actorRef := sys.StartActor(ctx, "Test", actor)
		assert.Nil(t, actorRef)
	})
	t.Run("With StartActor an actor when started", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := NewTestActor()
		actorRef := sys.StartActor(ctx, "Test", actor)
		assert.NotNil(t, actorRef)

		assert.NoError(t, sys.Stop(ctx))
	})
	t.Run("With StartActor an actor already exist", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("test", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := NewTestActor()
		ref1 := sys.StartActor(ctx, "Test", actor)
		assert.NotNil(t, ref1)

		ref2 := sys.StartActor(ctx, "Test", actor)
		assert.NotNil(t, ref2)

		// point to the same memory address
		assert.True(t, ref1 == ref2)

		assert.NoError(t, sys.Stop(ctx))
	})
	t.Run("With remoting enabled", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys",
			WithLogger(log.DiscardLogger),
			WithRemoting("localhost", 0),
		)

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := NewTestActor()
		actorRef := sys.StartActor(ctx, "Test", actor)
		assert.NotNil(t, actorRef)

		assert.NoError(t, sys.Stop(ctx))
	})
}
