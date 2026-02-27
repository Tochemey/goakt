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

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/passivation"
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

func TestRemoteAsk_InvalidResponseType(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteLookupResponse{}, nil // wrong type
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteAskRequest", handler))
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

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	_, err = r.RemoteAsk(context.Background(), from, to, durationpb.New(time.Second), time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid response type")

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteAsk_EmptyMessagesReturnsNil(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteAskResponse{Messages: nil}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteAskRequest", handler))
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

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	resp, err := r.RemoteAsk(context.Background(), from, to, durationpb.New(time.Second), time.Second)
	require.NoError(t, err)
	assert.Nil(t, resp)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteAsk_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_DEADLINE_EXCEEDED, Message: "timeout"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteAskRequest", handler))
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

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	_, err = r.RemoteAsk(context.Background(), from, to, durationpb.New(time.Second), time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrRequestTimeout)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteLookup_NotFoundReturnsNoSender(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "actor not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteLookupRequest", handler))
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

	addr, err := r.RemoteLookup(context.Background(), host, port, "missing-actor")
	require.NoError(t, err)
	require.NotNil(t, addr)
	assert.True(t, addr.Equals(address.NoSender()))

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteLookup_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_UNAVAILABLE, Message: "unavailable"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteLookupRequest", handler))
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

	_, err = r.RemoteLookup(context.Background(), host, port, "actor")
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrRemoteSendFailure)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteLookup_InvalidResponseType(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteAskResponse{}, nil // wrong type
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteLookupRequest", handler))
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

	_, err = r.RemoteLookup(context.Background(), host, port, "actor")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid response type")

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteStop_NotFoundReturnsNoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "actor not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteStopRequest", handler))
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

	err = r.RemoteStop(context.Background(), host, port, "missing-actor")
	require.NoError(t, err)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteReinstate_NotFoundReturnsNoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "actor not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteReinstateRequest", handler))
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

	err = r.RemoteReinstate(context.Background(), host, port, "missing-actor")
	require.NoError(t, err)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteReSpawn_NotFoundReturnsNil(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "actor not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteReSpawnRequest", handler))
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

	addr, err := r.RemoteReSpawn(context.Background(), host, port, "missing-actor")
	require.NoError(t, err)
	assert.Nil(t, addr)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteActivateGrain_DependenciesEncodeError(t *testing.T) {
	r := NewClient()
	port := 1000
	grainReq := &remote.GrainRequest{
		Kind:         "kind",
		Name:         "name",
		Dependencies: []extension.Dependency{&failingDependency{}},
	}

	err := r.RemoteActivateGrain(context.Background(), "host", port, grainReq)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal failed")
}

func TestRemoteAskGrain_InvalidResponseType(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteLookupResponse{}, nil // wrong type
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteAskGrainRequest", handler))
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

	grainReq := &remote.GrainRequest{Kind: "kind", Name: "name"}
	_, err = r.RemoteAskGrain(context.Background(), host, port, grainReq, durationpb.New(time.Second), time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid response type")

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteAskGrain_DeserializeError(t *testing.T) {
	// Return valid RemoteAskGrainResponse but with message that will fail deserialization
	handler := func(_ context.Context, _ inet.Connection, req proto.Message) (proto.Message, error) {
		return &internalpb.RemoteAskGrainResponse{Message: []byte("invalid-proto-bytes")}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteAskGrainRequest", handler))
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

	grainReq := &remote.GrainRequest{Kind: "kind", Name: "name"}
	_, err = r.RemoteAskGrain(context.Background(), host, port, grainReq, durationpb.New(time.Second), time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteBatchAsk_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_DEADLINE_EXCEEDED, Message: "timeout"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteAskRequest", handler))
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

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	_, err = r.RemoteBatchAsk(context.Background(), from, to, []any{durationpb.New(time.Second)}, time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrRequestTimeout)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestWithClientSerializers_InterfaceRegistration(t *testing.T) {
	// Register serializer for (*testInterface)(nil) - exercises the interface branch in WithClientSerializers
	// (typ.Kind() == reflect.Ptr && typ.Elem().Kind() == reflect.Interface)
	custom := &stubSerializer{serializeData: []byte("ok"), deserializeMsg: &nonProtoImpl{}}
	r := NewClient(WithClientSerializers((*testInterface)(nil), custom)).(*client)
	s := r.Serializer(&nonProtoImpl{})
	require.NotNil(t, s)
	assert.Same(t, custom, s)
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

func TestRemoteBatchAsk_Success(t *testing.T) {
	msg := durationpb.New(time.Second)
	serializer := remote.NewProtoSerializer()
	packed, err := serializer.Serialize(msg)
	require.NoError(t, err)

	handler := func(_ context.Context, _ inet.Connection, req proto.Message) (proto.Message, error) {
		return &internalpb.RemoteAskResponse{Messages: [][]byte{packed}}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteAskRequest", handler))
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

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	responses, err := r.RemoteBatchAsk(context.Background(), from, to, []any{msg}, time.Second)
	require.NoError(t, err)
	require.Len(t, responses, 1)
	assert.True(t, proto.Equal(msg, responses[0].(*durationpb.Duration)))

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteBatchAsk_InvalidResponseType(t *testing.T) {
	msg := durationpb.New(time.Second)
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteLookupResponse{}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteAskRequest", handler))
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

	from := address.New("from", "sys", host, port)
	to := address.New("to", "sys", host, port)
	_, err = r.RemoteBatchAsk(context.Background(), from, to, []any{msg}, time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid response type")

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteSpawn_ConnectionRefused(t *testing.T) {
	r := NewClient()

	_, err := r.RemoteSpawn(context.Background(), "host", 1000, &remote.SpawnRequest{Name: "actor", Kind: "kind"})
	assert.Error(t, err)
}

func TestRemoteSpawn_Success(t *testing.T) {
	childAddr := "goakt://sys@127.0.0.1:9000/actor"
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteSpawnResponse{Address: childAddr}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteSpawnRequest", handler))
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

	addr, err := r.RemoteSpawn(context.Background(), host, port, &remote.SpawnRequest{Name: "actor", Kind: "kind"})
	require.NoError(t, err)
	require.NotNil(t, addr)
	assert.Equal(t, childAddr, *addr)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteSpawn_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_FAILED_PRECONDITION, Message: gerrors.ErrTypeNotRegistered.Error()}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteSpawnRequest", handler))
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

	_, err = r.RemoteSpawn(context.Background(), host, port, &remote.SpawnRequest{Name: "actor", Kind: "kind"})
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrTypeNotRegistered)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteSpawn_InvalidResponse(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteSpawnResponse{Address: ""}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteSpawnRequest", handler))
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

	_, err = r.RemoteSpawn(context.Background(), host, port, &remote.SpawnRequest{Name: "actor", Kind: "kind"})
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidResponse)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteSpawn_DependenciesEncodeError(t *testing.T) {
	r := NewClient()
	req := &remote.SpawnRequest{
		Name:         "actor",
		Kind:         "kind",
		Dependencies: []extension.Dependency{&failingDependency{}},
	}
	_, err := r.RemoteSpawn(context.Background(), "host", 1000, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal failed")
}

func TestRemoteSpawn_WithSingleton(t *testing.T) {
	childAddr := "goakt://sys@127.0.0.1:9000/singleton"
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteSpawnResponse{Address: childAddr}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteSpawnRequest", handler))
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

	req := &remote.SpawnRequest{
		Name: "actor",
		Kind: "kind",
		Singleton: &remote.SingletonSpec{
			SpawnTimeout: 5 * time.Second,
			WaitInterval: 100 * time.Millisecond,
			MaxRetries:   10,
		},
	}
	addr, err := r.RemoteSpawn(context.Background(), host, port, req)
	require.NoError(t, err)
	require.NotNil(t, addr)
	assert.Equal(t, childAddr, *addr)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteReSpawn_Success(t *testing.T) {
	childAddr := "goakt://sys@127.0.0.1:9000/actor"
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteReSpawnResponse{Address: childAddr}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteReSpawnRequest", handler))
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
	require.NoError(t, err)
	require.NotNil(t, addr)
	assert.Equal(t, childAddr, *addr)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteChildren_ConnectionRefused(t *testing.T) {
	r := NewClient()
	_, err := r.RemoteChildren(context.Background(), "host", 1000, "actor")
	assert.Error(t, err)
}

func TestRemoteParent_ConnectionRefused(t *testing.T) {
	r := NewClient()
	_, err := r.RemoteParent(context.Background(), "host", 1000, "actor")
	assert.Error(t, err)
}

func TestRemoteKind_ConnectionRefused(t *testing.T) {
	r := NewClient()
	_, err := r.RemoteKind(context.Background(), "host", 1000, "actor")
	assert.Error(t, err)
}

func TestRemoteMetric_ConnectionRefused(t *testing.T) {
	r := NewClient()
	_, err := r.RemoteMetric(context.Background(), "host", 1000, "actor")
	assert.Error(t, err)
}

func TestRemoteRole_ConnectionRefused(t *testing.T) {
	r := NewClient()
	_, err := r.RemoteRole(context.Background(), "host", 1000, "actor")
	assert.Error(t, err)
}

func TestRemoteStashSize_ConnectionRefused(t *testing.T) {
	r := NewClient()
	_, err := r.RemoteStashSize(context.Background(), "host", 1000, "actor")
	assert.Error(t, err)
}

func TestRemoteState_ConnectionRefused(t *testing.T) {
	r := NewClient()
	_, err := r.RemoteState(context.Background(), "host", 1000, "actor", remote.ActorStateRunning)
	assert.Error(t, err)
}

func TestRemotePassivationStrategy_ConnectionRefused(t *testing.T) {
	r := NewClient()
	_, err := r.RemotePassivationStrategy(context.Background(), "host", 1000, "actor")
	assert.Error(t, err)
}

func TestRemoteDependencies_ConnectionRefused(t *testing.T) {
	r := NewClient()
	_, err := r.RemoteDependencies(context.Background(), "host", 1000, "actor")
	assert.Error(t, err)
}

func TestRemoteSpawnChild_ConnectionRefused(t *testing.T) {
	r := NewClient()
	req := &remote.SpawnChildRequest{Name: "child", Kind: "kind", Parent: "parent"}
	_, err := r.RemoteSpawnChild(context.Background(), "host", 1000, req)
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

// --- RemoteChildren tests ---

func TestRemoteChildren_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1
	_, err := r.RemoteChildren(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteChildren_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteChildrenRequest", handler))
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

	_, err = r.RemoteChildren(context.Background(), host, port, "actor")
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrAddressNotFound)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteChildren_InvalidResponseType(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteLookupResponse{}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteChildrenRequest", handler))
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

	_, err = r.RemoteChildren(context.Background(), host, port, "actor")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid response type")

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteChildren_Success(t *testing.T) {
	childAddr := "goakt://sys@127.0.0.1:9000/child1"
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteChildrenResponse{Addresses: []string{childAddr}}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteChildrenRequest", handler))
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

	addrs, err := r.RemoteChildren(context.Background(), host, port, "parent")
	require.NoError(t, err)
	require.Len(t, addrs, 1)
	parsed, err := address.Parse(childAddr)
	require.NoError(t, err)
	assert.True(t, addrs[0].Equals(parsed))

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteChildren_SuccessEmpty(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteChildrenResponse{Addresses: nil}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteChildrenRequest", handler))
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

	addrs, err := r.RemoteChildren(context.Background(), host, port, "parent")
	require.NoError(t, err)
	assert.Empty(t, addrs)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

// --- RemoteParent tests ---

func TestRemoteParent_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1
	_, err := r.RemoteParent(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteParent_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteParentRequest", handler))
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

	_, err = r.RemoteParent(context.Background(), host, port, "actor")
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrAddressNotFound)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteParent_Success(t *testing.T) {
	parentAddr := "goakt://sys@127.0.0.1:9000/parent"
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteParentResponse{Address: parentAddr}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteParentRequest", handler))
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

	addr, err := r.RemoteParent(context.Background(), host, port, "child")
	require.NoError(t, err)
	parsed, err := address.Parse(parentAddr)
	require.NoError(t, err)
	assert.True(t, addr.Equals(parsed))

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteParent_EmptyAddressReturnsNoSender(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteParentResponse{Address: ""}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteParentRequest", handler))
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

	addr, err := r.RemoteParent(context.Background(), host, port, "actor")
	require.NoError(t, err)
	assert.True(t, addr.Equals(address.NoSender()))

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

// --- RemoteKind tests ---

func TestRemoteKind_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1
	_, err := r.RemoteKind(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteKind_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteKindRequest", handler))
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

	_, err = r.RemoteKind(context.Background(), host, port, "actor")
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrAddressNotFound)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteKind_Success(t *testing.T) {
	kind := "actor.MockActor"
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteKindResponse{Kind: kind}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteKindRequest", handler))
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

	got, err := r.RemoteKind(context.Background(), host, port, "actor")
	require.NoError(t, err)
	assert.Equal(t, kind, got)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

// --- RemoteMetric tests ---

func TestRemoteMetric_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1
	_, err := r.RemoteMetric(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteMetric_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteMetricRequest", handler))
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

	_, err = r.RemoteMetric(context.Background(), host, port, "actor")
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrAddressNotFound)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteMetric_Success(t *testing.T) {
	m := &internalpb.Metric{
		Uptime:           100,
		ProcessedCount:   42,
		ChildrenCount:    2,
		DeadlettersCount: 0,
	}
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteMetricResponse{Metric: m}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteMetricRequest", handler))
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

	got, err := r.RemoteMetric(context.Background(), host, port, "actor")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(100), got.GetUptime())
	assert.Equal(t, uint64(42), got.GetProcessedCount())
	assert.Equal(t, uint64(2), got.GetChildrenCount())

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteMetric_SuccessNilMetric(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteMetricResponse{Metric: nil}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteMetricRequest", handler))
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

	got, err := r.RemoteMetric(context.Background(), host, port, "actor")
	require.NoError(t, err)
	assert.Nil(t, got)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

// --- RemoteRole tests ---

func TestRemoteRole_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1
	_, err := r.RemoteRole(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteRole_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteRoleRequest", handler))
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

	_, err = r.RemoteRole(context.Background(), host, port, "actor")
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrAddressNotFound)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteRole_Success(t *testing.T) {
	role := "api"
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteRoleResponse{Role: role}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteRoleRequest", handler))
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

	got, err := r.RemoteRole(context.Background(), host, port, "actor")
	require.NoError(t, err)
	assert.Equal(t, role, got)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

// --- RemoteStashSize tests ---

func TestRemoteStashSize_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1
	_, err := r.RemoteStashSize(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteStashSize_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteStashSizeRequest", handler))
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

	_, err = r.RemoteStashSize(context.Background(), host, port, "actor")
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrAddressNotFound)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteStashSize_Success(t *testing.T) {
	size := uint64(5)
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteStashSizeResponse{Size: size}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteStashSizeRequest", handler))
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

	got, err := r.RemoteStashSize(context.Background(), host, port, "actor")
	require.NoError(t, err)
	assert.Equal(t, size, got)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

// --- RemoteState tests ---

func TestRemoteState_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1
	_, err := r.RemoteState(context.Background(), "host", port, "actor", remote.ActorStateRunning)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteState_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteStateRequest", handler))
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

	_, err = r.RemoteState(context.Background(), host, port, "actor", remote.ActorStateRunning)
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrAddressNotFound)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteState_Success(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteStateResponse{State: true}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteStateRequest", handler))
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

	ok, err := r.RemoteState(context.Background(), host, port, "actor", remote.ActorStateRunning)
	require.NoError(t, err)
	assert.True(t, ok)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

// --- RemotePassivationStrategy tests ---

func TestRemotePassivationStrategy_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1
	_, err := r.RemotePassivationStrategy(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemotePassivationStrategy_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemotePassivationStrategyRequest", handler))
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

	_, err = r.RemotePassivationStrategy(context.Background(), host, port, "actor")
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrAddressNotFound)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemotePassivationStrategy_Success(t *testing.T) {
	timeout := 30 * time.Second
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemotePassivationStrategyResponse{
			PassivationStrategy: &internalpb.PassivationStrategy{
				Strategy: &internalpb.PassivationStrategy_TimeBased{
					TimeBased: &internalpb.TimeBasedPassivation{
						PassivateAfter: durationpb.New(timeout),
					},
				},
			},
		}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemotePassivationStrategyRequest", handler))
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

	strategy, err := r.RemotePassivationStrategy(context.Background(), host, port, "actor")
	require.NoError(t, err)
	require.NotNil(t, strategy)
	assert.Equal(t, timeout, strategy.(*passivation.TimeBasedStrategy).Timeout())

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

// --- RemoteDependencies tests ---

func TestRemoteDependencies_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1
	_, err := r.RemoteDependencies(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteDependencies_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteDependenciesRequest", handler))
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

	_, err = r.RemoteDependencies(context.Background(), host, port, "actor")
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrAddressNotFound)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteDependencies_SuccessEmpty(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteDependenciesResponse{Dependencies: nil}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteDependenciesRequest", handler))
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

	deps, err := r.RemoteDependencies(context.Background(), host, port, "actor")
	require.NoError(t, err)
	assert.Empty(t, deps)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteDependencies_DependenciesWithoutRegistryReturnsError(t *testing.T) {
	dep := &mockDependencyForRemote{}
	dep.id = "dep1"
	bytea, _ := dep.MarshalBinary()
	pbDep := &internalpb.Dependency{
		Id:       dep.ID(),
		TypeName: types.Name(dep),
		Bytea:    bytea,
	}
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteDependenciesResponse{Dependencies: []*internalpb.Dependency{pbDep}}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteDependenciesRequest", handler))
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

	_, err = r.RemoteDependencies(context.Background(), host, port, "actor")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "dependency registry required")

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteDependencies_SuccessWithRegistry(t *testing.T) {
	dep := &mockDependencyForRemote{}
	dep.id = "dep1"
	bytea, _ := dep.MarshalBinary()
	pbDep := &internalpb.Dependency{
		Id:       dep.ID(),
		TypeName: types.Name(dep),
		Bytea:    bytea,
	}
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteDependenciesResponse{Dependencies: []*internalpb.Dependency{pbDep}}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteDependenciesRequest", handler))
	require.NoError(t, err)
	require.NoError(t, ps.Listen())
	done := make(chan error, 1)
	go func() { done <- ps.Serve() }()
	pause.For(100 * time.Millisecond)
	host, portStr, err := net.SplitHostPort(ps.ListenAddr().String())
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	reg := types.NewRegistry()
	reg.Register(dep)

	r := NewClient(WithClientCompression(remote.NoCompression), WithDependencyRegistry(reg))
	defer r.Close()

	deps, err := r.RemoteDependencies(context.Background(), host, port, "actor")
	require.NoError(t, err)
	require.Len(t, deps, 1)
	assert.Equal(t, "dep1", deps[0].ID())

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

// --- RemoteSpawnChild tests ---

func TestRemoteSpawnChild_InvalidRequest(t *testing.T) {
	r := NewClient()
	_, err := r.RemoteSpawnChild(context.Background(), "host", 1000, &remote.SpawnChildRequest{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid spawn")
}

func TestRemoteSpawnChild_InvalidPort(t *testing.T) {
	r := NewClient()
	port := int(math.MaxInt32) + 1
	req := &remote.SpawnChildRequest{Name: "child", Kind: "kind", Parent: "parent"}
	_, err := r.RemoteSpawnChild(context.Background(), "host", port, req)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteSpawnChild_ProtoError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.Error{Code: internalpb.Code_CODE_NOT_FOUND, Message: "parent not found"}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteSpawnChildRequest", handler))
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

	req := &remote.SpawnChildRequest{Name: "child", Kind: "kind", Parent: "parent"}
	_, err = r.RemoteSpawnChild(context.Background(), host, port, req)
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrAddressNotFound)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteSpawnChild_Success(t *testing.T) {
	childAddr := "goakt://sys@127.0.0.1:9000/child"
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteSpawnChildResponse{Address: childAddr}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteSpawnChildRequest", handler))
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

	req := &remote.SpawnChildRequest{Name: "child", Kind: "kind", Parent: "parent"}
	addr, err := r.RemoteSpawnChild(context.Background(), host, port, req)
	require.NoError(t, err)
	parsed, err := address.Parse(childAddr)
	require.NoError(t, err)
	assert.True(t, addr.Equals(parsed))

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}

func TestRemoteSpawnChild_EmptyAddressReturnsError(t *testing.T) {
	handler := func(_ context.Context, _ inet.Connection, _ proto.Message) (proto.Message, error) {
		return &internalpb.RemoteSpawnChildResponse{Address: ""}, nil
	}
	ps, err := inet.NewProtoServer("127.0.0.1:0", inet.WithProtoHandler("internalpb.RemoteSpawnChildRequest", handler))
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

	req := &remote.SpawnChildRequest{Name: "child", Kind: "kind", Parent: "parent"}
	_, err = r.RemoteSpawnChild(context.Background(), host, port, req)
	require.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidResponse)

	require.NoError(t, ps.Shutdown(time.Second))
	<-done
}
