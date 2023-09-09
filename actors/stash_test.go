package actors

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
)

func TestStash(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withStash(10),
		}

		// create the actor path
		actor := &Stasher{}
		actorPath := NewPath("Stasher", NewAddress("sys", "host", 1))
		pid := newPID(ctx, actorPath, actor, opts...)
		require.NotNil(t, pid)

		// wait for the actor to properly start
		time.Sleep(5 * time.Millisecond)

		// send a stash message to the actor
		err := Tell(ctx, pid, new(testpb.TestStash))
		require.NoError(t, err)

		// add some pause here due to async calls
		time.Sleep(5 * time.Millisecond)
		require.EqualValues(t, 1, pid.StashSize(ctx))

		// at this stage any message sent to the actor is stashed
		for i := 0; i < 5; i++ {
			assert.NoError(t, Tell(ctx, pid, new(testpb.TestSend)))
		}

		// add some pause here due to async calls
		time.Sleep(5 * time.Millisecond)

		// when we assert the actor received count it will only show 1
		assert.EqualValues(t, 6, pid.ReceivedCount(ctx))
		require.EqualValues(t, 1, pid.StashSize(ctx))

		// send another stash
		require.NoError(t, Tell(ctx, pid, new(testpb.TestLogin)))
		// add some pause here due to async calls
		time.Sleep(5 * time.Millisecond)
		require.EqualValues(t, 2, pid.StashSize(ctx))

		// add some pause here due to async calls
		time.Sleep(5 * time.Millisecond)
		assert.NoError(t, Tell(ctx, pid, new(testpb.TestUnstash)))

		// add some pause here due to async calls
		time.Sleep(5 * time.Millisecond)
		require.EqualValues(t, 1, pid.StashSize(ctx))

		// add some pause here due to async calls
		time.Sleep(5 * time.Millisecond)
		assert.NoError(t, Tell(ctx, pid, new(testpb.TestUnstashAll)))

		// add some pause here due to async calls
		time.Sleep(5 * time.Millisecond)

		require.Zero(t, pid.StashSize(ctx))

		// stop the actor
		err = Tell(ctx, pid, new(testpb.TestBye))
		require.NoError(t, err)

		time.Sleep(time.Second)
		assert.False(t, pid.IsRunning())
	})
	t.Run("With stash failure", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		actor := &Stasher{}
		actorPath := NewPath("Stasher", NewAddress("sys", "host", 1))
		pid := newPID(ctx, actorPath, actor, opts...)
		require.NotNil(t, pid)

		// wait for the actor to properly start
		time.Sleep(5 * time.Millisecond)

		err := pid.stash(new(receiveContext))
		assert.Error(t, err)
		assert.EqualError(t, err, ErrStashBufferNotSet.Error())
	})
	t.Run("With unstash failure", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		actor := &Stasher{}
		actorPath := NewPath("Stasher", NewAddress("sys", "host", 1))
		pid := newPID(ctx, actorPath, actor, opts...)
		require.NotNil(t, pid)

		// wait for the actor to properly start
		time.Sleep(5 * time.Millisecond)

		err := pid.unstash()
		assert.Error(t, err)
		assert.EqualError(t, err, ErrStashBufferNotSet.Error())

		err = pid.unstashAll()
		assert.Error(t, err)
		assert.EqualError(t, err, ErrStashBufferNotSet.Error())
	})
}
