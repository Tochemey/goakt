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

package actor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/test/data/testpb"
)

func TestPassivation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	opts := []pidOption{
		withInitMaxRetries(1),
		withPassivationAfter(passivateAfter),
		withAskTimeout(askTimeout),
		withCustomLogger(log.DiscardLogger),
	}
	path := NewPath("test", NewAddress("system", "127.0.0.1", 1))
	pid, err := newPID(ctx, path, new(testActor), opts...)
	require.NoError(t, err)
	require.NotNil(t, pid)

	done := make(chan types.Unit)
	stop := make(chan struct{})
	go func() {
		select {
		case <-time.After(delay):
			done <- types.Unit{}
		case <-stop:
			return
		}
	}()

	<-done
	close(stop)
	err = Tell(ctx, pid, new(testpb.TestTell))
	assert.Error(t, err)
	assert.EqualError(t, err, ErrDead.Error())
}

func TestPassivation_WhenPostStopReturnsError_ReturnsNoError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	opts := []pidOption{
		withInitMaxRetries(1),
		withPassivationAfter(passivateAfter),
		withAskTimeout(askTimeout),
		withCustomLogger(log.DiscardLogger),
	}
	path := NewPath("test", NewAddress("system", "127.0.0.1", 1))
	pid, err := newPID(ctx, path, new(postStopActor), opts...)
	require.NoError(t, err)
	require.NotNil(t, pid)

	done := make(chan types.Unit)
	stop := make(chan struct{})
	go func() {
		select {
		case <-time.After(delay):
			done <- types.Unit{}
		case <-stop:
			return
		}
	}()

	<-done
	close(stop)
	err = Tell(ctx, pid, new(testpb.TestTell))
	assert.Error(t, err)
	assert.EqualError(t, err, ErrDead.Error())
}

func TestReceive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := NewPath("test", NewAddress("system", "127.0.0.1", 1))
	pid, err := newPID(ctx, path, new(testActor), withCustomLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, pid)

	count := 10
	for i := 0; i < count; i++ {
		receiveContext := &ReceiveContext{
			ctx:       ctx,
			message:   new(testpb.TestTell),
			sender:    NoSender,
			recipient: pid,
		}
		pid.doReceive(receiveContext)
	}

	t.Cleanup(func() {
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
}

func TestAsk(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := NewPath("test", NewAddress("system", "127.0.0.1", 1))
	pid, err := newPID(ctx, path, new(testActor), withCustomLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, pid)

	response, err := Ask(ctx, pid, new(testpb.TestAsk), askTimeout)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.True(t, proto.Equal(new(testpb.TestAsk), response))

	t.Cleanup(func() {
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
}

func TestAsk_WhenRequestTimesOut_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := NewPath("test", NewAddress("system", "127.0.0.1", 1))
	pid, err := newPID(ctx, path, new(testActor), withCustomLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, pid)

	response, err := Ask(ctx, pid, new(testpb.TestAskTimeout), askTimeout)
	require.Error(t, err)
	require.Nil(t, response)
	assert.EqualError(t, err, ErrRequestTimeout.Error())

	t.Cleanup(func() {
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
}

func TestAsk_WhenActorNotReady_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := NewPath("test", NewAddress("system", "127.0.0.1", 1))
	pid, err := newPID(ctx, path, new(testActor), withCustomLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, pid)

	err = pid.Shutdown(ctx)
	require.NoError(t, err)

	response, err := Ask(ctx, pid, new(testpb.TestAsk), askTimeout)
	require.Error(t, err)
	require.Nil(t, response)
	assert.EqualError(t, err, ErrDead.Error())
}

func TestRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := NewPath("test", NewAddress("system", "127.0.0.1", 1))
	pid, err := newPID(ctx, path, new(testActor), withCustomLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, pid)

	err = pid.Shutdown(ctx)
	require.NoError(t, err)

	// wait for shutdown to properly complete
	continueCh := make(chan types.Unit)
	timer := time.AfterFunc(500*time.Millisecond, func() {
		continueCh <- types.Unit{}
	})
	defer timer.Stop()

	<-continueCh

	// making sure the actor is dead
	response, err := Ask(ctx, pid, new(testpb.TestAsk), askTimeout)
	require.Error(t, err)
	require.Nil(t, response)
	assert.EqualError(t, err, ErrDead.Error())

	// restart the actor
	err = pid.Restart(ctx)
	require.NoError(t, err)
	require.True(t, pid.IsRunning())

	response, err = Ask(ctx, pid, new(testpb.TestAsk), time.Second)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.True(t, proto.Equal(new(testpb.TestAsk), response))

	t.Cleanup(func() {
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
}

func TestRestart_WhenPreStartReturnsError_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := NewPath("test", NewAddress("system", "127.0.0.1", 1))
	pid, err := newPID(ctx, path, new(preStartActor), withCustomLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, pid)

	// wait awhile for a proper start
	assert.True(t, pid.IsRunning())

	err = pid.Shutdown(ctx)
	require.NoError(t, err)

	// wait for shutdown to properly complete
	continueCh := make(chan types.Unit)
	timer := time.AfterFunc(500*time.Millisecond, func() {
		continueCh <- types.Unit{}
	})
	defer timer.Stop()

	<-continueCh

	// restart the actor
	err = pid.Restart(ctx)
	require.Error(t, err)
	require.False(t, pid.IsRunning())
}

func TestRestart_WhenPostStopReturnsError_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := NewPath("test", NewAddress("system", "127.0.0.1", 1))
	pid, err := newPID(ctx, path, new(postStopActor), withCustomLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, pid)

	// wait awhile for a proper start
	assert.True(t, pid.IsRunning())

	// restart the actor
	err = pid.Restart(ctx)
	require.Error(t, err)
	require.False(t, pid.IsRunning())
}

func TestTell(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := NewPath("test", NewAddress("system", "127.0.0.1", 1))
	pid, err := newPID(ctx, path, new(testActor), withCustomLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, pid)

	err = Tell(ctx, pid, new(testpb.TestTell))
	require.NoError(t, err)

	t.Cleanup(func() {
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
}

func TestTell_WhenActorNotReady_ReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := NewPath("test", NewAddress("system", "127.0.0.1", 1))
	pid, err := newPID(ctx, path, new(testActor), withCustomLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, pid)

	err = pid.Shutdown(ctx)
	require.NoError(t, err)

	err = Tell(ctx, pid, new(testpb.TestTell))
	require.Error(t, err)
	assert.EqualError(t, err, ErrDead.Error())
}

func TestTell_WhenMultiNodesRunning_HappyPath(t *testing.T) {
	t.Parallel()
	t.Skip("")
	ctx := context.Background()
	server := startNatsServer(t)

	node1, sd1 := startNode(t, "node1", server.Addr().String())
	node2, sd2 := startNode(t, "node2", server.Addr().String())

	actor1, err := node1.Spawn(ctx, "actor1", new(testActor))
	require.NoError(t, err)
	require.NotNil(t, actor1)

	actor2, err := node2.Spawn(ctx, "actor2", new(testActor))
	require.NoError(t, err)
	require.NotNil(t, actor2)

	err = actor1.Tell(ctx, actor2.Name(), new(testpb.TestTell))
	require.NoError(t, err)

	t.Cleanup(func() {
		assert.NoError(t, node1.Stop(ctx))
		assert.NoError(t, node2.Stop(ctx))
		assert.NoError(t, sd2.Close())
		assert.NoError(t, sd1.Close())
		server.Shutdown()
	})
}
