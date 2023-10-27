/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
	"github.com/travisjeffery/go-dynaport"
)

func TestScheduler(t *testing.T) {
	t.Run("With ScheduleOnce", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled())
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		time.Sleep(time.Second)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = sys.ScheduleOnce(ctx, message, actorRef, 100*time.Millisecond)
		require.NoError(t, err)

		time.Sleep(time.Second)
		assert.EqualValues(t, 1, actor.counter.Load())
		assert.EqualValues(t, 1, actor.counter.Load())

		// stop the actor
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With RemoteScheduleOnce", func(t *testing.T) {
		// create the context
		ctx := context.TODO()
		// define the logger to use
		logger := log.New(log.DebugLevel, os.Stdout)
		// generate the remoting port
		nodePorts := dynaport.Get(1)
		remotingPort := nodePorts[0]
		host := "0.0.0.0"

		// create the actor system
		sys, err := NewActorSystem("test",
			WithLogger(logger),
			WithPassivationDisabled(),
			WithRemoting(host, int32(remotingPort)),
		)
		// assert there are no error
		require.NoError(t, err)

		// start the actor system
		err = sys.Start(ctx)
		assert.NoError(t, err)

		time.Sleep(time.Second)

		// create a test actor
		actorName := "test"
		actor := NewTester()
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		require.NoError(t, err)
		assert.NotNil(t, actorRef)

		// get the address of the actor
		addr, err := RemoteLookup(ctx, host, remotingPort, actorName)
		require.NoError(t, err)

		// send a message to the actor after 100 ms
		message := new(testpb.TestSend)
		err = sys.RemoteScheduleOnce(ctx, message, addr, 100*time.Millisecond)
		require.NoError(t, err)

		time.Sleep(time.Second)
		assert.EqualValues(t, 1, actor.counter.Load())
		assert.EqualValues(t, 1, actor.counter.Load())

		// stop the actor
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}
