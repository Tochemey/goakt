// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package actor

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/hash"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestRouter(t *testing.T) {
	t.Run("With Fan-Out strategy", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"test",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		routeesKind := new(MockRoutee)
		poolSize := 2
		routingStrategy := FanOutRouting
		routerName := "workerPool"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, routeesKind, WithRoutingStrategy(routingStrategy))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send a broadcast message to the router
		err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "msg"}.Build()))
		require.NoError(t, err)

		pause.For(time.Second)

		// this is just for tests purpose
		workerOneName := routeeName(0, routerName)
		workerTwoName := routeeName(1, routerName)

		workerOneRef, ok := system.findRoutee(workerOneName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)

		workerTwoRef, ok := system.findRoutee(workerTwoName)
		require.True(t, ok)
		require.NotNil(t, workerTwoRef)

		expected := testpb.TestCount_builder{Value: 2}.Build()

		reply, err := Ask(ctx, workerOneRef, new(testpb.TestGetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		actual, ok := reply.(*testpb.TestCount)
		require.True(t, ok)
		assert.True(t, proto.Equal(expected, actual))
		reply, err = Ask(ctx, workerTwoRef, new(testpb.TestGetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		actual, ok = reply.(*testpb.TestCount)
		require.True(t, ok)
		assert.True(t, proto.Equal(expected, actual))

		assert.NoError(t, system.Stop(ctx))
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

		pause.For(time.Second)

		routeesKind := new(MockRoutee)
		poolSize := 2
		routingStrategy := FanOutRouting
		routerName := "workerPool"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, routeesKind, WithRoutingStrategy(routingStrategy))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// this is just for tests purpose
		workerOneName := routeeName(0, routerName)
		workerTwoName := routeeName(1, routerName)

		workerOneRef, ok := system.findRoutee(workerOneName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)

		workerTwoRef, ok := system.findRoutee(workerTwoName)
		require.True(t, ok)
		require.NotNil(t, workerTwoRef)

		require.NoError(t, workerOneRef.Tell(ctx, router, NewPanicSignal(&anypb.Any{}, "test panic signal", time.Now())))

		require.NoError(t, workerTwoRef.Tell(ctx, router, NewPanicSignal(&anypb.Any{}, "test panic signal", time.Now())))

		// send a broadcast message to the router
		err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "msg"}.Build()))
		require.NoError(t, err)

		pause.For(time.Second)

		ref, err := system.ActorOf(ctx, routerName)
		require.Error(t, err)
		require.Nil(t, ref)
		assert.ErrorIs(t, err, errors.ErrActorNotFound)

		assert.NoError(t, system.Stop(ctx))
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

		pause.For(time.Second)

		routeesKind := new(MockRoutee)
		poolSize := 1
		routingStrategy := RoundRobinRouting
		routerName := "workerPool"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, routeesKind, WithRoutingStrategy(routingStrategy))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send a broadcast message to the router
		err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "msg"}.Build()))
		require.NoError(t, err)

		pause.For(time.Second)

		// this is just for tests purpose
		workerName := routeeName(0, routerName)

		workerOneRef, ok := system.findRoutee(workerName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)

		expected := testpb.TestCount_builder{Value: 2}.Build()

		reply, err := Ask(ctx, workerOneRef, new(testpb.TestGetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		actual, ok := reply.(*testpb.TestCount)
		require.True(t, ok)
		assert.True(t, proto.Equal(expected, actual))

		assert.NoError(t, system.Stop(ctx))
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

		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := system.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		routeesKind := new(MockRoutee)
		poolSize := 1
		routingStrategy := RoundRobinRouting
		routerName := "workerPool"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, routeesKind, WithRoutingStrategy(routingStrategy))

		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// this is just for tests purpose
		workerName := routeeName(0, routerName)
		workerOneRef, ok := system.findRoutee(workerName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)

		require.NoError(t, workerOneRef.Tell(ctx, router, NewPanicSignal(&anypb.Any{}, "test panic signal", time.Now())))

		pause.For(time.Second)

		// send a broadcast message to the router
		err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "msg"}.Build()))
		require.NoError(t, err)

		pause.For(time.Second)
		require.False(t, router.IsRunning())

		var items []*Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*Deadletter)
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

		pause.For(time.Second)

		routeesKind := new(MockRoutee)
		poolSize := 1
		routingStrategy := RandomRouting
		routerName := "workerPool"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, routeesKind, WithRoutingStrategy(routingStrategy))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send a broadcast message to the router
		err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "msg"}.Build()))
		require.NoError(t, err)

		pause.For(time.Second)

		// this is just for tests purpose
		workerName := routeeName(0, routerName)

		workerOneRef, ok := system.findRoutee(workerName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)

		expected := testpb.TestCount_builder{Value: 2}.Build()

		reply, err := Ask(ctx, workerOneRef, new(testpb.TestGetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		actual, ok := reply.(*testpb.TestCount)
		require.True(t, ok)
		assert.True(t, proto.Equal(expected, actual))

		assert.NoError(t, system.Stop(ctx))
	})

	t.Run("As TailChopping router", func(t *testing.T) {
		ctx := t.Context()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		summationActor, err := system.Spawn(ctx, "summation", NewMockSummingRoutee())
		require.NoError(t, err)
		require.NotNil(t, summationActor)

		poolSize := 2
		routerName := "tailChopRouter"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, new(MockRoutee), AsTailChopping(2*time.Second, 20*time.Millisecond))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send a broadcast message to the router
		summation := testpb.TestSum_builder{A: 10, B: 20}.Build()
		err = summationActor.Tell(ctx, router, NewBroadcast(summation))
		require.NoError(t, err)

		expected := testpb.TestSumResult_builder{Result: 30}.Build()
		require.Eventually(t, func() bool {
			reply, err := Ask(ctx, summationActor, new(testpb.TestGetSumResult), time.Second)
			if err != nil {
				return false
			}
			sum, ok := reply.(*testpb.TestSumResult)
			if !ok {
				return false
			}
			return proto.Equal(expected, sum)
		}, 5*time.Second, 100*time.Millisecond, "expected sum result to match")

		assert.NoError(t, system.Stop(ctx))
	})

	t.Run("As ScatterGatherFirst router", func(t *testing.T) {
		ctx := t.Context()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		probe := NewMockTailChopRoutee()
		summationActor, err := system.Spawn(ctx, "summation", probe)
		require.NoError(t, err)
		require.NotNil(t, summationActor)

		poolSize := 2
		routerName := "scatterGatherRouter"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, new(MockRoutee), AsScatterGatherFirst(2*time.Second))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send a broadcast message to the router
		summation := testpb.TestSum_builder{A: 10, B: 20}.Build()
		err = summationActor.Tell(ctx, router, NewBroadcast(summation))
		require.NoError(t, err)

		pause.For(3 * time.Second)
		expected := testpb.TestSumResult_builder{Result: 30}.Build()

		reply, err := Ask(ctx, summationActor, new(testpb.TestGetSumResult), time.Second)
		require.NoError(t, err)
		require.NotNil(t, reply)
		actual, ok := reply.(*testpb.TestSumResult)
		require.True(t, ok)
		assert.True(t, proto.Equal(expected, actual))

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("As ScatterGatherFirst router with StatusFailure", func(t *testing.T) {
		ctx := t.Context()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		probe := NewMockTailChopRoutee()
		summationActor, err := system.Spawn(ctx, "summation", probe)
		require.NoError(t, err)
		require.NotNil(t, summationActor)

		poolSize := 2
		routerName := "scatterGatherRouter"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, new(MockBlockingRoutee), AsScatterGatherFirst(2*time.Second))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send a broadcast message to the router
		summation := testpb.TestSum_builder{A: 10, B: 20}.Build()
		err = summationActor.Tell(ctx, router, NewBroadcast(summation))
		require.NoError(t, err)

		require.True(t, probe.WaitForFailure(5*time.Second), "expected status failure")
		assert.EqualValues(t, 0, probe.Sum())
		assert.EqualValues(t, 1, probe.FailureCount())

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("As ScatterGatherFirst router with routee errors", func(t *testing.T) {
		ctx := t.Context()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		probe := NewMockTailChopRoutee()
		summationActor, err := system.Spawn(ctx, "scatterErrorProbe", probe)
		require.NoError(t, err)
		require.NotNil(t, summationActor)

		routerName := "scatterGatherErrorRouter"
		router, err := system.SpawnRouter(ctx, routerName, 1, new(MockFaultyRoutee), AsScatterGatherFirst(50*time.Millisecond))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		summation := testpb.TestSum_builder{A: 5, B: 7}.Build()
		err = summationActor.Tell(ctx, router, NewBroadcast(summation))
		require.NoError(t, err)

		require.True(t, probe.WaitForFailure(5*time.Second), "expected status failure due to routee errors")
		assert.EqualValues(t, 1, probe.FailureCount())

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("As TailChopping router with StatusFailure", func(t *testing.T) {
		ctx := t.Context()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		probe := NewMockTailChopRoutee()
		pid, err := system.Spawn(ctx, "summation", probe)
		require.NoError(t, err)
		require.NotNil(t, pid)

		poolSize := 2
		routerName := "tailChopRouter"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, new(MockRoutee), AsTailChopping(2*time.Second, 20*time.Millisecond))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send a broadcast message to the router
		summation := testpb.TestSum_builder{A: 10, B: 20, Delay: durationpb.New(3 * time.Second)}.Build()
		err = pid.Tell(ctx, router, NewBroadcast(summation))
		require.NoError(t, err)

		pause.For(3 * time.Second)
		require.True(t, probe.WaitForFailure(5*time.Second), "expected status failure (sum=%d failures=%d)", probe.Sum(), probe.FailureCount())
		assert.EqualValues(t, 0, probe.Sum())
		assert.EqualValues(t, 1, probe.FailureCount())

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("As TailChopping router with exhausted deadline", func(t *testing.T) {
		ctx := t.Context()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		probe := NewMockTailChopRoutee()
		pid, err := system.Spawn(ctx, "tailDeadlineProbe", probe)
		require.NoError(t, err)
		require.NotNil(t, pid)

		routerName := "tailDeadlineRouter"
		router, err := system.SpawnRouter(ctx, routerName, 1, new(MockRoutee), AsTailChopping(time.Nanosecond, time.Nanosecond))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		err = pid.Tell(ctx, router, NewBroadcast(testpb.TestSum_builder{A: 1, B: 2}.Build()))
		require.NoError(t, err)

		require.True(t, probe.WaitForFailure(5*time.Second), "expected status failure due to exhausted deadline")
		assert.EqualValues(t, 1, probe.FailureCount())

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("With PreStart failure due to invalid poolsize", func(t *testing.T) {
		ctx := t.Context()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		routerName := "invalidPoolRouter"
		router, err := system.SpawnRouter(ctx, routerName, 0, new(MockRoutee), AsTailChopping(2*time.Second, 20*time.Millisecond))
		require.Error(t, err)
		require.Nil(t, router)
		assert.ErrorIs(t, err, errors.ErrInvalidRouterPoolSize)

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("With PreStart failure due to invalid TailChopping setting", func(t *testing.T) {
		ctx := t.Context()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		routerName := "invalidTailChopRouter"
		router, err := system.SpawnRouter(ctx, routerName, 1, new(MockRoutee), AsTailChopping(0, 0))
		require.Error(t, err)
		require.Nil(t, router)
		assert.ErrorIs(t, err, errors.ErrTailChopingRouterMisconfigured)

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("With PreStart failure due to invalid ScatterGatherFirst setting", func(t *testing.T) {
		ctx := t.Context()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		routerName := "invalidScatterGatherRouter"
		router, err := system.SpawnRouter(ctx, routerName, 1, new(MockRoutee), AsScatterGatherFirst(0))
		require.Error(t, err)
		require.Nil(t, router)
		assert.ErrorIs(t, err, errors.ErrScatterGatherFirstRouterMisconfigured)

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("With Restarting routee", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"test",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		routeesKind := new(MockFaultyRoutee)
		poolSize := 1
		routingStrategy := FanOutRouting
		routerName := "workerPool"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, routeesKind,
			WithRoutingStrategy(routingStrategy),
			WithRestartRouteeOnFailure(2, time.Second))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send a broadcast message to the router
		err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "msg"}.Build()))
		require.NoError(t, err)

		pause.For(2 * time.Second)

		// this is just for tests purpose
		workerOneName := routeeName(0, routerName)
		workerOneRef, ok := system.findRoutee(workerOneName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)
		require.True(t, workerOneRef.IsRunning())
		require.EqualValues(t, 1, workerOneRef.RestartCount())

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("With Restarting routee with no retries", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"test",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		routeesKind := new(MockFaultyRoutee)
		poolSize := 1
		routingStrategy := FanOutRouting
		routerName := "workerPool"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, routeesKind,
			WithRoutingStrategy(routingStrategy),
			WithRestartRouteeOnFailure(0, -1))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send a broadcast message to the router
		err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "msg"}.Build()))
		require.NoError(t, err)

		pause.For(2 * time.Second)

		// this is just for tests purpose
		workerOneName := routeeName(0, routerName)
		workerOneRef, ok := system.findRoutee(workerOneName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)
		require.True(t, workerOneRef.IsRunning())
		require.EqualValues(t, 1, workerOneRef.RestartCount())

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("With Resuming routee", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"test",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		routeesKind := new(MockFaultyRoutee)
		poolSize := 1
		routingStrategy := FanOutRouting
		routerName := "workerPool"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, routeesKind,
			WithRoutingStrategy(routingStrategy),
			WithResumeRouteeOnFailure())
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send a broadcast message to the router
		err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "msg"}.Build()))
		require.NoError(t, err)

		pause.For(2 * time.Second)

		// this is just for tests purpose
		workerOneName := routeeName(0, routerName)
		workerOneRef, ok := system.findRoutee(workerOneName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)
		require.Zero(t, workerOneRef.RestartCount())
		require.True(t, workerOneRef.IsRunning())

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("With Stopping routee", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"test",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		routeesKind := new(MockFaultyRoutee)
		poolSize := 1
		routingStrategy := FanOutRouting
		routerName := "workerPool"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, routeesKind,
			WithRoutingStrategy(routingStrategy),
			WithStopRouteeOnFailure())
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send a broadcast message to the router
		err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "msg"}.Build()))
		require.NoError(t, err)

		waitForRouteeCount(t, ctx, router, 0)

		// this is just for tests purpose
		workerOneName := routeeName(0, routerName)
		require.Eventually(t, func() bool {
			workerOneRef, ok := system.findRoutee(workerOneName)
			return !ok && workerOneRef == nil
		}, 5*time.Second, 100*time.Millisecond, "expected routee to be removed")

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("With Adjusting Pool Size", func(t *testing.T) {
		ctx := t.Context()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"test",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		routeesKind := new(MockRoutee)
		poolSize := 1
		routingStrategy := FanOutRouting
		routerName := "workerPool"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, routeesKind,
			WithRoutingStrategy(routingStrategy),
			WithStopRouteeOnFailure())
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// scale up the router by 3 (delta)
		err = system.NoSender().Tell(ctx, router, NewAdjustRouterPoolSize(3))
		require.NoError(t, err)

		waitForRouteeCount(t, ctx, router, 4)

		// scale down the router by 2 (delta)
		err = system.NoSender().Tell(ctx, router, NewAdjustRouterPoolSize(-2))
		require.NoError(t, err)

		waitForRouteeCount(t, ctx, router, 2)

		// scale down the router by 3 (delta)
		err = system.NoSender().Tell(ctx, router, NewAdjustRouterPoolSize(-3))
		require.NoError(t, err)

		waitForRouteeCount(t, ctx, router, 0)

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("With ConsistentHash strategy same key routes to same routee", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		extractor := func(msg any) string {
			switch m := msg.(type) {
			case *testpb.TestLog:
				return m.GetText()
			default:
				return ""
			}
		}

		poolSize := 5
		routerName := "consistentHashRouter"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, new(MockRoutee),
			WithConsistentHashRouter(extractor))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send the same key multiple times
		for range 10 {
			err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "sticky-key"}.Build()))
			require.NoError(t, err)
		}

		pause.For(time.Second)

		// exactly one routee should have received all 10 messages
		var totalCount int
		var routeesWithMessages int
		for i := range poolSize {
			name := routeeName(i, routerName)
			ref, ok := system.findRoutee(name)
			require.True(t, ok)
			require.NotNil(t, ref)

			reply, err := Ask(ctx, ref, new(testpb.TestGetCount), time.Second)
			require.NoError(t, err)
			count := reply.(*testpb.TestCount).GetValue()
			if count > 1 {
				routeesWithMessages++
				totalCount += int(count)
			}
		}

		assert.Equal(t, 1, routeesWithMessages, "all messages with the same key should go to one routee")
		// count includes the GetCount request itself (+1), so the routee has 10 TestLog + 1 GetCount = value 11
		assert.Equal(t, 11, totalCount)

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("With ConsistentHash strategy different keys distribute across routees", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		extractor := func(msg any) string {
			switch m := msg.(type) {
			case *testpb.TestLog:
				return m.GetText()
			default:
				return ""
			}
		}

		poolSize := 3
		routerName := "consistentHashDistRouter"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, new(MockRoutee),
			WithConsistentHashRouter(extractor))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send 100 distinct keys to ensure distribution across routees
		for i := range 100 {
			err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: fmt.Sprintf("key-%d", i)}.Build()))
			require.NoError(t, err)
		}

		pause.For(time.Second)

		var routeesWithTraffic int
		for i := range poolSize {
			name := routeeName(i, routerName)
			ref, ok := system.findRoutee(name)
			require.True(t, ok)
			reply, err := Ask(ctx, ref, new(testpb.TestGetCount), time.Second)
			require.NoError(t, err)
			count := reply.(*testpb.TestCount).GetValue()
			if count > 1 {
				routeesWithTraffic++
			}
		}

		assert.GreaterOrEqual(t, routeesWithTraffic, 2, "distinct keys should hit at least 2 routees")

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("With ConsistentHash strategy empty key falls back to random", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		extractor := func(msg any) string {
			return ""
		}

		poolSize := 2
		routerName := "consistentHashFallbackRouter"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, new(MockRoutee),
			WithConsistentHashRouter(extractor))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "something"}.Build()))
		require.NoError(t, err)

		pause.For(time.Second)

		var totalCount int32
		for i := range poolSize {
			name := routeeName(i, routerName)
			ref, ok := system.findRoutee(name)
			require.True(t, ok)
			reply, err := Ask(ctx, ref, new(testpb.TestGetCount), time.Second)
			require.NoError(t, err)
			totalCount += reply.(*testpb.TestCount).GetValue()
		}

		// at least one routee got the message (counter includes the GetCount itself)
		assert.GreaterOrEqual(t, totalCount, int32(3))

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("With ConsistentHash strategy missing key extractor fails validation", func(t *testing.T) {
		ctx := t.Context()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		routerName := "noExtractorRouter"
		router, err := system.SpawnRouter(ctx, routerName, 2, new(MockRoutee),
			WithRoutingStrategy(ConsistentHashRouting))
		require.Error(t, err)
		require.Nil(t, router)
		assert.ErrorIs(t, err, errors.ErrConsistentHashRouterMisconfigured)

		assert.NoError(t, system.Stop(ctx))
	})
	t.Run("With ConsistentHash strategy ring rebuild on scale up", func(t *testing.T) {
		ctx := t.Context()
		logger := log.DiscardLogger
		system, err := NewActorSystem(
			"testSystem",
			WithLogger(logger))

		require.NoError(t, err)
		require.NotNil(t, system)

		require.NoError(t, system.Start(ctx))

		pause.For(time.Second)

		extractor := func(msg any) string {
			switch m := msg.(type) {
			case *testpb.TestLog:
				return m.GetText()
			default:
				return ""
			}
		}

		poolSize := 2
		routerName := "consistentHashScaleRouter"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, new(MockRoutee),
			WithConsistentHashRouter(extractor))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send messages with a key before scaling
		for range 5 {
			err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "scale-key"}.Build()))
			require.NoError(t, err)
		}

		pause.For(time.Second)

		// scale up
		err = system.NoSender().Tell(ctx, router, NewAdjustRouterPoolSize(3))
		require.NoError(t, err)

		waitForRouteeCount(t, ctx, router, 5)

		// send more messages with the same key - should still route consistently
		for range 5 {
			err = Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "scale-key"}.Build()))
			require.NoError(t, err)
		}

		pause.For(time.Second)

		// verify messages still route consistently (exactly one routee handles "scale-key")
		var receiversAfterScale int
		for i := range 5 {
			name := routeeName(i, routerName)
			ref, ok := system.findRoutee(name)
			if !ok || ref == nil {
				continue
			}
			reply, err := Ask(ctx, ref, new(testpb.TestGetCount), time.Second)
			require.NoError(t, err)
			count := reply.(*testpb.TestCount).GetValue()
			if count > 1 {
				receiversAfterScale++
			}
		}

		// the key may have been remapped to a new node on scale-up, but at most
		// two routees should have received messages for this key (one before, one after)
		assert.LessOrEqual(t, receiversAfterScale, 2)

		assert.NoError(t, system.Stop(ctx))
	})
}

// TestRouterRoundRobinRotatesInOrder checks that round-robin visits the routees
// in a fixed rotation: 50 broadcasts over 5 routees give each routee exactly 10.
func TestRouterRoundRobinRotatesInOrder(t *testing.T) {
	ctx := t.Context()
	system, err := NewActorSystem("testSystem", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))
	t.Cleanup(func() { assert.NoError(t, system.Stop(context.Background())) })

	const poolSize = 5
	const total = 50
	routerName := "roundRobinPool"
	router, err := system.SpawnRouter(ctx, routerName, poolSize, new(MockRoutee), WithRoutingStrategy(RoundRobinRouting))
	require.NoError(t, err)
	waitForRouteeCount(t, ctx, router, poolSize)

	for range total {
		require.NoError(t, Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "msg"}.Build())))
	}

	routees := make([]*PID, 0, poolSize)
	for i := range poolSize {
		routee, ok := system.findRoutee(routeeName(i, routerName))
		require.True(t, ok)
		routees = append(routees, routee)
	}

	// every routee has processed PostStart; the broadcasts add up to total
	require.Eventually(t, func() bool {
		processed := 0
		for _, routee := range routees {
			processed += routee.ProcessedCount()
		}
		return processed == poolSize+total
	}, 5*time.Second, 10*time.Millisecond)

	for _, routee := range routees {
		reply, err := Ask(ctx, routee, new(testpb.TestGetCount), time.Second)
		require.NoError(t, err)
		// 10 TestLog plus the TestGetCount itself
		assert.EqualValues(t, total/poolSize+1, reply.(*testpb.TestCount).GetValue(), "routee %s", routee.Name())
	}
}

// TestRouterFanOutPreservesPerRouteeOrder checks that every routee receives the
// fan-out copies in the order the router handled the broadcasts.
func TestRouterFanOutPreservesPerRouteeOrder(t *testing.T) {
	ctx := t.Context()
	system, err := NewActorSystem("testSystem", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))
	t.Cleanup(func() { assert.NoError(t, system.Stop(context.Background())) })

	const poolSize = 2
	const total = 500
	routerName := "fanOutPool"
	router, err := system.SpawnRouter(ctx, routerName, poolSize, new(MockOrderRecordingActor))
	require.NoError(t, err)
	waitForRouteeCount(t, ctx, router, poolSize)

	for i := 1; i <= total; i++ {
		require.NoError(t, Tell(ctx, router, NewBroadcast(testpb.TestCount_builder{Value: int32(i)}.Build())))
	}

	// the routees leave the trailing broadcast unhandled; a routee that has
	// started its turn has finished handling every TestCount before it
	require.NoError(t, Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "done"}.Build())))

	for i := range poolSize {
		routee, ok := system.findRoutee(routeeName(i, routerName))
		require.True(t, ok)

		// PostStart, the TestCount broadcasts and the trailing one
		require.Eventually(t, func() bool {
			return routee.ProcessedCount() >= total+2
		}, 5*time.Second, 10*time.Millisecond)

		seen := routee.Actor().(*MockOrderRecordingActor).Seen()
		require.Len(t, seen, total)

		for j, value := range seen {
			require.EqualValues(t, j+1, value, "routee %s received the broadcasts out of order", routee.Name())
		}
	}
}

// TestRouterDropsStoppedRouteeBeforeRouting checks that a routee that stopped on
// its own gets no message: with no live routee left, the broadcast becomes a
// dead letter and the router shuts down.
func TestRouterDropsStoppedRouteeBeforeRouting(t *testing.T) {
	ctx := t.Context()
	system, err := NewActorSystem("testSystem", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))
	t.Cleanup(func() { assert.NoError(t, system.Stop(context.Background())) })

	consumer, err := system.Subscribe()
	require.NoError(t, err)

	routerName := "lonePool"
	router, err := system.SpawnRouter(ctx, routerName, 1, new(MockRoutee), WithRoutingStrategy(RoundRobinRouting))
	require.NoError(t, err)
	waitForRouteeCount(t, ctx, router, 1)

	routee, ok := system.findRoutee(routeeName(0, routerName))
	require.True(t, ok)
	require.NoError(t, routee.Shutdown(ctx))
	require.False(t, routee.IsRunning())

	require.NoError(t, Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "msg"}.Build())))

	require.Eventually(t, func() bool {
		return !router.IsRunning()
	}, 5*time.Second, 10*time.Millisecond, "router kept running after its only routee stopped")

	require.Eventually(t, func() bool {
		for message := range consumer.Iterator() {
			if _, ok := message.Payload().(*Deadletter); ok {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "the broadcast left no dead letter")
}

// TestRouterResumeReinsertsRoutee checks that the Resume directive puts a routee
// back into the pool after a broadcast handled during its suspension removed it.
func TestRouterResumeReinsertsRoutee(t *testing.T) {
	ctx := t.Context()
	system, err := NewActorSystem("testSystem", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))
	t.Cleanup(func() { assert.NoError(t, system.Stop(context.Background())) })

	routerName := "resumePool"
	router, err := system.SpawnRouter(ctx, routerName, 2, new(MockRoutee), WithResumeRouteeOnFailure())
	require.NoError(t, err)
	waitForRouteeCount(t, ctx, router, 2)

	routee, ok := system.findRoutee(routeeName(0, routerName))
	require.True(t, ok)
	routee.suspend("test suspension")

	// a broadcast routed while the routee is suspended drops it from the pool
	require.NoError(t, Tell(ctx, router, NewBroadcast(testpb.TestLog_builder{Text: "msg"}.Build())))
	waitForRouteeCount(t, ctx, router, 1)

	// the routee's failure then reaches the router, which resumes it
	require.NoError(t, routee.Tell(ctx, router, NewPanicSignal(&anypb.Any{}, "test panic signal", time.Now())))
	waitForRouteeCount(t, ctx, router, 2)
	require.True(t, routee.IsRunning())
}

// TestRouterScaleUpAfterRemovalGrowsPool checks that growing the pool after a
// routee was removed adds the requested number of routees under fresh names.
func TestRouterScaleUpAfterRemovalGrowsPool(t *testing.T) {
	ctx := t.Context()
	system, err := NewActorSystem("testSystem", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))
	t.Cleanup(func() { assert.NoError(t, system.Stop(context.Background())) })

	routerName := "scalePool"
	router, err := system.SpawnRouter(ctx, routerName, 2, new(MockRoutee), WithStopRouteeOnFailure())
	require.NoError(t, err)
	waitForRouteeCount(t, ctx, router, 2)

	routee, ok := system.findRoutee(routeeName(0, routerName))
	require.True(t, ok)
	require.NoError(t, routee.Tell(ctx, router, NewPanicSignal(&anypb.Any{}, "test panic signal", time.Now())))
	waitForRouteeCount(t, ctx, router, 1)

	require.NoError(t, system.NoSender().Tell(ctx, router, NewAdjustRouterPoolSize(1)))
	waitForRouteeCount(t, ctx, router, 2)

	// the new routee takes the next free index, not the removed routee's
	_, ok = system.findRoutee(routeeName(2, routerName))
	require.True(t, ok)
}

// TestRouterScatterGatherDoesNotBlockRouter checks that a scatter-gather request
// nobody answers in time does not hold the router: a control message queued
// behind it is answered at once, and the sender still gets the StatusFailure.
func TestRouterScatterGatherDoesNotBlockRouter(t *testing.T) {
	ctx := t.Context()
	system, err := NewActorSystem("testSystem", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))
	t.Cleanup(func() { assert.NoError(t, system.Stop(context.Background())) })

	probe := NewMockTailChopRoutee()
	sender, err := system.Spawn(ctx, "scatterProbe", probe)
	require.NoError(t, err)

	const within = 2 * time.Second
	router, err := system.SpawnRouter(ctx, "scatterPool", 2, new(MockRoutee), AsScatterGatherFirst(within))
	require.NoError(t, err)
	waitForRouteeCount(t, ctx, router, 2)

	// the routees answer only after the time budget
	slow := testpb.TestSum_builder{A: 10, B: 20, Delay: durationpb.New(within + time.Second)}.Build()
	require.NoError(t, sender.Tell(ctx, router, NewBroadcast(slow)))

	// queued behind the broadcast in the router's mailbox
	reply, err := Ask(ctx, router, new(GetRoutees), within/2)
	require.NoError(t, err, "the router did not answer while gathering replies")
	require.Len(t, reply.(*Routees).Names(), 2)

	require.True(t, probe.WaitForFailure(2*within), "expected status failure")
	assert.EqualValues(t, 1, probe.FailureCount())
}

// TestRouterTailChoppingDoesNotBlockRouter checks that a tail-chopping request
// nobody answers in time does not hold the router: a control message queued
// behind it is answered at once, and the sender still gets the StatusFailure.
func TestRouterTailChoppingDoesNotBlockRouter(t *testing.T) {
	ctx := t.Context()
	system, err := NewActorSystem("testSystem", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, system.Start(ctx))
	t.Cleanup(func() { assert.NoError(t, system.Stop(context.Background())) })

	probe := NewMockTailChopRoutee()
	sender, err := system.Spawn(ctx, "tailChopProbe", probe)
	require.NoError(t, err)

	const within = 2 * time.Second
	router, err := system.SpawnRouter(ctx, "tailChopPool", 2, new(MockRoutee), AsTailChopping(within, 20*time.Millisecond))
	require.NoError(t, err)
	waitForRouteeCount(t, ctx, router, 2)

	// the routees answer only after the time budget
	slow := testpb.TestSum_builder{A: 10, B: 20, Delay: durationpb.New(within + time.Second)}.Build()
	require.NoError(t, sender.Tell(ctx, router, NewBroadcast(slow)))

	// queued behind the broadcast in the router's mailbox
	reply, err := Ask(ctx, router, new(GetRoutees), within/2)
	require.NoError(t, err, "the router did not answer while probing routees")
	require.Len(t, reply.(*Routees).Names(), 2)

	require.True(t, probe.WaitForFailure(2*within), "expected status failure")
	assert.EqualValues(t, 1, probe.FailureCount())
}

func TestFindRouteeMissReleasesLock(t *testing.T) {
	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	system := sys.(*actorSystem)
	_, ok := system.findRoutee("does-not-exist")
	require.False(t, ok)

	// The not-found path used to leak read locks, deadlocking the next
	// write-lock acquisition during shutdown.
	require.True(t, system.locker.TryLock(), "findRoutee leaked a read lock")
	system.locker.Unlock()
}

func TestConsistentHashRing(t *testing.T) {
	t.Run("empty ring returns empty string", func(t *testing.T) {
		ring := newConsistentHashRing(nil, 0)
		assert.Empty(t, ring.lookup("anything"))
	})

	t.Run("single member receives all keys", func(t *testing.T) {
		ring := newConsistentHashRing(nil, 0)
		ring.set([]string{"node-A"})

		for i := range 100 {
			got := ring.lookup(fmt.Sprintf("key-%d", i))
			assert.Equal(t, "node-A", got)
		}
	})

	t.Run("same key always maps to same member", func(t *testing.T) {
		ring := newConsistentHashRing(nil, 0)
		ring.set([]string{"node-A", "node-B", "node-C"})

		for i := range 200 {
			key := fmt.Sprintf("user:%d", i)
			first := ring.lookup(key)
			for j := range 5 {
				_ = j
				assert.Equal(t, first, ring.lookup(key), "key=%s must be stable", key)
			}
		}
	})

	t.Run("keys distribute across all members", func(t *testing.T) {
		members := []string{"node-A", "node-B", "node-C", "node-D", "node-E"}
		ring := newConsistentHashRing(nil, 0)
		ring.set(members)

		counts := make(map[string]int, len(members))
		total := 10_000
		for i := range total {
			member := ring.lookup(fmt.Sprintf("key-%d", i))
			counts[member]++
		}

		assert.Len(t, counts, len(members), "every member should receive at least one key")

		expected := float64(total) / float64(len(members))
		for member, count := range counts {
			ratio := float64(count) / expected
			assert.InDelta(t, 1.0, ratio, 0.35,
				"member=%s count=%d expected≈%.0f ratio=%.2f", member, count, expected, ratio)
		}
	})

	t.Run("adding a member remaps only a fraction of keys", func(t *testing.T) {
		members := []string{"A", "B", "C"}
		ring := newConsistentHashRing(nil, 0)
		ring.set(members)

		total := 5000
		before := make([]string, total)
		for i := range total {
			before[i] = ring.lookup(fmt.Sprintf("k%d", i))
		}

		ring.set(append(members, "D"))

		moved := 0
		for i := range total {
			after := ring.lookup(fmt.Sprintf("k%d", i))
			if after != before[i] {
				moved++
			}
		}

		idealFraction := 1.0 / float64(len(members)+1)
		actualFraction := float64(moved) / float64(total)
		assert.Less(t, actualFraction, idealFraction*2,
			"expected ≤%.2f%% remapped, got %.2f%%", idealFraction*200, actualFraction*100)
	})

	t.Run("removing a member only remaps that member's keys", func(t *testing.T) {
		members := []string{"A", "B", "C", "D"}
		ring := newConsistentHashRing(nil, 0)
		ring.set(members)

		total := 5000
		before := make(map[string]string, total)
		for i := range total {
			key := fmt.Sprintf("k%d", i)
			before[key] = ring.lookup(key)
		}

		ring.set([]string{"A", "B", "D"})

		for key, prev := range before {
			after := ring.lookup(key)
			if prev != "C" {
				assert.Equal(t, prev, after,
					"key=%s was on %s (not removed), should not have moved to %s", key, prev, after)
			}
		}
	})

	t.Run("set clears previous members", func(t *testing.T) {
		ring := newConsistentHashRing(nil, 0)
		ring.set([]string{"A", "B"})
		require.NotEmpty(t, ring.lookup("x"))

		ring.set(nil)
		assert.Empty(t, ring.lookup("x"))
		assert.Zero(t, ring.len())
	})

	t.Run("custom hasher is used", func(t *testing.T) {
		ring := newConsistentHashRing(hash.DefaultHasher(), 50)
		ring.set([]string{"X", "Y"})
		assert.Equal(t, 100, ring.len())

		got := ring.lookup("test-key")
		assert.Contains(t, []string{"X", "Y"}, got)
	})

	t.Run("custom virtual node count", func(t *testing.T) {
		ring := newConsistentHashRing(nil, 300)
		ring.set([]string{"A", "B"})
		assert.Equal(t, 600, ring.len())
	})

	t.Run("default virtual node count", func(t *testing.T) {
		ring := newConsistentHashRing(nil, 0)
		ring.set([]string{"A"})
		assert.Equal(t, defaultVirtualNodes, ring.len())
	})

	t.Run("large member set", func(t *testing.T) {
		members := make([]string, 100)
		for i := range members {
			members[i] = fmt.Sprintf("node-%d", i)
		}

		ring := newConsistentHashRing(nil, 0)
		ring.set(members)
		assert.Equal(t, 100*defaultVirtualNodes, ring.len())

		seen := make(map[string]struct{})
		for i := range 50_000 {
			m := ring.lookup(fmt.Sprintf("k%d", i))
			seen[m] = struct{}{}
		}
		assert.Greater(t, len(seen), 90, "at least 90 of 100 members should see traffic")
	})

	t.Run("distribution variance", func(t *testing.T) {
		members := []string{"A", "B", "C"}
		ring := newConsistentHashRing(nil, 0)
		ring.set(members)

		counts := make(map[string]int, len(members))
		total := 30_000
		for i := range total {
			counts[ring.lookup(fmt.Sprintf("k%d", i))]++
		}

		mean := float64(total) / float64(len(members))
		var sumSqDev float64
		for _, c := range counts {
			diff := float64(c) - mean
			sumSqDev += diff * diff
		}
		stddev := math.Sqrt(sumSqDev / float64(len(members)))
		cv := stddev / mean
		assert.Less(t, cv, 0.15, "coefficient of variation should be small")
	})
}
