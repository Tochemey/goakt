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
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/datacenter"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/log"
	mocksremote "github.com/tochemey/goakt/v3/mocks/remote"
	"github.com/tochemey/goakt/v3/remote"
)

func TestTellGrainAcrossDataCenters(t *testing.T) {
	ctx := context.Background()
	grainID := &GrainIdentity{kind: "TestGrain", name: "test-grain"}
	message := &internalpb.RemoteTellRequest{}

	t.Run("returns ErrActorNotFound when datacenter controller is nil", func(t *testing.T) {
		sys := &actorSystem{
			logger: log.DiscardLogger,
		}
		sys.dataCenterController = nil

		err := sys.tellGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
	})

	t.Run("returns ErrActorNotFound when no datacenter records", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		}, remotingMock)

		err := sys.tellGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
	})

	t.Run("returns ErrActorNotFound when no active endpoints", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterDraining,
					Endpoints: []string{"127.0.0.1:9000"},
				},
			}, nil
		}, remotingMock)

		err := sys.tellGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
	})

	t.Run("returns ErrActorNotFound when all remote calls fail", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
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
			RemoteTellGrain(mock.Anything, "127.0.0.1", 9000, mock.MatchedBy(func(req *remote.GrainRequest) bool {
				return req.Name == grainID.Name() && req.Kind == grainID.Kind()
			}), mock.Anything).
			Return(errors.New("remote tell failed")).
			Once()

		err := sys.tellGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
	})

	t.Run("succeeds when one endpoint responds successfully", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
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
			RemoteTellGrain(mock.Anything, "127.0.0.1", 9000, mock.MatchedBy(func(req *remote.GrainRequest) bool {
				return req.Name == grainID.Name() && req.Kind == grainID.Kind()
			}), mock.Anything).
			Return(nil).
			Once()

		err := sys.tellGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.NoError(t, err)
	})

	t.Run("succeeds with first successful response from multiple endpoints", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"127.0.0.1:9000", "127.0.0.2:9000"},
				},
			}, nil
		}, remotingMock)

		// One fails, one succeeds
		remotingMock.EXPECT().
			RemoteTellGrain(mock.Anything, "127.0.0.1", 9000, mock.Anything, mock.Anything).
			Return(errors.New("failed")).
			Maybe()

		remotingMock.EXPECT().
			RemoteTellGrain(mock.Anything, "127.0.0.2", 9000, mock.Anything, mock.Anything).
			Return(nil).
			Maybe()

		err := sys.tellGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.NoError(t, err)
	})

	t.Run("queries multiple datacenters", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
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

		remotingMock.EXPECT().
			RemoteTellGrain(mock.Anything, "127.0.0.1", 9000, mock.Anything, mock.Anything).
			Return(errors.New("failed")).
			Maybe()

		remotingMock.EXPECT().
			RemoteTellGrain(mock.Anything, "127.0.0.2", 9000, mock.Anything, mock.Anything).
			Return(nil).
			Maybe()

		err := sys.tellGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.NoError(t, err)
	})

	t.Run("skips non-active datacenter records", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
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

		// Only the active DC should be queried
		remotingMock.EXPECT().
			RemoteTellGrain(mock.Anything, "127.0.0.2", 9000, mock.Anything, mock.Anything).
			Return(nil).
			Once()

		err := sys.tellGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.NoError(t, err)
	})

	t.Run("skips invalid endpoint formats", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"invalid-endpoint", "127.0.0.1:notaport", "127.0.0.2:9000"},
				},
			}, nil
		}, remotingMock)

		// Only valid endpoint should be queried
		remotingMock.EXPECT().
			RemoteTellGrain(mock.Anything, "127.0.0.2", 9000, mock.Anything, mock.Anything).
			Return(nil).
			Once()

		err := sys.tellGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.NoError(t, err)
	})

	t.Run("uses provided timeout when smaller than default", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
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
			RemoteTellGrain(mock.Anything, "127.0.0.1", 9000, mock.Anything, mock.Anything).
			Return(nil).
			Once()

		// Use a short timeout
		err := sys.tellGrainAcrossDataCenters(ctx, grainID, message, 100*time.Millisecond)
		require.NoError(t, err)
	})
}

func TestAskGrainAcrossDataCenters(t *testing.T) {
	ctx := context.Background()
	grainID := &GrainIdentity{kind: "TestGrain", name: "test-grain"}
	message := &internalpb.RemoteLookupRequest{}

	t.Run("returns ErrActorNotFound when datacenter controller is nil", func(t *testing.T) {
		sys := &actorSystem{
			logger: log.DiscardLogger,
		}
		sys.dataCenterController = nil

		resp, err := sys.askGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Nil(t, resp)
	})

	t.Run("returns ErrActorNotFound when no datacenter records", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		}, remotingMock)

		resp, err := sys.askGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Nil(t, resp)
	})

	t.Run("returns ErrActorNotFound when no active endpoints", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterInactive,
					Endpoints: []string{"127.0.0.1:9000"},
				},
			}, nil
		}, remotingMock)

		resp, err := sys.askGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Nil(t, resp)
	})

	t.Run("returns ErrActorNotFound when all remote calls fail", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
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
			RemoteAskGrain(mock.Anything, "127.0.0.1", 9000, mock.MatchedBy(func(req *remote.GrainRequest) bool {
				return req.Name == grainID.Name() && req.Kind == grainID.Kind()
			}), mock.Anything, mock.Anything).
			Return(nil, errors.New("remote ask failed")).
			Once()

		resp, err := sys.askGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Nil(t, resp)
	})

	t.Run("returns ErrActorNotFound when remote returns nil response", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
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
			RemoteAskGrain(mock.Anything, "127.0.0.1", 9000, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, nil).
			Once()

		resp, err := sys.askGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Nil(t, resp)
	})

	t.Run("succeeds when one endpoint responds successfully", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"127.0.0.1:9000"},
				},
			}, nil
		}, remotingMock)

		responseMsg := &internalpb.RemoteLookupResponse{Address: "test-address"}
		anyResp, _ := anypb.New(responseMsg)

		remotingMock.EXPECT().
			RemoteAskGrain(mock.Anything, "127.0.0.1", 9000, mock.MatchedBy(func(req *remote.GrainRequest) bool {
				return req.Name == grainID.Name() && req.Kind == grainID.Kind()
			}), mock.Anything, mock.Anything).
			Return(anyResp, nil).
			Once()

		resp, err := sys.askGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("succeeds with first successful response from multiple endpoints", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"127.0.0.1:9000", "127.0.0.2:9000"},
				},
			}, nil
		}, remotingMock)

		responseMsg := &internalpb.RemoteLookupResponse{Address: "test-address"}
		anyResp, _ := anypb.New(responseMsg)

		// One fails, one succeeds
		remotingMock.EXPECT().
			RemoteAskGrain(mock.Anything, "127.0.0.1", 9000, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("failed")).
			Maybe()

		remotingMock.EXPECT().
			RemoteAskGrain(mock.Anything, "127.0.0.2", 9000, mock.Anything, mock.Anything, mock.Anything).
			Return(anyResp, nil).
			Maybe()

		resp, err := sys.askGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("queries multiple datacenters", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
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

		responseMsg := &internalpb.RemoteLookupResponse{Address: "found"}
		anyResp, _ := anypb.New(responseMsg)

		remotingMock.EXPECT().
			RemoteAskGrain(mock.Anything, "127.0.0.1", 9000, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("failed")).
			Maybe()

		remotingMock.EXPECT().
			RemoteAskGrain(mock.Anything, "127.0.0.2", 9000, mock.Anything, mock.Anything, mock.Anything).
			Return(anyResp, nil).
			Maybe()

		resp, err := sys.askGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("skips non-active datacenter records", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
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

		responseMsg := &internalpb.RemoteLookupResponse{Address: "found"}
		anyResp, _ := anypb.New(responseMsg)

		// Only the active DC should be queried
		remotingMock.EXPECT().
			RemoteAskGrain(mock.Anything, "127.0.0.2", 9000, mock.Anything, mock.Anything, mock.Anything).
			Return(anyResp, nil).
			Once()

		resp, err := sys.askGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("skips invalid endpoint formats", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"invalid-endpoint", "127.0.0.1:notaport", "127.0.0.2:9000"},
				},
			}, nil
		}, remotingMock)

		responseMsg := &internalpb.RemoteLookupResponse{Address: "found"}
		anyResp, _ := anypb.New(responseMsg)

		// Only valid endpoint should be queried
		remotingMock.EXPECT().
			RemoteAskGrain(mock.Anything, "127.0.0.2", 9000, mock.Anything, mock.Anything, mock.Anything).
			Return(anyResp, nil).
			Once()

		resp, err := sys.askGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("uses provided timeout when smaller than default", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"127.0.0.1:9000"},
				},
			}, nil
		}, remotingMock)

		responseMsg := &internalpb.RemoteLookupResponse{Address: "found"}
		anyResp, _ := anypb.New(responseMsg)

		remotingMock.EXPECT().
			RemoteAskGrain(mock.Anything, "127.0.0.1", 9000, mock.Anything, mock.Anything, mock.Anything).
			Return(anyResp, nil).
			Once()

		// Use a short timeout
		resp, err := sys.askGrainAcrossDataCenters(ctx, grainID, message, 100*time.Millisecond)
		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("returns ErrActorNotFound when all endpoints have invalid format", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{
				{
					ID:        "dc-1",
					State:     datacenter.DataCenterActive,
					Endpoints: []string{"invalid", "also-invalid"},
				},
			}, nil
		}, remotingMock)

		resp, err := sys.askGrainAcrossDataCenters(ctx, grainID, message, time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrActorNotFound)
		assert.Nil(t, resp)
	})
}
