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

	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestSchedulerListSchedules(t *testing.T) {
	t.Run("With no schedules", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)

		require.NoError(t, system.Start(ctx))
		pause.For(time.Second)

		assert.Empty(t, system.ListSchedules())

		require.NoError(t, system.Stop(ctx))
	})
	t.Run("With scheduler not started", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)

		require.NoError(t, system.Start(ctx))
		pause.For(time.Second)

		// test purpose only
		typedSystem := system.(*actorSystem)
		typedSystem.scheduler.Stop(ctx)

		assert.Empty(t, system.ListSchedules())

		require.NoError(t, system.Stop(ctx))
	})
	t.Run("With an interval schedule", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)

		require.NoError(t, system.Start(ctx))
		pause.For(time.Second)

		actorRef, err := system.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		pause.For(time.Second)

		message := new(testpb.TestSend)
		err = system.Schedule(ctx, message, actorRef, 100*time.Millisecond, WithReference("interval-ref"))
		require.NoError(t, err)

		schedules := system.ListSchedules()
		require.Len(t, schedules, 1)
		info := schedules[0]
		assert.Equal(t, "interval-ref", info.Reference)
		assert.Equal(t, TriggerKindInterval, info.TriggerKind)
		assert.Equal(t, 100*time.Millisecond, info.Interval)
		assert.Empty(t, info.Expression)
		assert.Equal(t, actorRef.Path().String(), info.Address)
		assert.False(t, info.NextFireTime.IsZero())

		require.NoError(t, system.CancelSchedule("interval-ref"))
		assert.Empty(t, system.ListSchedules())

		require.NoError(t, system.Stop(ctx))
	})
	t.Run("With a ScheduleOnce schedule that disappears once delivered", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)

		require.NoError(t, system.Start(ctx))
		pause.For(time.Second)

		actorRef, err := system.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		pause.For(time.Second)

		message := new(testpb.TestSend)
		err = system.ScheduleOnce(ctx, message, actorRef, 50*time.Millisecond, WithReference("run-once"))
		require.NoError(t, err)

		schedules := system.ListSchedules()
		require.Len(t, schedules, 1)
		info := schedules[0]
		assert.Equal(t, "run-once", info.Reference)
		assert.Equal(t, TriggerKindOnce, info.TriggerKind)
		assert.Equal(t, 50*time.Millisecond, info.Interval)
		assert.Empty(t, info.Expression)
		assert.Equal(t, actorRef.Path().String(), info.Address)

		require.Eventually(t, func() bool {
			return actorRef.ProcessedCount()-1 >= 1
		}, 2*time.Second, 20*time.Millisecond)

		// the one-shot has fired: it must no longer be listed even though it was
		// never explicitly canceled.
		require.Eventually(t, func() bool {
			return len(system.ListSchedules()) == 0
		}, 2*time.Second, 20*time.Millisecond)

		require.NoError(t, system.Stop(ctx))
	})
	t.Run("With a cron schedule", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)

		require.NoError(t, system.Start(ctx))
		pause.For(time.Second)

		actorRef, err := system.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		pause.For(time.Second)

		message := new(testpb.TestSend)
		const expr = "* * * ? * *"
		err = system.ScheduleWithCron(ctx, message, actorRef, expr, WithReference("cron-ref"))
		require.NoError(t, err)

		schedules := system.ListSchedules()
		require.Len(t, schedules, 1)
		info := schedules[0]
		assert.Equal(t, "cron-ref", info.Reference)
		assert.Equal(t, TriggerKindCron, info.TriggerKind)
		assert.Equal(t, expr, info.Expression)
		assert.Zero(t, info.Interval)
		assert.Equal(t, actorRef.Path().String(), info.Address)
		assert.False(t, info.NextFireTime.IsZero())

		require.NoError(t, system.CancelSchedule("cron-ref"))
		assert.Empty(t, system.ListSchedules())

		require.NoError(t, system.Stop(ctx))
	})
	t.Run("With multiple schedules", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger
		system, err := NewActorSystem("test", WithLogger(logger))
		require.NoError(t, err)

		require.NoError(t, system.Start(ctx))
		pause.For(time.Second)

		actorRef, err := system.Spawn(ctx, "test", NewMockActor())
		require.NoError(t, err)
		pause.For(time.Second)

		message := new(testpb.TestSend)
		require.NoError(t, system.Schedule(ctx, message, actorRef, 5*time.Second, WithReference("interval-ref")))
		require.NoError(t, system.ScheduleOnce(ctx, message, actorRef, 5*time.Second, WithReference("once-ref")))
		require.NoError(t, system.ScheduleWithCron(ctx, message, actorRef, "0 0 0 1 1 ?", WithReference("cron-ref")))

		schedules := system.ListSchedules()
		require.Len(t, schedules, 3)

		byReference := make(map[string]ScheduleInfo, len(schedules))
		for _, info := range schedules {
			byReference[info.Reference] = info
		}

		require.Contains(t, byReference, "interval-ref")
		require.Contains(t, byReference, "once-ref")
		require.Contains(t, byReference, "cron-ref")
		assert.Equal(t, TriggerKindInterval, byReference["interval-ref"].TriggerKind)
		assert.Equal(t, TriggerKindOnce, byReference["once-ref"].TriggerKind)
		assert.Equal(t, TriggerKindCron, byReference["cron-ref"].TriggerKind)

		require.NoError(t, system.Stop(ctx))
	})
}
