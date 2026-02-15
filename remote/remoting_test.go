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
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v3/address"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/reentrancy"
)

func TestRemotingOptionsAndDefaults(t *testing.T) {
	r := NewRemoting().(*remoting)

	// Default compression is Zstd (matches server default in remote.Config)
	assert.Equal(t, ZstdCompression, r.Compression())
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
	r := NewRemoting(
		WithRemotingTLS(tlsCfg),
		WithRemotingCompression(GzipCompression),
		WithRemotingMaxIdleConns(50),
		WithRemotingIdleTimeout(60*time.Second),
		WithRemotingDialTimeout(3*time.Second),
		WithRemotingKeepAlive(10*time.Second),
	).(*remoting)

	assert.Equal(t, tlsCfg, r.TLSConfig())
	assert.Equal(t, GzipCompression, r.Compression())
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
		r := NewRemoting().(*remoting)

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
		r := NewRemoting().(*remoting)

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
		r := NewRemoting().(*remoting)

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
		r := NewRemoting().(*remoting)

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
		r := NewRemoting().(*remoting)

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
		r := NewRemoting().(*remoting)

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

func TestRemoteSpawn_WithReentrancy(t *testing.T) {
	// Test that spawn request with reentrancy config does not panic
	r := NewRemoting()

	req := &SpawnRequest{
		Name: "actor",
		Kind: "kind",
		Reentrancy: reentrancy.New(
			reentrancy.WithMode(reentrancy.StashNonReentrant),
			reentrancy.WithMaxInFlight(5),
		),
	}

	// This will fail to connect but should not panic with reentrancy config
	err := r.RemoteSpawn(context.Background(), "host", 1000, req)
	// Error is expected since we're not actually connecting to a server
	assert.Error(t, err)
}

func TestRemoteLookup_InvalidPort(t *testing.T) {
	r := NewRemoting()
	port := int(math.MaxInt32) + 1

	_, err := r.RemoteLookup(context.Background(), "host", port, "actor")
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
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

func TestRemoteActivateGrain_InvalidPort(t *testing.T) {
	r := NewRemoting()
	port := int(math.MaxInt32) + 1
	grainReq := &GrainRequest{
		Kind: "kind",
		Name: "name",
	}

	err := r.RemoteActivateGrain(context.Background(), "host", port, grainReq)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteTellGrain_InvalidMessage(t *testing.T) {
	r := NewRemoting()
	grainReq := &GrainRequest{
		Kind: "kind",
		Name: "name",
	}

	err := r.RemoteTellGrain(context.Background(), "host", 1000, grainReq, nil)
	assert.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRemoteAskGrain_InvalidMessage(t *testing.T) {
	r := NewRemoting()
	grainReq := &GrainRequest{
		Kind: "kind",
		Name: "name",
	}

	_, err := r.RemoteAskGrain(context.Background(), "host", 1000, grainReq, nil, time.Second)
	assert.Error(t, err)
	assert.ErrorIs(t, err, gerrors.ErrInvalidMessage)
}

func TestRemoteAskGrain_InvalidPort(t *testing.T) {
	r := NewRemoting()
	port := int(math.MaxInt32) + 1
	grainReq := &GrainRequest{
		Kind: "kind",
		Name: "name",
	}

	_, err := r.RemoteAskGrain(context.Background(), "host", port, grainReq, durationpb.New(time.Second), time.Second)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "out of range")
}

func TestRemoteBatchTell_FiltersNilMessages(t *testing.T) {
	r := NewRemoting()
	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)

	// Pass a mix of valid and nil messages
	messages := []proto.Message{
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
	r := NewRemoting()
	from := address.New("from", "sys", "host", 1000)
	to := address.New("to", "sys", "host", 1000)

	// Pass a mix of valid and nil messages
	messages := []proto.Message{
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

// TODO: Integration tests
// The following tests require a running proto TCP server and should be
// rewritten as integration tests:
// - TestRemotingHeaderPropagation
// - TestRemoteSpawn_Success
// - TestRemoteLookup_Success
// - TestRemoteAsk_Success
// - TestRemoteTell_Success
// - TestRemoteReSpawn_Success
// - TestRemoteStop_Success
// - TestRemoteReinstate_Success
// - TestRemoteActivateGrain_Success
// - TestRemoteTellGrain_Success
// - TestRemoteAskGrain_Success
//
// These tests should be implemented in a separate integration test file
// that starts an actual actor system with proto TCP server enabled.
