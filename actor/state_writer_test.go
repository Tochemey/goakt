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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	internalcluster "github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/log"
	mockscluster "github.com/tochemey/goakt/v3/mocks/cluster"
)

func TestStateWriterReceive(t *testing.T) {
	t.Run("PersistPeerActor success", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		msg := &internalpb.PersistPeerActor{
			Actor: &internalpb.Actor{Type: "state-writer"},
		}

		receiveCtx := newStateWriterReceiveContext(t, clusterMock, msg)

		clusterMock.EXPECT().
			PersistPeerActor(mock.Anything, mock.Anything).
			Run(func(_ context.Context, actual *internalpb.PersistPeerActor) {
				require.Same(t, msg, actual)
			}).
			Return(nil).
			Once()

		writer := &stateWriter{}
		writer.Receive(receiveCtx)

		require.NoError(t, receiveCtx.getError())
		clusterMock.AssertExpectations(t)
	})

	t.Run("PersistPeerActor error", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		msg := &internalpb.PersistPeerActor{
			Actor: &internalpb.Actor{Type: "state-writer"},
		}

		receiveCtx := newStateWriterReceiveContext(t, clusterMock, msg)

		expectedErr := assert.AnError

		clusterMock.EXPECT().
			PersistPeerActor(mock.Anything, mock.Anything).
			Run(func(_ context.Context, actual *internalpb.PersistPeerActor) {
				require.Same(t, msg, actual)
			}).
			Return(expectedErr).
			Once()

		writer := &stateWriter{}
		writer.Receive(receiveCtx)

		require.ErrorIs(t, receiveCtx.getError(), expectedErr)
		clusterMock.AssertExpectations(t)
	})

	t.Run("PersistPeerGrain success", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		msg := &internalpb.PersistPeerGrain{
			Grain: &internalpb.Grain{
				GrainId: &internalpb.GrainId{Kind: "kind", Name: "grain", Value: "id"},
			},
		}

		receiveCtx := newStateWriterReceiveContext(t, clusterMock, msg)

		clusterMock.EXPECT().
			PersistPeerGrain(mock.Anything, mock.Anything).
			Run(func(_ context.Context, actual *internalpb.PersistPeerGrain) {
				require.Same(t, msg, actual)
			}).
			Return(nil).
			Once()

		writer := &stateWriter{}
		writer.Receive(receiveCtx)

		require.NoError(t, receiveCtx.getError())
		clusterMock.AssertExpectations(t)
	})

	t.Run("RemovePeerActor success", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		msg := &internalpb.RemovePeerActor{
			ActorName:   "actor-3",
			PeerAddress: "peer-address",
		}

		receiveCtx := newStateWriterReceiveContext(t, clusterMock, msg)

		clusterMock.EXPECT().
			RemovePeerActor(mock.Anything, msg.GetActorName(), msg.GetPeerAddress()).
			Return(nil).
			Once()

		writer := &stateWriter{}
		writer.Receive(receiveCtx)

		require.NoError(t, receiveCtx.getError())
		clusterMock.AssertExpectations(t)
	})

	t.Run("RemovePeerActor error", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		msg := &internalpb.RemovePeerActor{
			ActorName:   "actor-4",
			PeerAddress: "peer-address",
		}

		receiveCtx := newStateWriterReceiveContext(t, clusterMock, msg)

		expectedErr := assert.AnError

		clusterMock.EXPECT().
			RemovePeerActor(mock.Anything, msg.GetActorName(), msg.GetPeerAddress()).
			Return(expectedErr).
			Once()

		writer := &stateWriter{}
		writer.Receive(receiveCtx)

		require.ErrorIs(t, receiveCtx.getError(), expectedErr)
		clusterMock.AssertExpectations(t)
	})

	t.Run("RemovePeerGrain success", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		msg := &internalpb.RemovePeerGrain{
			GrainId:     &internalpb.GrainId{Kind: "kind", Name: "grain", Value: "id"},
			PeerAddress: "peer-address",
		}

		receiveCtx := newStateWriterReceiveContext(t, clusterMock, msg)

		clusterMock.EXPECT().
			RemovePeerGrain(mock.Anything, msg.GetGrainId(), msg.GetPeerAddress()).
			Return(nil).
			Once()

		writer := &stateWriter{}
		writer.Receive(receiveCtx)

		require.NoError(t, receiveCtx.getError())
		clusterMock.AssertExpectations(t)
	})
}

func newStateWriterReceiveContext(t *testing.T, cl internalcluster.Cluster, message proto.Message) *ReceiveContext {
	t.Helper()

	system, err := NewActorSystem("testStateWriter", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	actorSys := system.(*actorSystem)
	actorSys.locker.Lock()
	actorSys.cluster = cl
	actorSys.locker.Unlock()

	pid := &PID{
		fieldsLocker: &sync.RWMutex{},
		logger:       log.DiscardLogger,
		system:       actorSys,
	}

	return &ReceiveContext{
		ctx:     context.Background(),
		message: message,
		self:    pid,
	}
}
