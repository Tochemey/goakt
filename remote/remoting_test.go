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

package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math"
	nethttp "net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/olric"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v3/address"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/internalpb/internalpbconnect"
)

func TestRemotingOptionsAndDefaults(t *testing.T) {
	r := NewRemoting().(*remoting)

	assert.NotNil(t, r.HTTPClient())
	assert.Equal(t, DefaultMaxReadFrameSize, r.MaxReadFrameSize())
	assert.Equal(t, NoCompression, r.Compression())
	assert.Nil(t, r.TLSConfig())

	// ensure close does not panic
	r.Close()
}

// nolint
func TestRemotingOptionApplication(t *testing.T) {
	tlsCfg := &tls.Config{}
	r := NewRemoting(
		WithRemotingTLS(tlsCfg),
		WithRemotingMaxReadFameSize(1024),
		WithRemotingCompression(GzipCompression),
	).(*remoting)

	assert.Equal(t, tlsCfg, r.TLSConfig())
	assert.Equal(t, 1024, r.MaxReadFrameSize())
	assert.Equal(t, GzipCompression, r.Compression())

	// RemotingServiceClient should build without hitting network.
	client := r.RemotingServiceClient("localhost", 8080)
	assert.NotNil(t, client)
}

func TestRemotingClientFactory_NoOpOnNil(t *testing.T) {
	r := NewRemoting().(*remoting)
	mockClient := &mockRemotingServiceClient{}
	r.setClientFactory(func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })
	r.setClientFactory(nil)

	client := r.RemotingServiceClient("host", 1000)
	assert.Same(t, mockClient, client)
}

func TestRemotingHeaderPropagation(t *testing.T) {
	ctxKey := struct{}{}
	headerKey := "x-goakt-actor"
	headerValAsk := "ask-context"
	headerValTell := "tell-context"
	headerValLookup := "lookup-context"
	headerValSpawn := "spawn-context"
	headerValRespawn := "respawn-context"
	headerValStop := "stop-context"
	headerValReinstate := "reinstate-context"

	propagator := &testHeaderPropagator{key: ctxKey, header: headerKey}
	r := NewRemoting(WithRemotingContextPropagator(propagator)).(*remoting)
	mockClient := &mockRemotingServiceClient{}
	setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

	from := address.New("from", "sys", "127.0.0.1", 10000)
	to := address.New("to", "sys", "127.0.0.1", 10001)

	askCtx := context.WithValue(context.Background(), ctxKey, headerValAsk)
	_, err := r.RemoteAsk(askCtx, from, to, &internalpb.RemoteLookupRequest{}, time.Second)
	assert.NoError(t, err)
	assert.Equal(t, headerValAsk, mockClient.askHeader.Get(headerKey))

	tellCtx := context.WithValue(context.Background(), ctxKey, headerValTell)
	err = r.RemoteTell(tellCtx, from, to, &internalpb.RemoteTellRequest{})
	assert.NoError(t, err)
	assert.Equal(t, headerValTell, mockClient.tellHeader.Get(headerKey))

	lookupCtx := context.WithValue(context.Background(), ctxKey, headerValLookup)
	_, err = r.RemoteLookup(lookupCtx, "remote-host", 1234, "actor")
	assert.NoError(t, err)
	assert.Equal(t, headerValLookup, mockClient.lookupHeader.Get(headerKey))

	spawnCtx := context.WithValue(context.Background(), ctxKey, headerValSpawn)
	err = r.RemoteSpawn(spawnCtx, "remote-host", 1235, &SpawnRequest{Name: "name", Kind: "kind"})
	assert.NoError(t, err)
	assert.Equal(t, headerValSpawn, mockClient.spawnHeader.Get(headerKey))

	respawnCtx := context.WithValue(context.Background(), ctxKey, headerValRespawn)
	err = r.RemoteReSpawn(respawnCtx, "remote-host", 1236, "actor")
	assert.NoError(t, err)
	assert.Equal(t, headerValRespawn, mockClient.respawnHeader.Get(headerKey))

	stopCtx := context.WithValue(context.Background(), ctxKey, headerValStop)
	err = r.RemoteStop(stopCtx, "remote-host", 1237, "actor")
	assert.NoError(t, err)
	assert.Equal(t, headerValStop, mockClient.stopHeader.Get(headerKey))

	reinstateCtx := context.WithValue(context.Background(), ctxKey, headerValReinstate)
	err = r.RemoteReinstate(reinstateCtx, "remote-host", 1238, "actor")
	assert.NoError(t, err)
	assert.Equal(t, headerValReinstate, mockClient.reinstateHeader.Get(headerKey))
}

func TestRemoteBatchTell_FiltersNilMessagesAndInjectsHeader(t *testing.T) {
	ctxKey := struct{}{}
	headerKey := "x-goakt-actor"
	propagator := &testHeaderPropagator{key: ctxKey, header: headerKey}
	r := NewRemoting(WithRemotingContextPropagator(propagator))

	mockClient := &mockRemotingServiceClient{}
	setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)
	ctx := context.WithValue(context.Background(), ctxKey, "batched")

	err := r.RemoteBatchTell(ctx, from, to, []proto.Message{&internalpb.RemoteLookupRequest{}, nil, &internalpb.RemoteTellRequest{}})
	assert.NoError(t, err)
	assert.Len(t, mockClient.lastTellReq.GetRemoteMessages(), 2)
	assert.Equal(t, "batched", mockClient.tellHeader.Get(headerKey))
}

func TestRemoteBatchAsk_ReturnsResponses(t *testing.T) {
	ctxKey := struct{}{}
	headerKey := "x-goakt-actor"
	propagator := &testHeaderPropagator{key: ctxKey, header: headerKey}
	r := NewRemoting(WithRemotingContextPropagator(propagator))

	mockClient := &mockRemotingServiceClient{
		batchResponses: []*anypb.Any{
			mustAny(durationpb.New(time.Second)),
			mustAny(durationpb.New(2 * time.Second)),
		},
	}
	setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)
	ctx := context.WithValue(context.Background(), ctxKey, "batched-ask")

	responses, err := r.RemoteBatchAsk(ctx, from, to, []proto.Message{&internalpb.RemoteLookupRequest{}, &internalpb.RemoteTellRequest{}}, time.Second)
	assert.NoError(t, err)
	assert.Len(t, responses, 2)
	assert.Equal(t, "batched-ask", mockClient.askHeader.Get(headerKey))
}

func TestRemoteAsk_InvalidMessage(t *testing.T) {
	r := NewRemoting()
	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)

	_, err := r.RemoteAsk(context.Background(), from, to, nil, time.Second)
	assert.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRemoteTell_InvalidMessage(t *testing.T) {
	r := NewRemoting()
	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)

	err := r.RemoteTell(context.Background(), from, to, nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRemoteSpawn_InvalidRequest(t *testing.T) {
	r := NewRemoting()
	err := r.RemoteSpawn(context.Background(), "host", 1000, &SpawnRequest{})
	assert.Error(t, err)
}

func TestRemoteSpawn_InvalidPort(t *testing.T) {
	r := NewRemoting()
	port := int(math.MaxInt32) + 1

	err := r.RemoteSpawn(context.Background(), "host", port, &SpawnRequest{Name: "actor", Kind: "kind"})
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteSpawn_MapsReentrancyConfig(t *testing.T) {
	r := NewRemoting()
	mockClient := &mockRemotingServiceClient{}
	setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

	err := r.RemoteSpawn(context.Background(), "host", 1000, &SpawnRequest{
		Name: "actor",
		Kind: "kind",
		Reentrancy: &ReentrancyConfig{
			Mode:        ReentrancyStashNonReentrant,
			MaxInFlight: 5,
		},
	})
	require.NoError(t, err)
	require.NotNil(t, mockClient.lastSpawnReq)
	require.NotNil(t, mockClient.lastSpawnReq.GetReentrancy())
	assert.Equal(t, internalpb.ReentrancyMode_REENTRANCY_MODE_STASH_NON_REENTRANT, mockClient.lastSpawnReq.GetReentrancy().GetMode())
	assert.Equal(t, uint32(5), mockClient.lastSpawnReq.GetReentrancy().GetMaxInFlight())
}

func TestRemoteSpawn_MapsAlreadyExistsErrors(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantErr error
	}{
		{
			name:    "singleton already exists",
			err:     gerrors.ErrSingletonAlreadyExists,
			wantErr: gerrors.ErrSingletonAlreadyExists,
		},
		{
			name:    "actor already exists",
			err:     gerrors.ErrActorAlreadyExists,
			wantErr: gerrors.ErrActorAlreadyExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRemoting()
			mockClient := &mockRemotingServiceClient{
				spawnErr: connect.NewError(connect.CodeAlreadyExists, tt.err),
			}
			setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

			err := r.RemoteSpawn(context.Background(), "host", 1000, &SpawnRequest{Name: "actor", Kind: "kind"})
			assert.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestRemoteSpawn_MapsFailedPreconditionErrors(t *testing.T) {
	testCases := []struct {
		name    string
		err     error
		wantErr error
	}{
		{
			name:    "type not registered (underlying)",
			err:     gerrors.ErrTypeNotRegistered,
			wantErr: gerrors.ErrTypeNotRegistered,
		},
		{
			name:    "type not registered (message)",
			err:     errors.New(gerrors.ErrTypeNotRegistered.Error()),
			wantErr: gerrors.ErrTypeNotRegistered,
		},
		{
			name:    "remoting disabled (underlying)",
			err:     gerrors.ErrRemotingDisabled,
			wantErr: gerrors.ErrRemotingDisabled,
		},
		{
			name:    "remoting disabled (message)",
			err:     errors.New(gerrors.ErrRemotingDisabled.Error()),
			wantErr: gerrors.ErrRemotingDisabled,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := NewRemoting()
			mockClient := &mockRemotingServiceClient{
				spawnErr: connect.NewError(connect.CodeFailedPrecondition, testCase.err),
			}
			setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

			err := r.RemoteSpawn(context.Background(), "host", 1000, &SpawnRequest{Name: "actor", Kind: "kind"})
			assert.ErrorIs(t, err, testCase.wantErr)
		})
	}
}

func TestRemoteSpawn_PassesThroughTransportErrors(t *testing.T) {
	r := NewRemoting()
	mockClient := &mockRemotingServiceClient{
		spawnErr: context.DeadlineExceeded,
	}
	setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

	err := r.RemoteSpawn(context.Background(), "host", 1000, &SpawnRequest{Name: "actor", Kind: "kind"})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRemoteSpawn_PassesThroughUnmappedConnectErrors(t *testing.T) {
	testCases := []struct {
		name       string
		code       connect.Code
		underlying error
		wantMsg    string
	}{
		{
			name:       "invalid spawn request",
			code:       connect.CodeInvalidArgument,
			underlying: errors.New("invalid spawn request payload"),
			wantMsg:    "invalid spawn request payload",
		},
		{
			name:       "failed precondition with unrelated error",
			code:       connect.CodeFailedPrecondition,
			underlying: errors.New("precondition failed: invalid host"),
			wantMsg:    "invalid host",
		},
		{
			name:       "already exists with generic resource",
			code:       connect.CodeAlreadyExists,
			underlying: errors.New("resource already exists"),
			wantMsg:    "resource already exists",
		},
		{
			name:       "unavailable due to transport",
			code:       connect.CodeUnavailable,
			underlying: errors.New("connection refused"),
			wantMsg:    "connection refused",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := NewRemoting()
			mockClient := &mockRemotingServiceClient{
				spawnErr: connect.NewError(testCase.code, testCase.underlying),
			}
			setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

			err := r.RemoteSpawn(context.Background(), "host", 1000, &SpawnRequest{Name: "actor", Kind: "kind"})
			assert.Equal(t, testCase.code, connect.CodeOf(err))
			assert.ErrorContains(t, err, testCase.wantMsg)
		})
	}
}

func TestRemoteSpawn_MapsQuorumErrors(t *testing.T) {
	testCases := []struct {
		name    string
		err     error
		wantErr error
	}{
		{
			name:    "write quorum",
			err:     olric.ErrWriteQuorum,
			wantErr: gerrors.ErrWriteQuorum,
		},
		{
			name:    "read quorum",
			err:     olric.ErrReadQuorum,
			wantErr: gerrors.ErrReadQuorum,
		},
		{
			name:    "cluster quorum",
			err:     olric.ErrClusterQuorum,
			wantErr: gerrors.ErrClusterQuorum,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			r := NewRemoting()
			mockClient := &mockRemotingServiceClient{
				spawnErr: connect.NewError(connect.CodeUnavailable, testCase.err),
			}
			setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

			err := r.RemoteSpawn(context.Background(), "host", 1000, &SpawnRequest{Name: "actor", Kind: "kind"})
			assert.ErrorIs(t, err, testCase.wantErr)
		})
	}
}

func TestRemoteLookup_NotFoundReturnsNoSender(t *testing.T) {
	r := NewRemoting()
	mockClient := &notFoundClient{}
	setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

	addr, err := r.RemoteLookup(context.Background(), "host", 1000, "actor")
	assert.NoError(t, err)
	assert.True(t, addr.Equals(address.NoSender()))
}

func TestRemoteLookup_InvalidAddress(t *testing.T) {
	r := NewRemoting()
	mockClient := &mockRemotingServiceClient{lookupAddress: "invalid-address"}
	setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

	addr, err := r.RemoteLookup(context.Background(), "host", 1000, "actor")
	assert.Error(t, err)
	assert.Nil(t, addr)
	assert.ErrorContains(t, err, "address format is invalid")
}

func TestRemoteLookup_InvalidPort(t *testing.T) {
	r := NewRemoting()
	port := int(math.MaxInt32) + 1

	addr, err := r.RemoteLookup(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.Nil(t, addr)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemotingContextPropagatorInjectError(t *testing.T) {
	injectErr := fmt.Errorf("inject failure")
	prop := &mockContextPropagator{err: injectErr}
	r := NewRemoting(WithRemotingContextPropagator(prop))
	// Use a mockClient client to avoid unexpected network calls if inject succeeds (it should not).
	mockClient := &mockRemotingServiceClient{}
	setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)
	ctx := context.Background()

	testCases := []struct {
		name string
		call func() error
	}{
		{
			name: "RemoteTell",
			call: func() error { return r.RemoteTell(ctx, from, to, &internalpb.RemoteTellRequest{}) },
		},
		{
			name: "RemoteAsk",
			call: func() error {
				_, err := r.RemoteAsk(ctx, from, to, &internalpb.RemoteLookupRequest{}, time.Second)
				return err
			},
		},
		{
			name: "RemoteBatchTell",
			call: func() error {
				return r.RemoteBatchTell(ctx, from, to, []proto.Message{&internalpb.RemoteTellRequest{}})
			},
		},
		{
			name: "RemoteBatchAsk",
			call: func() error {
				_, err := r.RemoteBatchAsk(ctx, from, to, []proto.Message{&internalpb.RemoteLookupRequest{}}, time.Second)
				return err
			},
		},
		{
			name: "RemoteLookup",
			call: func() error {
				_, err := r.RemoteLookup(ctx, "host", 1000, "actor")
				return err
			},
		},
		{
			name: "RemoteSpawn",
			call: func() error {
				return r.RemoteSpawn(ctx, "host", 1000, &SpawnRequest{Name: "actor", Kind: "kind"})
			},
		},
		{
			name: "RemoteReSpawn",
			call: func() error {
				return r.RemoteReSpawn(ctx, "host", 1000, "actor")
			},
		},
		{
			name: "RemoteStop",
			call: func() error {
				return r.RemoteStop(ctx, "host", 1000, "actor")
			},
		},
		{
			name: "RemoteReinstate",
			call: func() error {
				return r.RemoteReinstate(ctx, "host", 1000, "actor")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			assert.ErrorIs(t, err, injectErr)
		})
	}
}

func TestRemoteReSpawn_InvalidPort(t *testing.T) {
	r := NewRemoting()
	port := int(math.MaxInt32) + 1

	err := r.RemoteReSpawn(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteStop_InvalidPort(t *testing.T) {
	r := NewRemoting()
	port := int(math.MaxInt32) + 1

	err := r.RemoteStop(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteReinstate_InvalidPort(t *testing.T) {
	r := NewRemoting()
	port := int(math.MaxInt32) + 1

	err := r.RemoteReinstate(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoting_RemoteActivateGrain(t *testing.T) {
	t.Run("returns error for invalid request", func(t *testing.T) {
		r := NewRemoting()
		err := r.RemoteActivateGrain(context.Background(), "host", 1000, &GrainRequest{})
		assert.Error(t, err)
		assert.ErrorContains(t, err, "invalid grain request")
	})

	t.Run("returns error when inject fails", func(t *testing.T) {
		injectErr := fmt.Errorf("inject failure")
		r := NewRemoting(WithRemotingContextPropagator(&mockContextPropagator{err: injectErr}))
		mockClient := &mockRemotingServiceClient{}
		setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

		err := r.RemoteActivateGrain(context.Background(), "host", 1000, &GrainRequest{Name: "grain", Kind: "kind"})
		assert.ErrorIs(t, err, injectErr)
	})

	t.Run("returns error when client fails", func(t *testing.T) {
		expectedErr := fmt.Errorf("activate failed")
		mockClient := &mockRemotingServiceClient{activateGrainErr: expectedErr}
		r := NewRemoting()
		setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

		err := r.RemoteActivateGrain(context.Background(), "host", 1000, &GrainRequest{Name: "grain", Kind: "kind"})
		assert.ErrorIs(t, err, expectedErr)
	})

	t.Run("sends sanitized request", func(t *testing.T) {
		ctxKey := struct{}{}
		headerKey := "x-goakt-propagated"
		headerVal := "activate-context"

		ctx := context.WithValue(context.Background(), ctxKey, headerVal)
		mockClient := &mockRemotingServiceClient{}
		r := NewRemoting(WithRemotingContextPropagator(&testHeaderPropagator{key: ctxKey, header: headerKey}))
		setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

		request := &GrainRequest{
			Name:              " grain ",
			Kind:              " kind ",
			ActivationTimeout: 0,
			ActivationRetries: 0,
		}
		err := r.RemoteActivateGrain(ctx, "host", 1000, request)
		assert.NoError(t, err)
		assert.Equal(t, headerVal, mockClient.activateGrainHeader.Get(headerKey))

		grain := mockClient.lastActivateGrainReq.GetGrain()
		assert.NotNil(t, grain)
		assert.Equal(t, "host", grain.GetHost())
		assert.Equal(t, int32(1000), grain.GetPort())
		assert.Equal(t, "grain", grain.GetGrainId().GetName())
		assert.Equal(t, "kind", grain.GetGrainId().GetKind())
		assert.Equal(t, "kind/grain", grain.GetGrainId().GetValue())
		assert.Equal(t, int32(5), grain.GetActivationRetries())
		assert.Equal(t, time.Second, grain.GetActivationTimeout().AsDuration())
	})
}

func TestRemoting_RemoteTellGrain(t *testing.T) {
	t.Run("returns error for invalid request", func(t *testing.T) {
		r := NewRemoting()
		err := r.RemoteTellGrain(context.Background(), "host", 1000, &GrainRequest{}, &internalpb.RemoteTellRequest{})
		assert.Error(t, err)
		assert.ErrorContains(t, err, "invalid grain request")
	})

	t.Run("returns error for invalid message", func(t *testing.T) {
		r := NewRemoting()
		err := r.RemoteTellGrain(context.Background(), "host", 1000, &GrainRequest{Name: "grain", Kind: "kind"}, nil)
		assert.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("returns error when inject fails", func(t *testing.T) {
		injectErr := fmt.Errorf("inject failure")
		r := NewRemoting(WithRemotingContextPropagator(&mockContextPropagator{err: injectErr}))
		mockClient := &mockRemotingServiceClient{}
		setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

		err := r.RemoteTellGrain(context.Background(), "host", 1000, &GrainRequest{Name: "grain", Kind: "kind"}, &internalpb.RemoteTellRequest{})
		assert.ErrorIs(t, err, injectErr)
	})

	t.Run("returns error when client fails", func(t *testing.T) {
		expectedErr := fmt.Errorf("tell failed")
		mockClient := &mockRemotingServiceClient{tellGrainErr: expectedErr}
		r := NewRemoting()
		setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

		err := r.RemoteTellGrain(context.Background(), "host", 1000, &GrainRequest{Name: "grain", Kind: "kind"}, &internalpb.RemoteTellRequest{})
		assert.ErrorIs(t, err, expectedErr)
	})

	t.Run("injects headers and sends request", func(t *testing.T) {
		ctxKey := struct{}{}
		headerKey := "x-goakt-propagated"
		headerVal := "tell-context"

		ctx := context.WithValue(context.Background(), ctxKey, headerVal)
		mockClient := &mockRemotingServiceClient{}
		r := NewRemoting(WithRemotingContextPropagator(&testHeaderPropagator{key: ctxKey, header: headerKey}))
		setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

		err := r.RemoteTellGrain(ctx, "host", 1000, &GrainRequest{Name: "grain", Kind: "kind"}, &internalpb.RemoteTellRequest{})
		assert.NoError(t, err)
		assert.Equal(t, headerVal, mockClient.tellGrainHeader.Get(headerKey))
	})
}

func TestRemoting_RemoteAskGrain(t *testing.T) {
	t.Run("returns error for invalid request", func(t *testing.T) {
		r := NewRemoting()
		_, err := r.RemoteAskGrain(context.Background(), "host", 1000, &GrainRequest{}, &internalpb.RemoteLookupRequest{}, time.Second)
		assert.Error(t, err)
		assert.ErrorContains(t, err, "invalid grain request")
	})

	t.Run("returns error for invalid message", func(t *testing.T) {
		r := NewRemoting()
		_, err := r.RemoteAskGrain(context.Background(), "host", 1000, &GrainRequest{Name: "grain", Kind: "kind"}, nil, time.Second)
		assert.Error(t, err)
		assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
	})

	t.Run("returns error when inject fails", func(t *testing.T) {
		injectErr := fmt.Errorf("inject failure")
		r := NewRemoting(WithRemotingContextPropagator(&mockContextPropagator{err: injectErr}))
		mockClient := &mockRemotingServiceClient{askGrainResponse: durationpb.New(time.Second)}
		setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

		_, err := r.RemoteAskGrain(context.Background(), "host", 1000, &GrainRequest{Name: "grain", Kind: "kind"}, &internalpb.RemoteLookupRequest{}, time.Second)
		assert.ErrorIs(t, err, injectErr)
	})

	t.Run("returns error when client fails", func(t *testing.T) {
		expectedErr := fmt.Errorf("ask failed")
		mockClient := &mockRemotingServiceClient{askGrainErr: expectedErr}
		r := NewRemoting()
		setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

		_, err := r.RemoteAskGrain(context.Background(), "host", 1000, &GrainRequest{Name: "grain", Kind: "kind"}, &internalpb.RemoteLookupRequest{}, time.Second)
		assert.ErrorIs(t, err, expectedErr)
	})

	t.Run("returns response and injects headers", func(t *testing.T) {
		ctxKey := struct{}{}
		headerKey := "x-goakt-propagated"
		headerVal := "ask-context"

		ctx := context.WithValue(context.Background(), ctxKey, headerVal)
		mockClient := &mockRemotingServiceClient{askGrainResponse: durationpb.New(2 * time.Second)}
		r := NewRemoting(WithRemotingContextPropagator(&testHeaderPropagator{key: ctxKey, header: headerKey}))
		setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

		resp, err := r.RemoteAskGrain(ctx, "host", 1000, &GrainRequest{Name: "grain", Kind: "kind"}, &internalpb.RemoteLookupRequest{}, time.Second)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, headerVal, mockClient.askGrainHeader.Get(headerKey))

		actual := new(durationpb.Duration)
		assert.NoError(t, resp.UnmarshalTo(actual))
		assert.Equal(t, 2*time.Second, actual.AsDuration())
	})

	t.Run("returns nil when response is nil", func(t *testing.T) {
		mockClient := &mockRemotingServiceClient{returnNilAskGrainResponse: true}
		r := NewRemoting()
		setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

		resp, err := r.RemoteAskGrain(context.Background(), "host", 1000, &GrainRequest{Name: "grain", Kind: "kind"}, &internalpb.RemoteLookupRequest{}, time.Second)
		assert.NoError(t, err)
		assert.Nil(t, resp)
	})
}

type testHeaderPropagator struct {
	key    any
	header string
}

func (p *testHeaderPropagator) Inject(ctx context.Context, headers nethttp.Header) error {
	if val := ctx.Value(p.key); val != nil {
		headers.Set(p.header, val.(string))
	}
	return nil
}

func (p *testHeaderPropagator) Extract(ctx context.Context, headers nethttp.Header) (context.Context, error) {
	if val := headers.Get(p.header); val != "" {
		ctx = context.WithValue(ctx, p.key, val)
	}
	return ctx, nil
}

type mockRemotingServiceClient struct {
	internalpbconnect.UnimplementedRemotingServiceHandler
	askHeader                 nethttp.Header
	tellHeader                nethttp.Header
	lookupHeader              nethttp.Header
	spawnHeader               nethttp.Header
	respawnHeader             nethttp.Header
	stopHeader                nethttp.Header
	reinstateHeader           nethttp.Header
	askGrainHeader            nethttp.Header
	tellGrainHeader           nethttp.Header
	activateGrainHeader       nethttp.Header
	lastTellReq               *internalpb.RemoteTellRequest
	lastAskReq                *internalpb.RemoteAskRequest
	lastSpawnReq              *internalpb.RemoteSpawnRequest
	lastAskGrainReq           *internalpb.RemoteAskGrainRequest
	lastTellGrainReq          *internalpb.RemoteTellGrainRequest
	lastActivateGrainReq      *internalpb.RemoteActivateGrainRequest
	batchResponses            []*anypb.Any
	lookupAddress             string
	askGrainResponse          proto.Message
	askGrainErr               error
	tellGrainErr              error
	activateGrainErr          error
	returnNilAskGrainResponse bool
	spawnErr                  error
}

func (x *mockRemotingServiceClient) RemoteAsk(_ context.Context, req *connect.Request[internalpb.RemoteAskRequest]) (*connect.Response[internalpb.RemoteAskResponse], error) {
	x.askHeader = req.Header().Clone()
	x.lastAskReq = req.Msg
	if x.batchResponses != nil {
		return connect.NewResponse(&internalpb.RemoteAskResponse{Messages: x.batchResponses}), nil
	}
	msg, _ := anypb.New(durationpb.New(time.Second))
	return connect.NewResponse(&internalpb.RemoteAskResponse{Messages: []*anypb.Any{msg}}), nil
}

func (x *mockRemotingServiceClient) RemoteTell(_ context.Context, req *connect.Request[internalpb.RemoteTellRequest]) (*connect.Response[internalpb.RemoteTellResponse], error) {
	x.tellHeader = req.Header().Clone()
	x.lastTellReq = req.Msg
	return connect.NewResponse(new(internalpb.RemoteTellResponse)), nil
}

// Unimplemented methods to satisfy the interface.
func (x *mockRemotingServiceClient) RemoteLookup(_ context.Context, req *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error) {
	x.lookupHeader = req.Header().Clone()
	msg := req.Msg
	addr := x.lookupAddress
	if addr == "" {
		addr = address.New(msg.GetName(), "sys", msg.GetHost(), int(msg.GetPort())).String()
	}
	return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: addr}), nil
}

func (x *mockRemotingServiceClient) RemoteReSpawn(_ context.Context, req *connect.Request[internalpb.RemoteReSpawnRequest]) (*connect.Response[internalpb.RemoteReSpawnResponse], error) {
	x.respawnHeader = req.Header().Clone()
	return connect.NewResponse(new(internalpb.RemoteReSpawnResponse)), nil
}

func (x *mockRemotingServiceClient) RemoteStop(_ context.Context, req *connect.Request[internalpb.RemoteStopRequest]) (*connect.Response[internalpb.RemoteStopResponse], error) {
	x.stopHeader = req.Header().Clone()
	return connect.NewResponse(new(internalpb.RemoteStopResponse)), nil
}

func (x *mockRemotingServiceClient) RemoteSpawn(_ context.Context, req *connect.Request[internalpb.RemoteSpawnRequest]) (*connect.Response[internalpb.RemoteSpawnResponse], error) {
	x.spawnHeader = req.Header().Clone()
	x.lastSpawnReq = req.Msg
	if x.spawnErr != nil {
		return nil, x.spawnErr
	}
	return connect.NewResponse(new(internalpb.RemoteSpawnResponse)), nil
}

func (x *mockRemotingServiceClient) RemoteReinstate(_ context.Context, req *connect.Request[internalpb.RemoteReinstateRequest]) (*connect.Response[internalpb.RemoteReinstateResponse], error) {
	x.reinstateHeader = req.Header().Clone()
	return connect.NewResponse(new(internalpb.RemoteReinstateResponse)), nil
}

func (x *mockRemotingServiceClient) RemoteAskGrain(_ context.Context, req *connect.Request[internalpb.RemoteAskGrainRequest]) (*connect.Response[internalpb.RemoteAskGrainResponse], error) {
	x.askGrainHeader = req.Header().Clone()
	x.lastAskGrainReq = req.Msg
	if x.askGrainErr != nil {
		return nil, x.askGrainErr
	}
	if x.returnNilAskGrainResponse {
		return nil, nil
	}
	response := x.askGrainResponse
	if response == nil {
		response = durationpb.New(time.Second)
	}
	msg, _ := anypb.New(response)
	return connect.NewResponse(&internalpb.RemoteAskGrainResponse{Message: msg}), nil
}

func (x *mockRemotingServiceClient) RemoteTellGrain(_ context.Context, req *connect.Request[internalpb.RemoteTellGrainRequest]) (*connect.Response[internalpb.RemoteTellGrainResponse], error) {
	x.tellGrainHeader = req.Header().Clone()
	x.lastTellGrainReq = req.Msg
	if x.tellGrainErr != nil {
		return nil, x.tellGrainErr
	}
	return connect.NewResponse(new(internalpb.RemoteTellGrainResponse)), nil
}

func (x *mockRemotingServiceClient) RemoteActivateGrain(_ context.Context, req *connect.Request[internalpb.RemoteActivateGrainRequest]) (*connect.Response[internalpb.RemoteActivateGrainResponse], error) {
	x.activateGrainHeader = req.Header().Clone()
	x.lastActivateGrainReq = req.Msg
	if x.activateGrainErr != nil {
		return nil, x.activateGrainErr
	}
	return connect.NewResponse(new(internalpb.RemoteActivateGrainResponse)), nil
}

type notFoundClient struct {
	internalpbconnect.UnimplementedRemotingServiceHandler
}

func (*notFoundClient) RemoteLookup(context.Context, *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error) {
	return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("not found"))
}

type mockContextPropagator struct {
	err error
}

func (x *mockContextPropagator) Inject(context.Context, nethttp.Header) error { return x.err }
func (x *mockContextPropagator) Extract(ctx context.Context, _ nethttp.Header) (context.Context, error) {
	return ctx, nil
}

// setClientFactory overrides the remoting client factory via reflection for testing without changing the public API.
func setClientFactory(t *testing.T, r Remoting, factory func(string, int) internalpbconnect.RemotingServiceClient) {
	t.Helper()
	impl, ok := r.(*remoting)
	assert.True(t, ok)
	impl.setClientFactory(factory)
}

func mustAny(msg proto.Message) *anypb.Any {
	val, _ := anypb.New(msg)
	return val
}
