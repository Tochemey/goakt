package actors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
)

func TestNewActorSystem(t *testing.T) {
	t.Run("With Defaults", func(t *testing.T) {
		cfg, err := NewConfig("testSys", "localhost:0", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		assert.NotNil(t, cfg)

		actorSys, err := NewActorSystem(cfg)
		require.NoError(t, err)
		require.NotNil(t, actorSys)
		var iface any = actorSys
		_, ok := iface.(ActorSystem)
		assert.True(t, ok)
		assert.Equal(t, "testSys", actorSys.Name())
		assert.Empty(t, actorSys.Actors())
		assert.Equal(t, "localhost:0", actorSys.NodeAddr())
	})
	t.Run("With Missing Config", func(t *testing.T) {
		sys, err := NewActorSystem(nil)
		assert.Error(t, err)
		assert.Nil(t, sys)
		assert.EqualError(t, err, ErrMissingConfig.Error())
	})
	t.Run("With StartActor an actor when not started", func(t *testing.T) {
		ctx := context.TODO()
		cfg, _ := NewConfig("testSys", "localhost:0", WithLogger(log.DiscardLogger))
		sys, _ := NewActorSystem(cfg)
		actor := NewTestActor()
		actorRef := sys.StartActor(ctx, "Test", "test-1", actor)
		assert.Nil(t, actorRef)
	})
	t.Run("With StartActor an actor when started", func(t *testing.T) {
		ctx := context.TODO()
		cfg, _ := NewConfig("testSys", "localhost:0", WithLogger(log.DiscardLogger))
		sys, _ := NewActorSystem(cfg)

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := NewTestActor()
		actorRef := sys.StartActor(ctx, "Test", "test-1", actor)
		assert.NotNil(t, actorRef)

		assert.NoError(t, sys.Stop(ctx))
	})
	t.Run("With StartActor an actor already exist", func(t *testing.T) {
		ctx := context.TODO()
		cfg, _ := NewConfig("testSys", "localhost:0", WithLogger(log.DiscardLogger))
		sys, _ := NewActorSystem(cfg)

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		actor := NewTestActor()
		ref1 := sys.StartActor(ctx, "Test", "test-1", actor)
		assert.NotNil(t, ref1)

		ref2 := sys.StartActor(ctx, "Test", "test-1", actor)
		assert.NotNil(t, ref2)

		// point to the same memory address
		assert.True(t, ref1 == ref2)

		assert.NoError(t, sys.Stop(ctx))
	})
}
