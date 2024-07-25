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

package actors

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestRoundRobinRouter(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DefaultLogger
		system, err := NewActorSystem(
			"testSystem",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		time.Sleep(time.Second)

		// create a roundrobin router with one routee
		// this is for the purpose of testing to make sure given routee does receive the
		// message sent
		roundrobin := newRoundRobinRouter(newWorker())

		router, err := system.Spawn(ctx, "worker-pool", roundrobin)
		require.NoError(t, err)
		require.NotNil(t, router)

		time.Sleep(time.Second)

		// send a broadcast message to the router
		message, _ := anypb.New(&testpb.DoLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		time.Sleep(time.Second)

		// this is just for tests purpose
		workerName := fmt.Sprintf("GoAktRoutee-%s-%d", router.Name(), 0)

		workerOneRef, err := system.LocalActor(workerName)
		require.NoError(t, err)
		require.NotNil(t, workerOneRef)

		expected := &testpb.Count{Value: 2}

		reply, err := Ask(ctx, workerOneRef, new(testpb.GetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.True(t, proto.Equal(expected, reply))

		t.Cleanup(func() {
			assert.NoError(t, system.Stop(ctx))
		})
	})
	t.Run("With no available routees router is alive and message in deadletter", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DefaultLogger
		system, err := NewActorSystem(
			"testSystem",
			WithPassivationDisabled(),
			WithLogger(logger),
			WithReplyTimeout(time.Minute))

		require.NoError(t, err)
		require.NotNil(t, system)
		require.NoError(t, system.Start(ctx))

		time.Sleep(time.Second)

		// create a deadletter subscriber
		consumer, err := system.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create a roundRobin router with one routee
		// this is for the purpose of testing to make sure given routee does receive the
		// message sent
		roundRobin := newRoundRobinRouter(newWorker())

		router, err := system.Spawn(ctx, "worker-pool", roundRobin)
		require.NoError(t, err)
		require.NotNil(t, router)

		time.Sleep(time.Second)

		// this is just for tests purpose
		workerName := fmt.Sprintf("GoAktRoutee-%s-%d", router.Name(), 0)
		err = system.Kill(ctx, workerName)
		require.NoError(t, err)

		time.Sleep(time.Second)

		// send a broadcast message to the router
		message, _ := anypb.New(&testpb.DoLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		time.Sleep(time.Second)
		assert.True(t, router.IsRunning())

		var items []*goaktpb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletters
			deadletter, ok := payload.(*goaktpb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Len(t, items, 1)

		t.Cleanup(func() {
			assert.NoError(t, system.Stop(ctx))
		})
	})
}
