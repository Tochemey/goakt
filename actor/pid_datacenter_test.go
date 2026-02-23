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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/address"
	"github.com/tochemey/goakt/v4/datacenter"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/log"
	mocksremote "github.com/tochemey/goakt/v4/mocks/remote"
)

func TestDiscoverActor(t *testing.T) {
	ctx := context.Background()

	t.Run("returns ErrDead when PID is not running", func(t *testing.T) {
		pid := &PID{
			logger: log.DiscardLogger,
		}
		// PID is not running by default (state is 0)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrDead)
		assert.Nil(t, addr)
	})

	t.Run("returns ErrActorNotFound when actor system is nil", func(t *testing.T) {
		pid := &PID{
			actorSystem: nil,
			logger:      log.DiscardLogger,
		}
		pid.setState(runningState, true)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Nil(t, addr)
	})

	t.Run("returns ErrActorNotFound when datacenter controller is nil", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystemWithNilController(t, remotingMock)
		sys.dataCenterController = nil

		pid := &PID{
			actorSystem: sys,
			logger:      log.DiscardLogger,
			remoting:    remotingMock,
		}
		pid.setState(runningState, true)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Nil(t, addr)
	})

	t.Run("returns ErrActorNotFound when no active datacenter records", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		}, remotingMock)

		pid := &PID{
			actorSystem: sys,
			logger:      log.DiscardLogger,
			remoting:    remotingMock,
		}
		pid.setState(runningState, true)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Nil(t, addr)
	})

	t.Run("returns ErrActorNotFound when no active endpoints (records have non-active state)", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterDraining,
					Endpoints: []string{"127.0.0.1:9000"},
				},
				{
					ID:        "dc-2",
					State:     datacenter.DataCenterInactive,
					Endpoints: []string{"127.0.0.2:9000"},
				},
			}, nil
		}, remotingMock)

		pid := &PID{
			actorSystem: sys,
			logger:      log.DiscardLogger,
			remoting:    remotingMock,
		}
		pid.setState(runningState, true)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Nil(t, addr)
	})

	t.Run("returns ErrActorNotFound when all remote lookups fail", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"127.0.0.1:9000"},
				},
			}, nil
		}, remotingMock)

		remotingMock.EXPECT().
			RemoteLookup(mock.Anything, "127.0.0.1", 9000, "actor-1").
			Return(nil, errors.New("lookup failed")).
			Once()

		pid := &PID{
			actorSystem: sys,
			logger:      log.DiscardLogger,
			remoting:    remotingMock,
		}
		pid.setState(runningState, true)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Nil(t, addr)
	})

	t.Run("returns ErrActorNotFound when remote lookup returns NoSender", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"127.0.0.1:9000"},
				},
			}, nil
		}, remotingMock)

		remotingMock.EXPECT().
			RemoteLookup(mock.Anything, "127.0.0.1", 9000, "actor-1").
			Return(address.NoSender(), nil).
			Once()

		pid := &PID{
			actorSystem: sys,
			logger:      log.DiscardLogger,
			remoting:    remotingMock,
		}
		pid.setState(runningState, true)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Nil(t, addr)
	})

	t.Run("returns actor address when found in one of the datacenters", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"127.0.0.1:9000"},
				},
			}, nil
		}, remotingMock)

		foundAddr := address.New("actor-1", "system", "127.0.0.1", 9000)
		remotingMock.EXPECT().
			RemoteLookup(mock.Anything, "127.0.0.1", 9000, "actor-1").
			Return(foundAddr, nil).
			Once()

		pid := &PID{
			actorSystem: sys,
			logger:      log.DiscardLogger,
			remoting:    remotingMock,
		}
		pid.setState(runningState, true)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.NoError(t, err)
		assert.NotNil(t, addr)
		assert.Equal(t, foundAddr.String(), addr.Address().String())
	})

	t.Run("returns first successful result from multiple datacenters", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"127.0.0.1:9000"},
				},
				{
					ID:        "dc-2",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"127.0.0.2:9000"},
				},
			}, nil
		}, remotingMock)

		foundAddr := address.New("actor-1", "system", "127.0.0.2", 9000)
		remotingMock.EXPECT().
			RemoteLookup(mock.Anything, "127.0.0.1", 9000, "actor-1").
			Return(nil, errors.New("not found")).
			Maybe()

		remotingMock.EXPECT().
			RemoteLookup(mock.Anything, "127.0.0.2", 9000, "actor-1").
			Return(foundAddr, nil).
			Maybe()

		pid := &PID{
			actorSystem: sys,
			logger:      log.DiscardLogger,
			remoting:    remotingMock,
		}
		pid.setState(runningState, true)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.NoError(t, err)
		assert.NotNil(t, addr)
	})

	t.Run("queries multiple endpoints within a datacenter", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"127.0.0.1:9000", "127.0.0.1:9001"},
				},
			}, nil
		}, remotingMock)

		foundAddr := address.New("actor-1", "system", "127.0.0.1", 9001)
		remotingMock.EXPECT().
			RemoteLookup(mock.Anything, "127.0.0.1", 9000, "actor-1").
			Return(nil, errors.New("not found")).
			Maybe()

		remotingMock.EXPECT().
			RemoteLookup(mock.Anything, "127.0.0.1", 9001, "actor-1").
			Return(foundAddr, nil).
			Maybe()

		pid := &PID{
			actorSystem: sys,
			logger:      log.DiscardLogger,
			remoting:    remotingMock,
		}
		pid.setState(runningState, true)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.NoError(t, err)
		assert.NotNil(t, addr)
	})

	t.Run("skips invalid endpoint formats (no colon)", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"invalid-endpoint", "127.0.0.1:9000"},
				},
			}, nil
		}, remotingMock)

		foundAddr := address.New("actor-1", "system", "127.0.0.1", 9000)
		remotingMock.EXPECT().
			RemoteLookup(mock.Anything, "127.0.0.1", 9000, "actor-1").
			Return(foundAddr, nil).
			Once()

		pid := &PID{
			actorSystem: sys,
			logger:      log.DiscardLogger,
			remoting:    remotingMock,
		}
		pid.setState(runningState, true)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.NoError(t, err)
		assert.NotNil(t, addr)
	})

	t.Run("skips invalid endpoint formats (non-numeric port)", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"127.0.0.1:notaport", "127.0.0.2:9000"},
				},
			}, nil
		}, remotingMock)

		foundAddr := address.New("actor-1", "system", "127.0.0.2", 9000)
		remotingMock.EXPECT().
			RemoteLookup(mock.Anything, "127.0.0.2", 9000, "actor-1").
			Return(foundAddr, nil).
			Once()

		pid := &PID{
			actorSystem: sys,
			logger:      log.DiscardLogger,
			remoting:    remotingMock,
		}
		pid.setState(runningState, true)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.NoError(t, err)
		assert.NotNil(t, addr)
	})

	t.Run("returns ErrActorNotFound when all endpoints have invalid format", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"invalid", "also-invalid"},
				},
			}, nil
		}, remotingMock)

		pid := &PID{
			actorSystem: sys,
			logger:      log.DiscardLogger,
			remoting:    remotingMock,
		}
		pid.setState(runningState, true)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Nil(t, addr)
	})

	t.Run("skips non-active datacenter records", func(t *testing.T) {
		remotingMock := mocksremote.NewClient(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-draining",
					State:     datacenter.DataCenterDraining,
					Endpoints: []string{"127.0.0.1:9000"},
				},
				{
					ID:        "dc-active",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"127.0.0.2:9000"},
				},
			}, nil
		}, remotingMock)

		foundAddr := address.New("actor-1", "system", "127.0.0.2", 9000)
		// Only the active DC should be queried
		remotingMock.EXPECT().
			RemoteLookup(mock.Anything, "127.0.0.2", 9000, "actor-1").
			Return(foundAddr, nil).
			Once()

		pid := &PID{
			actorSystem: sys,
			logger:      log.DiscardLogger,
			remoting:    remotingMock,
		}
		pid.setState(runningState, true)

		addr, err := pid.DiscoverActor(ctx, "actor-1", time.Second)
		require.NoError(t, err)
		assert.NotNil(t, addr)
	})
}

// MockDatacenterSystemWithNilController creates a mock actor system without a datacenter controller.
func MockDatacenterSystemWithNilController(t *testing.T, remoting *mocksremote.Client) *actorSystem {
	t.Helper()
	sys := &actorSystem{
		logger:   log.DiscardLogger,
		remoting: remoting,
	}
	sys.started.Store(true)
	sys.remotingEnabled.Store(true)
	return sys
}
