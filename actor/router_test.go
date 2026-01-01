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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/test/data/testpb"
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
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
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

		expected := &testpb.TestCount{Value: 2}

		reply, err := Ask(ctx, workerOneRef, new(testpb.TestGetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		require.True(t, proto.Equal(expected, reply))
		reply, err = Ask(ctx, workerTwoRef, new(testpb.TestGetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.True(t, proto.Equal(expected, reply))

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

		require.NoError(t, workerOneRef.Tell(ctx, router, &goaktpb.PanicSignal{
			Message:   &anypb.Any{},
			Reason:    "test panic signal",
			Timestamp: timestamppb.Now(),
		}))

		require.NoError(t, workerTwoRef.Tell(ctx, router, &goaktpb.PanicSignal{
			Message:   &anypb.Any{},
			Reason:    "test panic signal",
			Timestamp: timestamppb.Now(),
		}))

		// send a broadcast message to the router
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		pause.For(time.Second)

		ref, err := system.LocalActor(routerName)
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
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		pause.For(time.Second)

		// this is just for tests purpose
		workerName := routeeName(0, routerName)

		workerOneRef, ok := system.findRoutee(workerName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)

		expected := &testpb.TestCount{Value: 2}

		reply, err := Ask(ctx, workerOneRef, new(testpb.TestGetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.True(t, proto.Equal(expected, reply))

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

		require.NoError(t, workerOneRef.Tell(ctx, router, &goaktpb.PanicSignal{
			Message:   &anypb.Any{},
			Reason:    "test panic signal",
			Timestamp: timestamppb.Now(),
		}))

		pause.For(time.Second)

		// send a broadcast message to the router
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		pause.For(time.Second)
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
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		pause.For(time.Second)

		// this is just for tests purpose
		workerName := routeeName(0, routerName)

		workerOneRef, ok := system.findRoutee(workerName)
		require.True(t, ok)
		require.NotNil(t, workerOneRef)

		expected := &testpb.TestCount{Value: 2}

		reply, err := Ask(ctx, workerOneRef, new(testpb.TestGetCount), time.Minute)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.True(t, proto.Equal(expected, reply))

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

		summationActor, err := system.Spawn(ctx, "summation", NewMockSum())
		require.NoError(t, err)
		require.NotNil(t, summationActor)

		poolSize := 2
		routerName := "tailChopRouter"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, new(MockRoutee), AsTailChopping(2*time.Second, 20*time.Millisecond))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send a broadcast message to the router
		summation := &testpb.TestSum{A: 10, B: 20}
		message, _ := anypb.New(summation)
		err = summationActor.Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		expected := &testpb.TestSumResult{Result: 30}
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

		probe := NewTailChopProbe()
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
		summation := &testpb.TestSum{A: 10, B: 20}
		message, _ := anypb.New(summation)
		err = summationActor.Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		pause.For(3 * time.Second)
		expected := &testpb.TestSumResult{Result: 30}

		reply, err := Ask(ctx, summationActor, new(testpb.TestGetSumResult), time.Second)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.True(t, proto.Equal(expected, reply))

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

		probe := NewTailChopProbe()
		summationActor, err := system.Spawn(ctx, "summation", probe)
		require.NoError(t, err)
		require.NotNil(t, summationActor)

		poolSize := 2
		routerName := "scatterGatherRouter"
		router, err := system.SpawnRouter(ctx, routerName, poolSize, new(BlockingRoutee), AsScatterGatherFirst(2*time.Second))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		// send a broadcast message to the router
		summation := &testpb.TestSum{A: 10, B: 20}
		message, _ := anypb.New(summation)
		err = summationActor.Tell(ctx, router, &goaktpb.Broadcast{Message: message})
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

		probe := NewTailChopProbe()
		summationActor, err := system.Spawn(ctx, "scatterErrorProbe", probe)
		require.NoError(t, err)
		require.NotNil(t, summationActor)

		routerName := "scatterGatherErrorRouter"
		router, err := system.SpawnRouter(ctx, routerName, 1, new(MockFaultyRoutee), AsScatterGatherFirst(50*time.Millisecond))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		summation := &testpb.TestSum{A: 5, B: 7}
		message, _ := anypb.New(summation)
		err = summationActor.Tell(ctx, router, &goaktpb.Broadcast{Message: message})
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

		probe := NewTailChopProbe()
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
		summation := &testpb.TestSum{A: 10, B: 20, Delay: durationpb.New(3 * time.Second)}
		message, _ := anypb.New(summation)
		err = pid.Tell(ctx, router, &goaktpb.Broadcast{Message: message})
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

		probe := NewTailChopProbe()
		pid, err := system.Spawn(ctx, "tailDeadlineProbe", probe)
		require.NoError(t, err)
		require.NotNil(t, pid)

		routerName := "tailDeadlineRouter"
		router, err := system.SpawnRouter(ctx, routerName, 1, new(MockRoutee), AsTailChopping(time.Nanosecond, time.Nanosecond))
		require.NoError(t, err)
		require.NotNil(t, router)

		pause.For(time.Second)

		message, _ := anypb.New(&testpb.TestSum{A: 1, B: 2})
		err = pid.Tell(ctx, router, &goaktpb.Broadcast{Message: message})
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
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
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
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
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
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
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
		message, _ := anypb.New(&testpb.TestLog{Text: "msg"})
		err = Tell(ctx, router, &goaktpb.Broadcast{Message: message})
		require.NoError(t, err)

		pause.For(2 * time.Second)

		response, err := Ask(ctx, router, new(goaktpb.GetRoutees), time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		routeesResponse, ok := response.(*goaktpb.Routees)
		require.True(t, ok)
		require.NotNil(t, routeesResponse)
		require.Empty(t, routeesResponse.GetNames())

		// this is just for tests purpose
		workerOneName := routeeName(0, routerName)
		workerOneRef, ok := system.findRoutee(workerOneName)
		require.False(t, ok)
		require.Nil(t, workerOneRef)

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
		err = system.NoSender().Tell(ctx, router, &goaktpb.AdjustRouterPoolSize{
			PoolSize: 3,
		})
		require.NoError(t, err)

		pause.For(time.Second)

		response, err := Ask(ctx, router, new(goaktpb.GetRoutees), time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		routeesResponse, ok := response.(*goaktpb.Routees)
		require.True(t, ok)
		require.NotNil(t, routeesResponse)
		require.Len(t, routeesResponse.GetNames(), 4)

		// scale down the router by 2 (delta)
		err = system.NoSender().Tell(ctx, router, &goaktpb.AdjustRouterPoolSize{
			PoolSize: -2,
		})
		require.NoError(t, err)

		pause.For(time.Second)

		response, err = Ask(ctx, router, new(goaktpb.GetRoutees), time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		routeesResponse, ok = response.(*goaktpb.Routees)
		require.True(t, ok)
		require.NotNil(t, routeesResponse)
		require.Len(t, routeesResponse.GetNames(), 2)

		// scale down the router by 3 (delta)
		err = system.NoSender().Tell(ctx, router, &goaktpb.AdjustRouterPoolSize{
			PoolSize: -3,
		})
		require.NoError(t, err)

		pause.For(time.Second)

		response, err = Ask(ctx, router, new(goaktpb.GetRoutees), time.Second)
		require.NoError(t, err)
		require.NotNil(t, response)
		routeesResponse, ok = response.(*goaktpb.Routees)
		require.True(t, ok)
		require.NotNil(t, routeesResponse)
		require.Empty(t, routeesResponse.GetNames())

		assert.NoError(t, system.Stop(ctx))
	})
}
