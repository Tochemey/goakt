/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package remote

import (
	"context"
	"crypto/tls"
	"fmt"
	nethttp "net/http"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
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

func TestRemoteLookup_NotFoundReturnsNoSender(t *testing.T) {
	r := NewRemoting()
	mockClient := &notFoundClient{}
	setClientFactory(t, r, func(string, int) internalpbconnect.RemotingServiceClient { return mockClient })

	addr, err := r.RemoteLookup(context.Background(), "host", 1000, "actor")
	assert.NoError(t, err)
	assert.True(t, addr.Equals(address.NoSender()))
}

func TestRemotingContextPropagatorInjectError(t *testing.T) {
	injectErr := fmt.Errorf("inject failure")
	prop := &failingPropagator{err: injectErr}
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
	askHeader       nethttp.Header
	tellHeader      nethttp.Header
	lookupHeader    nethttp.Header
	spawnHeader     nethttp.Header
	respawnHeader   nethttp.Header
	stopHeader      nethttp.Header
	reinstateHeader nethttp.Header
	lastTellReq     *internalpb.RemoteTellRequest
	lastAskReq      *internalpb.RemoteAskRequest
	batchResponses  []*anypb.Any
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
	return connect.NewResponse(&internalpb.RemoteLookupResponse{}), nil
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
	return connect.NewResponse(new(internalpb.RemoteSpawnResponse)), nil
}

func (x *mockRemotingServiceClient) RemoteReinstate(_ context.Context, req *connect.Request[internalpb.RemoteReinstateRequest]) (*connect.Response[internalpb.RemoteReinstateResponse], error) {
	x.reinstateHeader = req.Header().Clone()
	return connect.NewResponse(new(internalpb.RemoteReinstateResponse)), nil
}

func (x *mockRemotingServiceClient) RemoteAskGrain(context.Context, *connect.Request[internalpb.RemoteAskGrainRequest]) (*connect.Response[internalpb.RemoteAskGrainResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented"))
}

func (x *mockRemotingServiceClient) RemoteTellGrain(context.Context, *connect.Request[internalpb.RemoteTellGrainRequest]) (*connect.Response[internalpb.RemoteTellGrainResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented"))
}

func (x *mockRemotingServiceClient) RemoteActivateGrain(context.Context, *connect.Request[internalpb.RemoteActivateGrainRequest]) (*connect.Response[internalpb.RemoteActivateGrainResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("not implemented"))
}

type notFoundClient struct {
	internalpbconnect.UnimplementedRemotingServiceHandler
}

func (*notFoundClient) RemoteLookup(context.Context, *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error) {
	return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("not found"))
}

type failingPropagator struct {
	err error
}

func (f *failingPropagator) Inject(context.Context, nethttp.Header) error { return f.err }
func (f *failingPropagator) Extract(ctx context.Context, _ nethttp.Header) (context.Context, error) {
	return ctx, nil
}

// setClientFactory overrides the remoting client factory via reflection for testing without changing the public API.
func setClientFactory(t *testing.T, r Remoting, factory func(string, int) internalpbconnect.RemotingServiceClient) {
	t.Helper()
	rv := reflect.ValueOf(r)
	assert.Equal(t, reflect.Pointer, rv.Kind())
	elem := rv.Elem()
	field := elem.FieldByName("clientFactory")
	assert.True(t, field.IsValid())
	field = reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	field.Set(reflect.ValueOf(factory))
}

func mustAny(msg proto.Message) *anypb.Any {
	val, _ := anypb.New(msg)
	return val
}
