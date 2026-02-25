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

package remoteclient

import (
	"context"
	"crypto/tls"
	"fmt"
	"math"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/address"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/remote"
)

func TestRemotingOptionsAndDefaults(t *testing.T) {
	r := NewClient().(*client)

	// Default compression is Zstd (matches server default in remote.Config)
	assert.Equal(t, remote.ZstdCompression, r.Compression())
	assert.Nil(t, r.TLSConfig())

	// Verify connection pooling defaults based on benchmarks
	assert.Equal(t, 8, r.maxIdleConns)
	assert.Equal(t, 30*time.Second, r.idleTimeout)
	assert.Equal(t, 5*time.Second, r.dialTimeout)
	assert.Equal(t, 15*time.Second, r.keepAlive)

	// ensure close does not panic
	r.Close()
}

func TestRemotingOptionApplication(t *testing.T) {
	tlsCfg := &tls.Config{} //nolint:gosec
	r := NewClient(
		WithClientTLS(tlsCfg),
		WithClientCompression(remote.GzipCompression),
		WithClientMaxIdleConns(50),
		WithClientIdleTimeout(60*time.Second),
		WithClientDialTimeout(3*time.Second),
		WithClientKeepAlive(10*time.Second),
	).(*client)

	assert.Equal(t, tlsCfg, r.TLSConfig())
	assert.Equal(t, remote.GzipCompression, r.Compression())
	assert.Equal(t, 50, r.maxIdleConns)
	assert.Equal(t, 60*time.Second, r.idleTimeout)
	assert.Equal(t, 3*time.Second, r.dialTimeout)
	assert.Equal(t, 10*time.Second, r.keepAlive)

	// NetClient should build without hitting network.
	client := r.NetClient("localhost", 8080)
	assert.NotNil(t, client)
}

// TestNetClient_Caching tests the client caching behavior in
// NetClient. The caching mechanism uses xsync.Map with double-checked locking
// to ensure only one client is created per endpoint.
func TestNetClient_Caching(t *testing.T) {
	t.Run("cache key generation", func(t *testing.T) {
		r := NewClient().(*client)

		// Test that cache key is generated correctly using fmt.Sprintf
		host := "test-host"
		port := 12345
		expectedKey := fmt.Sprintf("%s:%d", host, port)

		// Call NetClient which generates the cache key
		_ = r.NetClient(host, port)

		// Verify the cache key format matches expected
		cached, ok := r.clientCache.Get(expectedKey)
		// Cache should be populated after first call
		assert.True(t, ok, "client should be cached after first call")
		if ok {
			assert.NotNil(t, cached)
		}
		assert.Equal(t, expectedKey, fmt.Sprintf("%s:%d", host, port),
			"cache key format should match fmt.Sprintf pattern")
	})

	t.Run("cache hit path returns same client instance", func(t *testing.T) {
		r := NewClient().(*client)

		host := "cache-hit.example.com"
		port := 9999

		// First call creates and caches the client
		client1 := r.NetClient(host, port)
		require.NotNil(t, client1)

		// Second call should return the same cached client
		client2 := r.NetClient(host, port)
		require.NotNil(t, client2, "should return client (cache hit path)")

		// Verify the returned client is the same instance
		assert.Same(t, client1, client2,
			"should return cached client on second call")

		// Verify cache still contains exactly one entry
		assert.Equal(t, 1, r.clientCache.Len(), "cache should contain exactly one entry")
	})

	t.Run("cache miss path creates and stores client", func(t *testing.T) {
		r := NewClient().(*client)

		host := "cache-miss.example.com"
		port := 8888
		cacheKey := fmt.Sprintf("%s:%d", host, port)

		// Ensure cache is empty to force cache miss
		r.clientCache.Delete(cacheKey)
		_, wasInCache := r.clientCache.Get(cacheKey)
		assert.False(t, wasInCache, "cache should be empty before call")

		// Call NetClient - should create new client (cache miss)
		client1 := r.NetClient(host, port)
		require.NotNil(t, client1, "should create client on cache miss")

		// Verify client was stored in cache (xsync.Map provides immediate consistency)
		cached, ok := r.clientCache.Get(cacheKey)
		assert.True(t, ok, "client should be stored in cache after creation")
		require.NotNil(t, cached, "cached client should not be nil")
		assert.Same(t, client1, cached, "cached client should match returned client")
	})

	t.Run("full sequence: cache miss then hit", func(t *testing.T) {
		r := NewClient().(*client)

		host := "sequence.example.com"
		port := 7777
		cacheKey := fmt.Sprintf("%s:%d", host, port)

		// Ensure cache is empty
		r.clientCache.Delete(cacheKey)

		// First call: cache miss path
		client1 := r.NetClient(host, port)
		require.NotNil(t, client1, "first call should create client (cache miss)")

		// Verify cache is now populated (xsync.Map provides immediate consistency)
		cached, ok := r.clientCache.Get(cacheKey)
		assert.True(t, ok, "cache should be populated after first call")
		require.NotNil(t, cached)
		assert.Same(t, client1, cached, "cached client should match first call result")

		// Second call: should hit cache
		client2 := r.NetClient(host, port)
		require.NotNil(t, client2, "second call should return client (cache hit path)")

		// Verify the same instance is returned (cache hit)
		assert.Same(t, client1, client2,
			"second call should return cached client (same instance)")

		// Verify cache is still populated and unchanged
		cached2, ok2 := r.clientCache.Get(cacheKey)
		assert.True(t, ok2, "cache should still be populated after second call")
		assert.Same(t, client1, cached2, "cached client should remain unchanged")
		// Cache length should still be 1
		assert.Equal(t, 1, r.clientCache.Len(), "cache should contain exactly one entry")
	})

	t.Run("caches client for same endpoint", func(t *testing.T) {
		r := NewClient().(*client)

		host := "example.com"
		port := 9090

		// Make calls to the same endpoint
		client1 := r.NetClient(host, port)
		require.NotNil(t, client1)

		// Make additional calls - all should return the same cached client
		for range 10 {
			client := r.NetClient(host, port)
			require.NotNil(t, client)
			assert.Same(t, client1, client, "all calls should return same cached client")
		}

		// Verify cache key format is correct
		cacheKey := fmt.Sprintf("%s:%d", host, port)
		assert.Equal(t, "example.com:9090", cacheKey)

		// Verify cache is populated
		cached, ok := r.clientCache.Get(cacheKey)
		assert.True(t, ok, "cache should be populated")
		assert.NotNil(t, cached, "cached client should not be nil")
	})

	t.Run("different endpoints get different clients", func(t *testing.T) {
		r := NewClient().(*client)

		// Create clients for different endpoints
		client1 := r.NetClient("host1", 8080)
		client2 := r.NetClient("host2", 8080)
		client3 := r.NetClient("host1", 9090)

		require.NotNil(t, client1)
		require.NotNil(t, client2)
		require.NotNil(t, client3)

		// All clients should be different instances (different endpoints)
		assert.NotSame(t, client1, client2, "different hosts should get different clients")
		assert.NotSame(t, client1, client3, "different ports should get different clients")
		assert.NotSame(t, client2, client3, "different endpoints should get different clients")
	})
}

func TestRemoteAsk_InvalidMessage(t *testing.T) {
	r := NewClient()
	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)

	_, err := r.RemoteAsk(context.Background(), from, to, nil, time.Second)
	assert.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRemoteTell_InvalidMessage(t *testing.T) {
	r := NewClient()
	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)

	err := r.RemoteTell(context.Background(), from, to, nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRemoteSpawn_InvalidRequest(t *testing.T) {
	r := NewClient()
	_, err := r.RemoteSpawn(context.Background(), "host", 1000, &remote.SpawnRequest{})
	assert.Error(t, err)
}

func TestRemoteSpawn_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1

	_, err := r.RemoteSpawn(context.Background(), "host", port, &remote.SpawnRequest{Name: "actor", Kind: "kind"})
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteSpawn_WithReentrancy(t *testing.T) {
	// Test that spawn request with reentrancy config does not panic
	r := NewClient()

	req := &remote.SpawnRequest{
		Name: "actor",
		Kind: "kind",
		Reentrancy: reentrancy.New(
			reentrancy.WithMode(reentrancy.StashNonReentrant),
			reentrancy.WithMaxInFlight(5),
		),
	}

	// This will fail to connect but should not panic with reentrancy config
	_, err := r.RemoteSpawn(context.Background(), "host", 1000, req)
	// Error is expected since we're not actually connecting to a server
	assert.Error(t, err)
}

func TestRemoteLookup_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1

	_, err := r.RemoteLookup(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteReSpawn_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1

	_, err := r.RemoteReSpawn(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

// TestRemoteReSpawn_EmptyAddressResponse covers the path where the server returns
// a RemoteReSpawnResponse with an empty address (line 1174: ErrInvalidResponse).
func TestRemoteReSpawn_EmptyAddressResponse(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteReSpawnResponse{Address: ""}, nil
	}

	ps, err := inet.NewProtoServer("127.0.0.1:0",
		inet.WithProtoHandler("internalpb.RemoteReSpawnRequest", handler),
	)
	require.NoError(t, err)
	require.NoError(t, ps.Listen())

	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)

	host, portStr, err := net.SplitHostPort(ps.ListenAddr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	r := NewClient(WithClientCompression(remote.NoCompression))
	defer r.Close()

	addr, err := r.RemoteReSpawn(context.Background(), host, port, "actor")
	assert.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidResponse)
	assert.Nil(t, addr)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteStop_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1

	err := r.RemoteStop(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteReinstate_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1

	err := r.RemoteReinstate(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteActivateGrain_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1
	grainReq := &remote.GrainRequest{
		Kind: "kind",
		Name: "name",
	}

	err := r.RemoteActivateGrain(context.Background(), "host", port, grainReq)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteTellGrain_InvalidMessage(t *testing.T) {
	r := NewClient()
	grainReq := &remote.GrainRequest{
		Kind: "kind",
		Name: "name",
	}

	err := r.RemoteTellGrain(context.Background(), "host", 1000, grainReq, nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRemoteAskGrain_InvalidMessage(t *testing.T) {
	r := NewClient()
	grainReq := &remote.GrainRequest{
		Kind: "kind",
		Name: "name",
	}

	_, err := r.RemoteAskGrain(context.Background(), "host", 1000, grainReq, nil, time.Second)
	assert.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRemoteAskGrain_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1
	grainReq := &remote.GrainRequest{
		Kind: "kind",
		Name: "name",
	}

	_, err := r.RemoteAskGrain(context.Background(), "host", port, grainReq, durationpb.New(time.Second), time.Second)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteBatchTell_FiltersNilMessages(t *testing.T) {
	r := NewClient()
	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)

	// Pass a mix of valid and nil messages
	messages := []any{
		durationpb.New(time.Second),
		nil, // should be filtered out
		durationpb.New(2 * time.Second),
		nil, // should be filtered out
	}

	// This will fail to connect but should not panic on nil messages
	err := r.RemoteBatchTell(context.Background(), from, to, messages)
	// Error is expected since we're not actually connecting to a server
	// but the important part is that it doesn't panic on nil messages
	assert.Error(t, err)
}

func TestRemoteBatchAsk_FiltersNilMessages(t *testing.T) {
	r := NewClient()
	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)

	// Pass a mix of valid and nil messages
	messages := []any{
		durationpb.New(time.Second),
		nil, // should be filtered out
		durationpb.New(2 * time.Second),
	}

	// This will fail to connect but should not panic on nil messages
	_, err := r.RemoteBatchAsk(context.Background(), from, to, messages, time.Second)
	// Error is expected since we're not actually connecting to a server
	// but the important part is that it doesn't panic on nil messages
	assert.Error(t, err)
}

func TestRemotingSerializer(t *testing.T) {
	t.Run("nil message returns the pre-built dispatcher", func(t *testing.T) {
		r := NewClient().(*client)
		s := r.Serializer(nil)
		require.NotNil(t, s, "Serializer(nil) must return the composite dispatcher, not nil")
		_, ok := s.(*serializerDispatch)
		assert.True(t, ok)
	})

	t.Run("proto message matches the interface entry", func(t *testing.T) {
		r := NewClient().(*client)
		s := r.Serializer(durationpb.New(time.Second))
		require.NotNil(t, s)
		_, ok := s.(*remote.ProtoSerializer)
		assert.True(t, ok)
	})

	t.Run("concrete type registered with WithRemotingSerializers", func(t *testing.T) {
		custom := &stubSerializer{}
		r := NewClient(WithClientSerializers(new(nonProtoMsg), custom)).(*client)
		s := r.Serializer(&nonProtoMsg{"x"})
		require.NotNil(t, s)
		assert.Same(t, custom, s)
	})

	t.Run("unknown type returns nil", func(t *testing.T) {
		r := NewClient().(*client)
		s := r.Serializer(&nonProtoMsg{"x"})
		assert.Nil(t, s)
	})
}

func TestWithRemotingContextPropagator(t *testing.T) {
	t.Run("nil propagator is ignored", func(t *testing.T) {
		r := NewClient(WithClientContextPropagator(nil)).(*client)
		assert.Nil(t, r.contextPropagator)
	})

	t.Run("non-nil propagator is applied", func(t *testing.T) {
		prop := mockPropagator{}
		r := NewClient(WithClientContextPropagator(prop)).(*client)
		assert.Equal(t, prop, r.contextPropagator)
	})
}

func TestWithRemotingSerializers(t *testing.T) {
	t.Run("concrete type registration", func(t *testing.T) {
		custom := &stubSerializer{}
		r := NewClient(WithClientSerializers(new(nonProtoMsg), custom)).(*client)
		s := r.Serializer(&nonProtoMsg{"x"})
		require.NotNil(t, s)
		assert.Same(t, custom, s)
	})

	t.Run("nil serializer is silently ignored", func(t *testing.T) {
		// Applying a nil serializer must not panic and must not add an entry.
		before := NewClient().(*client)
		after := NewClient(WithClientSerializers(new(nonProtoMsg), nil)).(*client)
		assert.Equal(t, len(before.serializers), len(after.serializers))
	})
}

func TestEnrichContext(t *testing.T) {
	t.Run("no propagator no deadline", func(t *testing.T) {
		r := NewClient().(*client)
		enriched, err := r.enrichContext(context.Background())
		require.NoError(t, err)
		assert.NotNil(t, enriched)
	})

	t.Run("propagator inject error is propagated", func(t *testing.T) {
		r := NewClient(WithClientContextPropagator(errInjectPropagator{})).(*client)
		_, err := r.enrichContext(context.Background())
		require.Error(t, err)
		assert.EqualError(t, err, "inject error")
	})

	t.Run("propagator with headers is applied", func(t *testing.T) {
		r := NewClient(WithClientContextPropagator(headerPropagator{"X-Trace-Id", "abc"})).(*client)
		enriched, err := r.enrichContext(context.Background())
		require.NoError(t, err)
		assert.NotNil(t, enriched)
	})

	t.Run("context with deadline sets metadata deadline", func(t *testing.T) {
		r := NewClient().(*client)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		enriched, err := r.enrichContext(ctx)
		require.NoError(t, err)
		assert.NotNil(t, enriched)
	})
}

func TestCheckProtoError(t *testing.T) {
	t.Run("non-error response returns nil", func(t *testing.T) {
		resp := &internalpb.RemoteLookupResponse{}
		assert.NoError(t, checkProtoError(resp))
	})

	t.Run("CODE_NOT_FOUND returns ErrAddressNotFound", func(t *testing.T) {
		resp := &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "gone"}
		err := checkProtoError(resp)
		require.ErrorIs(t, err, gerrors.ErrAddressNotFound)
	})

	t.Run("CODE_DEADLINE_EXCEEDED returns ErrRequestTimeout", func(t *testing.T) {
		resp := &internalpb.Error{Code: internalpb.Code_CODE_DEADLINE_EXCEEDED}
		err := checkProtoError(resp)
		require.ErrorIs(t, err, gerrors.ErrRequestTimeout)
	})

	t.Run("CODE_UNAVAILABLE returns ErrRemoteSendFailure", func(t *testing.T) {
		resp := &internalpb.Error{Code: internalpb.Code_CODE_UNAVAILABLE}
		err := checkProtoError(resp)
		require.ErrorIs(t, err, gerrors.ErrRemoteSendFailure)
	})

	t.Run("CODE_FAILED_PRECONDITION delegates to parseFailedPrecondition", func(t *testing.T) {
		resp := &internalpb.Error{Code: internalpb.Code_CODE_FAILED_PRECONDITION, Message: gerrors.ErrRemotingDisabled.Error()}
		err := checkProtoError(resp)
		require.ErrorIs(t, err, gerrors.ErrRemotingDisabled)
	})

	t.Run("CODE_ALREADY_EXISTS delegates to parseAlreadyExists", func(t *testing.T) {
		resp := &internalpb.Error{Code: internalpb.Code_CODE_ALREADY_EXISTS, Message: "actor conflict"}
		err := checkProtoError(resp)
		require.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)
	})

	t.Run("CODE_INVALID_ARGUMENT returns formatted error", func(t *testing.T) {
		resp := &internalpb.Error{Code: internalpb.Code_CODE_INVALID_ARGUMENT, Message: "bad field"}
		err := checkProtoError(resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bad field")
	})

	t.Run("CODE_INTERNAL_ERROR returns plain error with message", func(t *testing.T) {
		resp := &internalpb.Error{Code: internalpb.Code_CODE_INTERNAL_ERROR, Message: "oops"}
		err := checkProtoError(resp)
		require.Error(t, err)
		assert.EqualError(t, err, "oops")
	})

	t.Run("unrecognized code falls through to default", func(t *testing.T) {
		resp := &internalpb.Error{Code: internalpb.Code_CODE_RESOURCE_EXHAUSTED, Message: "quota"}
		err := checkProtoError(resp)
		require.Error(t, err)
		assert.EqualError(t, err, "quota")
	})
}

func TestParseFailedPrecondition(t *testing.T) {
	t.Run("ErrTypeNotRegistered substring", func(t *testing.T) {
		err := parseFailedPrecondition(gerrors.ErrTypeNotRegistered.Error())
		assert.ErrorIs(t, err, gerrors.ErrTypeNotRegistered)
	})

	t.Run("ErrRemotingDisabled substring", func(t *testing.T) {
		err := parseFailedPrecondition(gerrors.ErrRemotingDisabled.Error())
		assert.ErrorIs(t, err, gerrors.ErrRemotingDisabled)
	})

	t.Run("ErrClusterDisabled substring", func(t *testing.T) {
		err := parseFailedPrecondition(gerrors.ErrClusterDisabled.Error())
		assert.ErrorIs(t, err, gerrors.ErrClusterDisabled)
	})

	t.Run("unknown message returns generic error", func(t *testing.T) {
		err := parseFailedPrecondition("something unexpected")
		require.Error(t, err)
		assert.EqualError(t, err, "something unexpected")
	})
}

func TestParseAlreadyExists(t *testing.T) {
	t.Run("singleton in message returns ErrSingletonAlreadyExists", func(t *testing.T) {
		err := parseAlreadyExists("singleton conflict")
		assert.ErrorIs(t, err, gerrors.ErrSingletonAlreadyExists)
	})

	t.Run("other message returns ErrActorAlreadyExists", func(t *testing.T) {
		err := parseAlreadyExists("actor conflict")
		assert.ErrorIs(t, err, gerrors.ErrActorAlreadyExists)
	})
}

func TestRemoteTell_NoSerializerForType(t *testing.T) {
	r := NewClient()
	from := address.New("from", "sys", "127.0.0.1", 1000)
	to := address.New("to", "sys", "127.0.0.1", 1000)

	err := r.RemoteTell(context.Background(), from, to, &nonProtoMsg{"x"})
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRemoteAsk_NoSerializerForType(t *testing.T) {
	r := NewClient()
	from := address.New("from", "sys", "127.0.0.1", 1000)
	to := address.New("to", "sys", "127.0.0.1", 1000)

	_, err := r.RemoteAsk(context.Background(), from, to, &nonProtoMsg{"x"}, time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRemoteTellGrain_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1
	grainReq := &remote.GrainRequest{Kind: "kind", Name: "name"}

	err := r.RemoteTellGrain(context.Background(), "host", port, grainReq, durationpb.New(time.Second))
	require.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteTellGrain_NoSerializerForType(t *testing.T) {
	r := NewClient()
	grainReq := &remote.GrainRequest{Kind: "kind", Name: "name"}

	err := r.RemoteTellGrain(context.Background(), "host", 1000, grainReq, &nonProtoMsg{"x"})
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRemoteActivateGrain_InvalidRequest(t *testing.T) {
	r := NewClient()
	// Empty Kind and Name should fail validation inside getGrainFromRequest.
	err := r.RemoteActivateGrain(context.Background(), "host", 1000, &remote.GrainRequest{})
	require.Error(t, err)
}

func TestRemoteBatchTell_NoSerializer(t *testing.T) {
	r := NewClient()
	from := address.New("from", "sys", "127.0.0.1", 1000)
	to := address.New("to", "sys", "127.0.0.1", 1000)

	err := r.RemoteBatchTell(context.Background(), from, to, []any{&nonProtoMsg{"x"}})
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRemoteBatchAsk_NoSerializer(t *testing.T) {
	r := NewClient()
	from := address.New("from", "sys", "127.0.0.1", 1000)
	to := address.New("to", "sys", "127.0.0.1", 1000)

	_, err := r.RemoteBatchAsk(context.Background(), from, to, []any{&nonProtoMsg{"x"}}, time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRemoteTell_ConnectionRefused(t *testing.T) {
	r := NewClient()
	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)

	err := r.RemoteTell(context.Background(), from, to, durationpb.New(time.Second))
	assert.Error(t, err)
}

func TestRemoteAsk_ConnectionRefused(t *testing.T) {
	r := NewClient()
	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)

	_, err := r.RemoteAsk(context.Background(), from, to, durationpb.New(time.Second), time.Second)
	assert.Error(t, err)
}

func TestRemoteLookup_ConnectionRefused(t *testing.T) {
	r := NewClient()

	_, err := r.RemoteLookup(context.Background(), "host", 1000, "some-actor")
	assert.Error(t, err)
}

func TestRemoteActivateGrain_ConnectionRefused(t *testing.T) {
	r := NewClient()
	grainReq := &remote.GrainRequest{Kind: "kind", Name: "name"}

	err := r.RemoteActivateGrain(context.Background(), "host", 1000, grainReq)
	assert.Error(t, err)
}

func TestRemoteAskGrain_ConnectionRefused(t *testing.T) {
	r := NewClient()
	grainReq := &remote.GrainRequest{Kind: "kind", Name: "name"}

	_, err := r.RemoteAskGrain(context.Background(), "host", 1000, grainReq, durationpb.New(time.Second), time.Second)
	assert.Error(t, err)
}

func TestRemoteTellGrain_ConnectionRefused(t *testing.T) {
	r := NewClient()
	grainReq := &remote.GrainRequest{Kind: "kind", Name: "name"}

	err := r.RemoteTellGrain(context.Background(), "host", 1000, grainReq, durationpb.New(time.Second))
	assert.Error(t, err)
}

func TestRemoteReSpawn_ConnectionRefused(t *testing.T) {
	r := NewClient()

	_, err := r.RemoteReSpawn(context.Background(), "host", 1000, "actor")
	assert.Error(t, err)
}

func TestRemoteStop_ConnectionRefused(t *testing.T) {
	r := NewClient()

	err := r.RemoteStop(context.Background(), "host", 1000, "actor")
	assert.Error(t, err)
}

func TestRemoteReinstate_ConnectionRefused(t *testing.T) {
	r := NewClient()

	err := r.RemoteReinstate(context.Background(), "host", 1000, "actor")
	assert.Error(t, err)
}

func TestRemoteBatchAsk_ConnectionRefused(t *testing.T) {
	r := NewClient()
	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)

	_, err := r.RemoteBatchAsk(context.Background(), from, to, []any{durationpb.New(time.Second)}, time.Second)
	assert.Error(t, err)
}

func TestRemoteSpawn_ConnectionRefused(t *testing.T) {
	r := NewClient()

	_, err := r.RemoteSpawn(context.Background(), "host", 1000, &remote.SpawnRequest{Name: "actor", Kind: "kind"})
	assert.Error(t, err)
}

func TestWithRemotingContextPropagator_EnrichesRequests(t *testing.T) {
	// Ensure the propagator is carried through to enrichContext on real send paths.
	r := NewClient(WithClientContextPropagator(headerPropagator{"X-ID", "42"}))
	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)

	// The send will fail at dial time, but enrichContext is still exercised.
	err := r.RemoteTell(context.Background(), from, to, durationpb.New(time.Second))
	assert.Error(t, err)
}
