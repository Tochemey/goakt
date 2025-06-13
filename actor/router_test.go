/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package actor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/util"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func TestRouter(t *testing.T) {
	t.Run("With Fan-Out strategy", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		util.Pause(time.Second)

		routeesKind := new(mockRouterActor)
		poolSize := 2
		routingStrategy := FanOutRouting
		router, err := system.SpawnRouter(ctx, poolSize, routeesKind, WithRoutingStrategy(routingStrategy))
		require.NoError(t, err)
		require.NotNil(t, router)

		util.Pause(time.Second)

		// send a broadcast message to the router
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		util.Pause(time.Second)

		// this is just for tests purpose
		workerOneName := fmt.Sprintf("GoAktRoutee-%s-%d", router.Name(), 0)
		workerTwoName := fmt.Sprintf("GoAktRoutee-%s-%d", router.Name(), 1)

		workerOneRef, ok := system.findRoutee(workerOneName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)

		workerTwoRef, ok := system.findRoutee(workerTwoName)
		require.True(t, ok)
		require.NotNil(t, workerTwoRef)

		expected := &testpb.TestCount{Value: 2}

		reply, err := Ask(ctx, workerOneRef, new(testpb.TestGetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.True(t, proto.Equal(expected, reply))

		reply, err = Ask(ctx, workerTwoRef, new(testpb.TestGetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.True(t, proto.Equal(expected, reply))

		t.Cleanup(func() {
			assert.NoError(t, system.Stop(ctx))
		})
	})
	t.Run("With Fan-Out strategy With no available routees router shuts down", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		util.Pause(time.Second)

		routeesKind := new(mockRouterActor)
		poolSize := 2
		routingStrategy := FanOutRouting
		router, err := system.SpawnRouter(ctx, poolSize, routeesKind, WithRoutingStrategy(routingStrategy))
		require.NoError(t, err)
		require.NotNil(t, router)

		util.Pause(time.Second)

		// this is just for tests purpose
		workerOneName := fmt.Sprintf("GoAktRoutee-%s-%d", router.Name(), 0)
		workerTwoName := fmt.Sprintf("GoAktRoutee-%s-%d", router.Name(), 1)

		workerOneRef, ok := system.findRoutee(workerOneName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)

		workerTwoRef, ok := system.findRoutee(workerTwoName)
		require.True(t, ok)
		require.NotNil(t, workerTwoRef)

		require.NoError(t, workerOneRef.Shutdown(ctx))
		require.NoError(t, workerTwoRef.Shutdown(ctx))

		// send a broadcast message to the router
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		util.Pause(time.Second)

		ref, err := system.LocalActor("routerQA-pool")
		require.Error(t, err)
		require.Nil(t, ref)
		assert.ErrorIs(t, err, ErrActorNotFound)

		t.Cleanup(func() {
			assert.NoError(t, system.Stop(ctx))
		})
	})
	t.Run("With Round Robin strategy", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		util.Pause(time.Second)

		routeesKind := new(mockRouterActor)
		poolSize := 1
		routingStrategy := RoundRobinRouting
		router, err := system.SpawnRouter(ctx, poolSize, routeesKind, WithRoutingStrategy(routingStrategy))
		require.NoError(t, err)
		require.NotNil(t, router)

		util.Pause(time.Second)

		// send a broadcast message to the router
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		util.Pause(time.Second)

		// this is just for tests purpose
		workerName := fmt.Sprintf("GoAktRoutee-%s-%d", router.Name(), 0)

		workerOneRef, ok := system.findRoutee(workerName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)

		expected := &testpb.TestCount{Value: 2}

		reply, err := Ask(ctx, workerOneRef, new(testpb.TestGetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.True(t, proto.Equal(expected, reply))

		t.Cleanup(func() {
			assert.NoError(t, system.Stop(ctx))
		})
	})
	t.Run("With Round Robin strategy With no available routees router is alive and message in deadletter", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		util.Pause(time.Second)

		// create a deadletter subscriber
		consumer, err := system.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		routeesKind := new(mockRouterActor)
		poolSize := 1
		routingStrategy := RoundRobinRouting
		router, err := system.SpawnRouter(ctx, poolSize, routeesKind, WithRoutingStrategy(routingStrategy))

		require.NoError(t, err)
		require.NotNil(t, router)

		util.Pause(time.Second)

		// this is just for tests purpose
		workerName := fmt.Sprintf("GoAktRoutee-%s-%d", router.Name(), 0)
		workerOneRef, ok := system.findRoutee(workerName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)

		err = workerOneRef.Shutdown(ctx)
		require.NoError(t, err)

		util.Pause(time.Second)

		// send a broadcast message to the router
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		util.Pause(time.Second)
		require.False(t, router.IsRunning())

		var items []*goaktpb.Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*goaktpb.Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.NotEmpty(t, items)

		t.Cleanup(func() {
			assert.NoError(t, system.Stop(ctx))
		})
	})
	t.Run("With Random strategy", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		util.Pause(time.Second)

		routeesKind := new(mockRouterActor)
		poolSize := 1
		routingStrategy := RandomRouting
		router, err := system.SpawnRouter(ctx, poolSize, routeesKind, WithRoutingStrategy(routingStrategy))
		require.NoError(t, err)
		require.NotNil(t, router)

		util.Pause(time.Second)

		// send a broadcast message to the router
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		util.Pause(time.Second)

		// this is just for tests purpose
		workerName := fmt.Sprintf("GoAktRoutee-%s-%d", router.Name(), 0)

		workerOneRef, ok := system.findRoutee(workerName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)

		expected := &testpb.TestCount{Value: 2}

		reply, err := Ask(ctx, workerOneRef, new(testpb.TestGetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.True(t, proto.Equal(expected, reply))

		t.Cleanup(func() {
			assert.NoError(t, system.Stop(ctx))
		})
	})
}
