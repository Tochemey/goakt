/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package router

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestBroadcast(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DefaultLogger
		system, err := actors.NewActorSystem(
			"testSystem",
			actors.WithPassivationDisabled(),
			actors.WithLogger(logger),
			actors.WithReplyTimeout(time.Minute))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		time.Sleep(time.Second)

		// create a broadcast router with two routeeRefs
		broadcaster := NewBroadcaster(newWorker(), newWorker())

		router, err := system.Spawn(ctx, "worker-pool", broadcaster)
		require.NoError(t, err)
		require.NotNil(t, router)

		time.Sleep(time.Second)

		// send a broadcast message to the router
		message, _ := anypb.New(&testpb.DoLog{Text: "msg"})
		err = actors.Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		time.Sleep(time.Second)

		// this is just for tests purpose
		workerOneName := fmt.Sprintf("routee-%s-%d", router.Name(), 0)
		workerTwoName := fmt.Sprintf("routee-%s-%d", router.Name(), 1)

		workerOneRef, err := system.LocalActor(workerOneName)
		require.NoError(t, err)
		require.NotNil(t, workerOneRef)

		workerTwoRef, err := system.LocalActor(workerTwoName)
		require.NoError(t, err)
		require.NotNil(t, workerTwoRef)

		expected := &testpb.Count{Value: 2}

		reply, err := actors.Ask(ctx, workerOneRef, new(testpb.GetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.True(t, proto.Equal(expected, reply))

		reply, err = actors.Ask(ctx, workerTwoRef, new(testpb.GetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.True(t, proto.Equal(expected, reply))

		t.Cleanup(func() {
			assert.NoError(t, system.Stop(ctx))
		})
	})
	t.Run("With no available routees router shuts down", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DefaultLogger
		system, err := actors.NewActorSystem(
			"testSystem",
			actors.WithPassivationDisabled(),
			actors.WithLogger(logger),
			actors.WithReplyTimeout(time.Minute))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		time.Sleep(time.Second)

		// create a broadcast router with two routeeRefs
		broadcaster := NewBroadcaster(newWorker(), newWorker())

		router, err := system.Spawn(ctx, "worker-pool", broadcaster)
		require.NoError(t, err)
		require.NotNil(t, router)

		time.Sleep(time.Second)

		// this is just for tests purpose
		workerOneName := fmt.Sprintf("routee-%s-%d", router.Name(), 0)
		workerTwoName := fmt.Sprintf("routee-%s-%d", router.Name(), 1)

		require.NoError(t, system.Kill(ctx, workerOneName))
		require.NoError(t, system.Kill(ctx, workerTwoName))

		// send a broadcast message to the router
		message, _ := anypb.New(&testpb.DoLog{Text: "msg"})
		err = actors.Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		time.Sleep(time.Second)

		ref, err := system.LocalActor("worker-pool")
		require.Error(t, err)
		require.Nil(t, ref)
		assert.EqualError(t, err, actors.ErrActorNotFound("worker-pool").Error())

		t.Cleanup(func() {
			assert.NoError(t, system.Stop(ctx))
		})
	})
}
