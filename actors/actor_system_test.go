package actors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewActorSystem(t *testing.T) {
	t.Run("With Defaults", func(t *testing.T) {
		cfg, err := NewConfig("testSys", "localhost:0")
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

	t.Run("With Spawn an actor when not started", func(t *testing.T) {
		ctx := context.TODO()
		cfg, _ := NewConfig("testSys", "localhost:0")
		sys, _ := NewActorSystem(cfg)

		id := &ID{
			Kind:  "Test",
			Value: "test-1",
		}
		actor := NewTestActor()
		actorRef := sys.Spawn(ctx, id, actor)
		assert.Nil(t, actorRef)
	})

	t.Run("With Spawn an actor when started", func(t *testing.T) {
		ctx := context.TODO()
		cfg, _ := NewConfig("testSys", "localhost:0")
		sys, _ := NewActorSystem(cfg)

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		id := &ID{
			Kind:  "Test",
			Value: "test-1",
		}
		actor := NewTestActor()
		actorRef := sys.Spawn(ctx, id, actor)
		assert.NotNil(t, actorRef)

		assert.NoError(t, sys.Stop(ctx))
	})

	t.Run("With Spawn an actor already exist", func(t *testing.T) {
		ctx := context.TODO()
		cfg, _ := NewConfig("testSys", "localhost:0")
		sys, _ := NewActorSystem(cfg)

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		id := &ID{
			Kind:  "Test",
			Value: "test-1",
		}
		actor := NewTestActor()
		ref1 := sys.Spawn(ctx, id, actor)
		assert.NotNil(t, ref1)

		ref2 := sys.Spawn(ctx, id, actor)
		assert.NotNil(t, ref2)

		// point to the same memory address
		assert.True(t, ref1 == ref2)

		assert.NoError(t, sys.Stop(ctx))
	})
}
