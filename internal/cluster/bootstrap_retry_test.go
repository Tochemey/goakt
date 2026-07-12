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

package cluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/discovery/nats"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/log"
)

func TestRetryBootstrap(t *testing.T) {
	t.Run("first attempt succeeds without sleeping", func(t *testing.T) {
		calls := 0
		start := time.Now()
		err := retryBootstrap(context.Background(), 3, time.Second, log.DiscardLogger, func() error {
			calls++
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 1, calls)
		assert.Less(t, time.Since(start), 500*time.Millisecond)
	})

	t.Run("transient failure heals within the attempt budget", func(t *testing.T) {
		calls := 0
		err := retryBootstrap(context.Background(), 3, time.Millisecond, log.DiscardLogger, func() error {
			calls++
			if calls < 3 {
				return errors.New("still syncing")
			}
			return nil
		})
		require.NoError(t, err)
		assert.Equal(t, 3, calls)
	})

	t.Run("exhaustion wraps the last error with the attempt count", func(t *testing.T) {
		boom := errors.New("boom")
		calls := 0
		err := retryBootstrap(context.Background(), 3, time.Millisecond, log.DiscardLogger, func() error {
			calls++
			return boom
		})
		require.Error(t, err)
		assert.Equal(t, 3, calls)
		assert.ErrorIs(t, err, boom)
		assert.Contains(t, err.Error(), "after 3 attempts")
	})

	t.Run("context cancelation during backoff stops retrying", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		boom := errors.New("boom")
		calls := 0
		err := retryBootstrap(ctx, 3, time.Hour, log.DiscardLogger, func() error {
			calls++
			cancel()
			return boom
		})
		require.Error(t, err)
		assert.Equal(t, 1, calls)
		assert.ErrorIs(t, err, boom)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

// TestBootstrapFailureRetriesAndReleasesPorts pins both halves of #1257: a failing
// bootstrap is retried the full attempt budget (instead of killing the process on the
// first error), and every failed attempt tears the partial engine down, so no port is
// left bound once Start finally gives up.
func TestBootstrapFailureRetriesAndReleasesPorts(t *testing.T) {
	ctx := context.TODO()
	nodePorts := dynaport.Get(4)
	gossipPort, clusterPort, remotingPort, deadPort := nodePorts[0], nodePorts[1], nodePorts[2], nodePorts[3]
	host := "127.0.0.1"

	// point discovery at a port nothing listens on: bootstrap cannot complete
	config := nats.Config{
		NatsServer:    fmt.Sprintf("nats://%s:%d", host, deadPort),
		NatsSubject:   "bootstrap-retry-subject",
		Host:          host,
		DiscoveryPort: gossipPort,
	}
	hostNode := discovery.Node{
		Name:          host,
		Host:          host,
		DiscoveryPort: gossipPort,
		PeersPort:     clusterPort,
		RemotingPort:  remotingPort,
	}

	engine := New("testSystem", nats.NewDiscovery(&config), &hostNode,
		WithLogger(log.DiscardLogger),
		WithBootstrapTimeout(time.Second),
	)
	require.NotNil(t, engine)

	err := engine.Start(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "after 3 attempts", "bootstrap must exhaust its retry budget, not fail on the first attempt")

	for _, port := range []int{gossipPort, clusterPort} {
		ln, lerr := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
		require.NoError(t, lerr, "port %d still bound after a failed bootstrap", port)
		require.NoError(t, ln.Close())
	}
}
