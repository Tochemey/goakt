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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestActorStarted(t *testing.T) {
	ctx := t.Context()
	host := "127.0.0.1"
	ports := dynaport.Get(1)

	actorSystem, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, ports[0])),
		WithLogger(log.DiscardLogger))

	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	require.NoError(t, actorSystem.Start(ctx))

	// create a subscriber
	consumer, err := actorSystem.Subscribe()
	require.NoError(t, err)
	require.NotNil(t, consumer)

	// create actor1
	pid, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
	require.NoError(t, err)
	require.NotNil(t, pid)

	var started []*ActorStarted
	for event := range consumer.Iterator() {
		payload := event.Payload()
		if actorStarted, ok := payload.(*ActorStarted); ok {
			started = append(started, actorStarted)
		}
	}

	require.NotEmpty(t, started)
	require.Len(t, started, 1)
	require.Equal(t, pid.ID(), started[0].Address())
	require.NotZero(t, started[0].StartedAt())

	assert.NoError(t, actorSystem.Stop(ctx))
}

func TestActorStopped(t *testing.T) {
	ctx := t.Context()
	host := "127.0.0.1"
	ports := dynaport.Get(1)

	actorSystem, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, ports[0])),
		WithLogger(log.DiscardLogger))

	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	require.NoError(t, actorSystem.Start(ctx))

	// create a subscriber
	consumer, err := actorSystem.Subscribe()
	require.NoError(t, err)
	require.NotNil(t, consumer)

	// create actor1
	pid, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
	require.NoError(t, err)
	require.NotNil(t, pid)

	// wait a while and stop the actor
	pause.For(time.Second)

	require.NoError(t, pid.Shutdown(ctx))

	var stopped []*ActorStopped
	for event := range consumer.Iterator() {
		payload := event.Payload()
		if actorStopped, ok := payload.(*ActorStopped); ok {
			stopped = append(stopped, actorStopped)
		}
	}

	require.NotEmpty(t, stopped)
	require.Len(t, stopped, 1)
	require.Equal(t, pid.ID(), stopped[0].Address())
	require.NotZero(t, stopped[0].StoppedAt())

	assert.NoError(t, actorSystem.Stop(ctx))
}

func TestActorPassivated(t *testing.T) {
	ctx := context.TODO()
	ports := dynaport.Get(1)
	host := "127.0.0.1"

	actorSystem, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, ports[0])),
		WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	require.NoError(t, actorSystem.Start(ctx))

	// create a subscriber
	consumer, err := actorSystem.Subscribe()
	require.NoError(t, err)
	require.NotNil(t, consumer)

	// create the actor path
	pid, err := actorSystem.Spawn(ctx, "test", NewMockActor(),
		WithPassivationStrategy(passivation.NewTimeBasedStrategy(passivateAfter)))
	require.NoError(t, err)
	assert.NotNil(t, pid)

	// let us sleep for some time to make the actor idle
	wg := sync.WaitGroup{}
	wg.Go(func() {
		pause.For(receivingDelay)
	})
	// block until timer is up
	wg.Wait()

	var passivated []*ActorPassivated
	for event := range consumer.Iterator() {
		payload := event.Payload()
		if actorPassivated, ok := payload.(*ActorPassivated); ok {
			passivated = append(passivated, actorPassivated)
		}
	}

	require.NotEmpty(t, passivated)
	require.Len(t, passivated, 1)
	require.Equal(t, pid.ID(), passivated[0].Address())
	require.NotZero(t, passivated[0].PassivatedAt())

	// let us send a message to the actor
	err = Tell(ctx, pid, new(testpb.TestSend))
	assert.Error(t, err)
	assert.ErrorIs(t, err, errors.ErrDead)
	assert.NoError(t, actorSystem.Stop(ctx))
}

func TestActorChildCreated(t *testing.T) {
	ctx := context.TODO()
	host := "127.0.0.1"
	ports := dynaport.Get(1)

	actorSystem, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, ports[0])),
		WithLogger(log.DiscardLogger))

	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	require.NoError(t, actorSystem.Start(ctx))

	// create a subscriber
	consumer, err := actorSystem.Subscribe()
	require.NoError(t, err)
	require.NotNil(t, consumer)

	// create the parent actor
	parent, err := actorSystem.Spawn(ctx, "Parent", NewMockSupervisor())

	require.NoError(t, err)
	assert.NotNil(t, parent)

	// create the child actor
	child, err := parent.SpawnChild(ctx, "child", NewMockSupervised(), WithMailbox(NewBoundedMailbox(20)))
	assert.NoError(t, err)
	assert.NotNil(t, child)
	pause.For(time.Second)
	assert.Len(t, parent.Children(), 1)

	var childCreated []*ActorChildCreated
	for event := range consumer.Iterator() {
		payload := event.Payload()
		if actorChildCreated, ok := payload.(*ActorChildCreated); ok {
			childCreated = append(childCreated, actorChildCreated)
		}
	}

	require.NotEmpty(t, childCreated)
	require.Len(t, childCreated, 1)
	require.Equal(t, child.ID(), childCreated[0].Address())
	require.Equal(t, parent.ID(), childCreated[0].Parent())
	require.NotZero(t, childCreated[0].CreatedAt())

	//stop the actor
	err = parent.Shutdown(ctx)
	assert.NoError(t, err)
	pause.For(time.Second)
	assert.NoError(t, actorSystem.Stop(ctx))
}

func TestActorRestarted(t *testing.T) {
	ctx := context.TODO()
	host := "127.0.0.1"
	ports := dynaport.Get(1)

	actorSystem, err := NewActorSystem("testSys",
		WithRemote(remote.NewConfig(host, ports[0])),
		WithLogger(log.DiscardLogger))

	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	require.NoError(t, actorSystem.Start(ctx))

	// create a subscriber
	consumer, err := actorSystem.Subscribe()
	require.NoError(t, err)
	require.NotNil(t, consumer)

	pid, err := actorSystem.Spawn(ctx, "test", NewMockActor(),
		WithPassivationStrategy(passivation.NewTimeBasedStrategy(10*time.Second)),
	)

	require.NoError(t, err)
	assert.NotNil(t, pid)

	pause.For(time.Second)

	assert.NotZero(t, pid.Uptime())

	// stop the actor
	err = pid.Shutdown(ctx)
	assert.NoError(t, err)

	pause.For(time.Second)
	assert.Zero(t, pid.Uptime())

	// let us send a message to the actor
	err = Tell(ctx, pid, new(testpb.TestSend))
	assert.Error(t, err)
	assert.ErrorIs(t, err, errors.ErrDead)

	// restart the actor
	err = pid.Restart(ctx)
	assert.NoError(t, err)
	assert.True(t, pid.IsRunning())
	assert.NotZero(t, pid.Uptime())

	var restarted []*ActorRestarted
	for event := range consumer.Iterator() {
		payload := event.Payload()
		if actorRestarted, ok := payload.(*ActorRestarted); ok {
			restarted = append(restarted, actorRestarted)
		}
	}

	require.NotEmpty(t, restarted)
	require.Len(t, restarted, 1)
	require.Equal(t, pid.ID(), restarted[0].Address())
	require.NotZero(t, restarted[0].RestartedAt())

	// let us send 10 messages to the actor
	count := 10
	for range count {
		err = Tell(ctx, pid, new(testpb.TestSend))
		assert.NoError(t, err)
	}

	// stop the actor
	err = pid.Shutdown(ctx)
	assert.NoError(t, err)
	assert.NoError(t, actorSystem.Stop(ctx))
}

func TestActorSuspended(t *testing.T) {
	ctx := context.Background()
	actorSystem, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

	// start the actor system
	err := actorSystem.Start(ctx)
	assert.NoError(t, err)

	consumer, err := actorSystem.Subscribe()
	require.NoError(t, err)
	require.NotNil(t, consumer)

	// create actor1
	pid, err := actorSystem.Spawn(ctx, "Exchange1", &exchanger{})
	require.NoError(t, err)
	require.NotNil(t, pid)

	// suspend actor2
	pid.suspend("test")
	require.True(t, pid.IsSuspended())
	require.False(t, pid.IsRunning())

	var suspended []*ActorSuspended
	for message := range consumer.Iterator() {
		payload := message.Payload()
		actorSuspended, ok := payload.(*ActorSuspended)
		if ok {
			suspended = append(suspended, actorSuspended)
		}
	}

	require.Len(t, suspended, 1)
	require.Equal(t, pid.ID(), suspended[0].Address())
	require.NotZero(t, suspended[0].SuspendedAt())

	require.NoError(t, actorSystem.Stop(ctx))
}
