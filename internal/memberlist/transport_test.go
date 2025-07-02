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

package memberlist

import (
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/kapetan-io/tackle/autotls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/travisjeffery/go-dynaport"
)

func TestTCPTransport(t *testing.T) {
	t.Run("NewTCPTransport with empty config", func(t *testing.T) {
		transport, err := NewTransport(TransportConfig{})
		require.NoError(t, err)
		require.NotNil(t, transport)
		assert.NoError(t, transport.Shutdown())
	})
	t.Run("NewTCPTransport failed with invalid address", func(t *testing.T) {
		transport, err := NewTransport(TransportConfig{
			BindAddrs: []string{"127.0.0.0.0.1"},
		})
		require.Error(t, err)
		require.Nil(t, transport)
	})
	t.Run("NewTCPTransport already listening on port", func(t *testing.T) {
		host := "127.0.0.1"
		ports := dynaport.Get(1)
		transport, err := NewTransport(TransportConfig{
			BindAddrs: []string{host},
			BindPort:  ports[0],
		})
		require.NoError(t, err)
		require.NotNil(t, transport)

		transport2, err := NewTransport(TransportConfig{
			BindAddrs: []string{host},
			BindPort:  ports[0],
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to start TCP listener")
		require.Nil(t, transport2)
		assert.NoError(t, transport.Shutdown())
	})
	t.Run("NewTCPTransport with TLS", func(t *testing.T) {
		host := "127.0.0.1"
		ports := dynaport.Get(1)
		// AutoGenerate TLS certs
		conf := autotls.Config{AutoTLS: true}
		require.NoError(t, autotls.Setup(&conf))
		transport, err := NewTransport(TransportConfig{
			BindAddrs:  []string{host},
			BindPort:   ports[0],
			TLSEnabled: true,
			TLS:        conf.ServerTLS,
		})
		require.NoError(t, err)
		require.NotNil(t, transport)
		assert.NoError(t, transport.Shutdown())
	})
	t.Run("With GetAutoBindPort", func(t *testing.T) {
		host := "127.0.0.1"
		transport, err := NewTransport(TransportConfig{
			BindAddrs: []string{host},
			BindPort:  0,
		})
		require.NoError(t, err)
		require.NotNil(t, transport)

		port := transport.GetAutoBindPort()
		assert.NotZero(t, port)
		assert.NoError(t, transport.Shutdown())
	})
	t.Run("With FinalAdvertiseAddr", func(t *testing.T) {
		host := "127.0.0.1"
		transport, err := NewTransport(TransportConfig{
			BindAddrs: []string{host},
			BindPort:  0,
		})
		require.NoError(t, err)
		require.NotNil(t, transport)
		ports := dynaport.Get(1)

		ip, port, err := transport.FinalAdvertiseAddr(host, ports[0])
		require.NoError(t, err)
		require.NotNil(t, ip)
		assert.Equal(t, ports[0], port)
		assert.NoError(t, transport.Shutdown())
	})
	t.Run("With FinalAdvertiseAddr with empty address", func(t *testing.T) {
		host := "127.0.0.1"
		transport, err := NewTransport(TransportConfig{
			BindAddrs: []string{host},
			BindPort:  0,
		})
		require.NoError(t, err)
		require.NotNil(t, transport)
		ports := dynaport.Get(1)

		ip, port, err := transport.FinalAdvertiseAddr("", ports[0])
		require.NoError(t, err)
		require.NotNil(t, ip)
		assert.NotEqual(t, ports[0], port)
		assert.NoError(t, transport.Shutdown())
	})

	t.Run("With FinalAdvertiseAddr with invalid address", func(t *testing.T) {
		host := "127.0.0.1"
		transport, err := NewTransport(TransportConfig{
			BindAddrs: []string{host},
			BindPort:  0,
		})
		require.NoError(t, err)
		require.NotNil(t, transport)
		ports := dynaport.Get(1)

		ip, port, err := transport.FinalAdvertiseAddr("127.0.0.0.0.1-invalid", ports[0])
		require.Error(t, err)
		require.Nil(t, ip)
		assert.Zero(t, port)
		assert.NoError(t, transport.Shutdown())
	})
	t.Run("SendBestEffort", func(t *testing.T) {
		cluster, cleanup := makeCluster(t)
		defer cleanup()

		msg := "message"
		err := cluster.members[0].SendBestEffort(cluster.members[1].Members()[1], []byte(msg))
		require.NoError(t, err)

		pause.For(time.Second)
		require.Len(t, cluster.delegates[1].Messages(), 1)
	})
	t.Run("SendReliable", func(t *testing.T) {
		cluster, cleanup := makeCluster(t)
		defer cleanup()

		msg := "message"
		err := cluster.members[0].SendReliable(cluster.members[1].Members()[1], []byte(msg))
		require.NoError(t, err)

		pause.For(time.Second)
		require.Len(t, cluster.delegates[1].Messages(), 1)
	})
}

func makeCluster(t *testing.T) (*mockCluster, func()) {
	cluster := &mockCluster{
		members:   [2]*memberlist.Memberlist{},
		delegates: [2]*delegate{},
	}

	for i := 0; i < 2; i++ {
		cluster.delegates[i] = &delegate{}
		cluster.members[i] = newNode(t, strconv.Itoa(i), cluster.delegates[i])
	}

	for i := 0; i < 2; i++ {
		_, err := cluster.members[i].Join([]string{cluster.members[(i+1)%2].LocalNode().Address()})
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for i := 0; i < 2; i++ {
		require.Len(t, cluster.members[i].Members(), 2)
	}

	cleanupFunc := func() {
		for i := 0; i < 2; i++ {
			err := cluster.members[i].Shutdown()
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	return cluster, cleanupFunc
}

func newNode(t *testing.T, name string, delegate memberlist.Delegate) *memberlist.Memberlist {
	// TODO: fix TLS verification
	conf := autotls.Config{
		AutoTLS:            true,
		ClientAuth:         tls.NoClientCert,
		InsecureSkipVerify: false,
	}

	require.NoError(t, autotls.Setup(&conf))

	mconf := memberlist.DefaultLocalConfig()
	mconf.BindPort = 0
	if delegate != nil {
		mconf.Delegate = delegate
	}
	mconf.BindAddr = "127.0.0.1"
	mconf.Logger = log.New(os.Stderr, name+": ", log.LstdFlags)

	tConfig := TransportConfig{
		BindAddrs:  []string{mconf.BindAddr},
		BindPort:   mconf.BindPort,
		TLS:        conf.ServerTLS,
		TLSEnabled: false, // TODO: fix TLS verification
	}

	// implement some poor mechanism here
	retry := func(limit int) (*Transport, error) {
		var err error
		for try := 0; try < limit; try++ {
			var transport *Transport
			if transport, err = NewTransport(tConfig); err == nil {
				return transport, nil
			}
			if strings.Contains(err.Error(), "address already in use") {
				mconf.Logger.Printf("[DEBUG] memberlist: Got bind error: %v", err)
				continue
			}
		}
		return nil, fmt.Errorf("failed to obtain an address: %v", err)
	}

	maxAttempts := 1
	if mconf.BindPort == 0 {
		maxAttempts = 10
	}

	transport, err := retry(maxAttempts)
	require.NoError(t, err)
	if mconf.BindPort == 0 {
		mconf.BindPort = transport.GetAutoBindPort()
		mconf.AdvertisePort = mconf.BindPort
		mconf.Logger.Printf("[DEBUG] memberlist: Using dynamic bind port %d", mconf.BindPort)
	}
	mconf.Transport = transport
	mconf.Name = fmt.Sprintf("node-%d", mconf.BindPort)
	mlist, err := memberlist.Create(mconf)
	require.NoError(t, err)
	return mlist
}

type mockCluster struct {
	members   [2]*memberlist.Memberlist
	delegates [2]*delegate
}

type delegate struct {
	sync.Mutex
	messages [][]byte
}

// nolint
func (d *delegate) NodeMeta(limit int) []byte {
	return []byte{}
}

// nolint
func (d *delegate) NotifyMsg(m []byte) {
	d.Lock()
	defer d.Unlock()
	d.messages = append(d.messages, m)
}

func (d *delegate) Messages() [][]byte {
	d.Lock()
	defer d.Unlock()
	return d.messages
}

// nolint
func (d *delegate) GetBroadcasts(overhead, limit int) [][]byte {
	return [][]byte{}
}

// nolint
func (d *delegate) LocalState(join bool) []byte {
	return []byte{}
}

// nolint
func (d *delegate) MergeRemoteState(buf []byte, join bool) {}
