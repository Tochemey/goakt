/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actors

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v2/address"
	"github.com/tochemey/goakt/v2/internal/lib"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestStash(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withStash(),
		}

		ports := dynaport.Get(1)

		// create the actor path
		actor := &stasher{}
		actorPath := address.New("stasher", "sys", "host", ports[0])
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// wait for the actor to properly start
		lib.Pause(time.Second)

		// send a stash message to the actor
		err = Tell(ctx, pid, new(testpb.TestStash))
		require.NoError(t, err)

		// add some pause here due to async calls
		lib.Pause(time.Second)
		require.EqualValues(t, 1, pid.StashSize())

		// at this stage any message sent to the actor is stashed
		for i := 0; i < 5; i++ {
			assert.NoError(t, Tell(ctx, pid, new(testpb.TestSend)))
		}

		// add some pause here due to async calls
		lib.Pause(time.Second)

		// when we assert the actor received count it will only show 1
		require.EqualValues(t, 1, pid.StashSize())

		// send another stash
		require.NoError(t, Tell(ctx, pid, new(testpb.TestLogin)))
		// add some pause here due to async calls
		lib.Pause(time.Second)
		require.EqualValues(t, 2, pid.StashSize())

		// add some pause here due to async calls
		lib.Pause(time.Second)
		assert.NoError(t, Tell(ctx, pid, new(testpb.TestUnstash)))

		// add some pause here due to async calls
		lib.Pause(time.Second)
		require.EqualValues(t, 1, pid.StashSize())

		// add some pause here due to async calls
		lib.Pause(time.Second)
		assert.NoError(t, Tell(ctx, pid, new(testpb.TestUnstashAll)))

		// add some pause here due to async calls
		lib.Pause(time.Second)

		require.Zero(t, pid.StashSize())

		// stop the actor
		err = Tell(ctx, pid, new(testpb.TestBye))
		require.NoError(t, err)

		lib.Pause(time.Second)
		assert.False(t, pid.IsRunning())
	})
	t.Run("With stash failure", func(t *testing.T) {
		ctx := context.TODO()
		// create an actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		ports := dynaport.Get(1)
		// create the actor path
		actor := &stasher{}
		actorPath := address.New("stasher", "sys", "host", ports[0])
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// wait for the actor to properly start
		lib.Pause(5 * time.Millisecond)

		err = pid.stash(new(ReceiveContext))
		assert.Error(t, err)
		assert.EqualError(t, err, ErrStashBufferNotSet.Error())

		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With unstash when stash not set", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}
		ports := dynaport.Get(1)
		// create the actor path
		actor := &stasher{}
		actorPath := address.New("stasher", "sys", "host", ports[0])
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// wait for the actor to properly start
		lib.Pause(5 * time.Millisecond)

		err = pid.unstash()
		assert.Error(t, err)
		assert.EqualError(t, err, ErrStashBufferNotSet.Error())

		err = pid.unstashAll()
		assert.Error(t, err)
		assert.EqualError(t, err, ErrStashBufferNotSet.Error())

		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("With unstash when there is no stashed message", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withStash(),
		}

		ports := dynaport.Get(1)

		// create the actor path
		actor := &stasher{}
		actorPath := address.New("stasher", "sys", "host", ports[0])
		pid, err := newPID(ctx, actorPath, actor, opts...)
		require.NoError(t, err)
		require.NotNil(t, pid)

		// wait for the actor to properly start
		lib.Pause(time.Second)

		// send a stash message to the actor
		err = Tell(ctx, pid, new(testpb.TestStash))
		require.NoError(t, err)

		// add some pause here due to async calls
		lib.Pause(time.Second)
		require.EqualValues(t, 1, pid.StashSize())

		// at this stage any message sent to the actor is stashed
		for i := 0; i < 5; i++ {
			assert.NoError(t, Tell(ctx, pid, new(testpb.TestSend)))
		}

		// add some pause here due to async calls
		lib.Pause(time.Second)

		// when we assert the actor received count it will only show 1
		require.EqualValues(t, 1, pid.StashSize())

		// send another stash
		require.NoError(t, Tell(ctx, pid, new(testpb.TestLogin)))
		// add some pause here due to async calls
		lib.Pause(time.Second)
		require.EqualValues(t, 2, pid.StashSize())

		// add some pause here due to async calls
		lib.Pause(time.Second)
		assert.NoError(t, Tell(ctx, pid, new(testpb.TestUnstash)))

		// add some pause here due to async calls
		lib.Pause(time.Second)
		require.EqualValues(t, 1, pid.StashSize())

		// add some pause here due to async calls
		lib.Pause(time.Second)
		assert.NoError(t, Tell(ctx, pid, new(testpb.TestUnstashAll)))

		// add some pause here due to async calls
		lib.Pause(time.Second)

		require.Zero(t, pid.StashSize())

		err = pid.unstash()
		assert.Error(t, err)
		assert.EqualError(t, err, "stash buffer may be closed")

		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
}
