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

package mdns

import (
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/mdns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/discovery"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
)

func TestDiscovery(t *testing.T) {
	t.Run("With new instance", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery(nil)
		require.NotNil(t, provider)
		// assert that provider implements the Discovery interface
		// this is a cheap test
		// assert the type of svc
		assert.IsType(t, &Discovery{}, provider)
		var p any = provider
		_, ok := p.(discovery.Provider)
		assert.True(t, ok)
	})
	t.Run("With ID assertion", func(t *testing.T) {
		// cheap test
		// create the instance of provider
		provider := NewDiscovery(nil)
		require.NotNil(t, provider)
		assert.Equal(t, "mdns", provider.ID())
	})

	t.Run("With Initialize", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := ports[0]
		serviceType := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local."

		// create the config
		config := Config{
			Service:     serviceType,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
		}
		// create the instance of provider
		provider := NewDiscovery(&config)

		// set config
		assert.NoError(t, provider.Initialize())
	})
	t.Run("With Initialize: already initialized", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := ports[0]
		serviceType := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local."

		// create the config
		config := Config{
			Service:     serviceType,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
		}
		// create the instance of provider
		provider := NewDiscovery(&config)
		provider.initialized = atomic.NewBool(true)
		assert.Error(t, provider.Initialize())
	})
	t.Run("With Register", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := ports[0]
		serviceType := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local"
		// create the config
		config := Config{
			Service:     serviceType,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
		}
		// create the instance of provider
		provider := NewDiscovery(&config)

		require.NoError(t, provider.Initialize())
		require.NoError(t, provider.Register())

		pause.For(time.Second)
		require.True(t, provider.initialized.Load())
		require.NoError(t, provider.Deregister())
		pause.For(time.Second)
		assert.False(t, provider.initialized.Load())
	})
	t.Run("With Register when already registered", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := ports[0]
		serviceType := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local"
		// create the config
		config := Config{
			Service:     serviceType,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
		}
		// create the instance of provider
		provider := NewDiscovery(&config)
		require.NoError(t, provider.Initialize())
		require.NoError(t, provider.Register())

		pause.For(time.Second)
		require.True(t, provider.initialized.Load())
		err := provider.Register()
		require.Error(t, err)
		require.EqualError(t, err, discovery.ErrAlreadyRegistered.Error())
		require.NoError(t, provider.Deregister())
		pause.For(time.Second)
		assert.False(t, provider.initialized.Load())
	})
	t.Run("With Deregister", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := ports[0]
		serviceType := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local"
		// create the config
		config := Config{
			Service:     serviceType,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
		}
		// create the instance of provider
		provider := NewDiscovery(&config)
		// for the sake of the test
		provider.initialized = atomic.NewBool(true)
		assert.NoError(t, provider.Deregister())
	})
	t.Run("With Deregister when not initialized", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := ports[0]
		serviceType := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local"
		// create the config
		config := Config{
			Service:     serviceType,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
		}
		// create the instance of provider
		provider := NewDiscovery(&config)
		// for the sake of the test
		provider.initialized = atomic.NewBool(false)
		err := provider.Deregister()
		assert.Error(t, err)
		assert.EqualError(t, err, discovery.ErrNotInitialized.Error())
	})
	t.Run("With DiscoverPeers", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := ports[0]
		service := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local."

		// create the config
		config := Config{
			Service:     service,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
		}

		// create the instance of provider
		provider := NewDiscovery(&config)
		require.NoError(t, provider.Initialize())
		require.NoError(t, provider.Register())

		// wait for registration to be completed
		pause.For(time.Second)
		require.True(t, provider.initialized.Load())

		// discover peers
		peers, err := provider.DiscoverPeers()
		require.NoError(t, err)
		require.NotEmpty(t, peers)

		assert.NoError(t, provider.Deregister())
		assert.NoError(t, provider.Close())
	})
	t.Run("With DiscoverPeers with IPV6", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := ports[0]
		service := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local."

		ipv6 := true
		// create the config
		config := Config{
			Service:     service,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
			IPv6:        &ipv6,
		}

		// create the instance of provider
		provider := NewDiscovery(&config)
		require.NoError(t, provider.Initialize())
		require.NoError(t, provider.Register())

		// wait for registration to be completed
		pause.For(time.Second)
		require.True(t, provider.initialized.Load())

		// discover peers
		peers, err := provider.DiscoverPeers()
		require.NoError(t, err)
		require.NotEmpty(t, peers)

		assert.NoError(t, provider.Deregister())
		assert.NoError(t, provider.Close())
	})
	t.Run("With DiscoverPeers: not initialized", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := ports[0]
		service := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local."

		// create the config
		config := Config{
			Service:     service,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
		}

		// create the instance of provider
		provider := NewDiscovery(&config)
		peers, err := provider.DiscoverPeers()
		assert.Error(t, err)
		assert.Empty(t, peers)
		assert.EqualError(t, err, discovery.ErrNotInitialized.Error())
	})
}

func TestMatches(t *testing.T) {
	const (
		service     = "_workstation._tcp"
		serviceName = "AccountsSystem"
		port        = 4042
	)

	provider := NewDiscovery(&Config{
		Service:     service,
		ServiceName: serviceName,
		Domain:      "local.",
		Port:        port,
	})

	testCases := []struct {
		name     string
		entry    *mdns.ServiceEntry
		expected bool
	}{
		{
			name: "entry of the same cluster",
			entry: &mdns.ServiceEntry{
				Name:       "AccountsSystem-abcd1234._workstation._tcp.local.",
				Port:       port,
				InfoFields: []string{"name=AccountsSystem"},
			},
			expected: true,
		},
		{
			name: "entry announcing another port",
			entry: &mdns.ServiceEntry{
				Name:       "AccountsSystem-abcd1234._workstation._tcp.local.",
				Port:       4043,
				InfoFields: []string{"name=AccountsSystem"},
			},
			expected: false,
		},
		{
			name: "entry of another service",
			entry: &mdns.ServiceEntry{
				Name:       "AccountsSystem-abcd1234._printer._tcp.local.",
				Port:       port,
				InfoFields: []string{"name=AccountsSystem"},
			},
			expected: false,
		},
		{
			name: "entry of another cluster",
			entry: &mdns.ServiceEntry{
				Name:       "Other-abcd1234._workstation._tcp.local.",
				Port:       port,
				InfoFields: []string{"name=Other"},
			},
			expected: false,
		},
		{
			name: "entry without TXT records",
			entry: &mdns.ServiceEntry{
				Name: "AccountsSystem-abcd1234._workstation._tcp.local.",
				Port: port,
			},
			expected: false,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			assert.Equal(t, testCase.expected, provider.matches(testCase.entry))
		})
	}

	t.Run("With domain without the trailing dot", func(t *testing.T) {
		provider := NewDiscovery(&Config{
			Service:     service,
			ServiceName: serviceName,
			Domain:      "local",
			Port:        port,
		})

		assert.True(t, provider.matches(&mdns.ServiceEntry{
			Name:       "AccountsSystem-abcd1234._workstation._tcp.local.",
			Port:       port,
			InfoFields: []string{"name=AccountsSystem"},
		}))
	})
}

func TestAddresses(t *testing.T) {
	const port = 4042

	entry := &mdns.ServiceEntry{
		Name:         "AccountsSystem-abcd1234._workstation._tcp.local.",
		Port:         port,
		AddrV4:       net.ParseIP("10.0.0.2"),
		AddrV6IPAddr: &net.IPAddr{IP: net.ParseIP("fe80::1")},
	}

	t.Run("With IPv6 disabled", func(t *testing.T) {
		provider := NewDiscovery(&Config{Port: port})
		assert.Equal(t, []string{"10.0.0.2:4042"}, provider.addresses([]*mdns.ServiceEntry{entry}))
	})

	t.Run("With IPv6 enabled", func(t *testing.T) {
		ipv6 := true
		provider := NewDiscovery(&Config{Port: port, IPv6: &ipv6})

		expected := []string{"10.0.0.2:4042", "[fe80::1]:4042"}
		slices.Sort(expected)

		assert.Equal(t, expected, provider.addresses([]*mdns.ServiceEntry{entry}))
	})

	t.Run("With duplicated entries", func(t *testing.T) {
		provider := NewDiscovery(&Config{Port: port})
		assert.Equal(t, []string{"10.0.0.2:4042"}, provider.addresses([]*mdns.ServiceEntry{entry, entry}))
	})

	t.Run("With an entry carrying no address", func(t *testing.T) {
		provider := NewDiscovery(&Config{Port: port})
		assert.Empty(t, provider.addresses([]*mdns.ServiceEntry{{Name: "Empty._workstation._tcp.local.", Port: port}}))
	})
}

func TestAdvertisedAddresses(t *testing.T) {
	for _, ip := range advertisedAddresses() {
		assert.False(t, ip.IsLoopback(), "loopback address advertised: %s", ip)
	}
}

func TestTwoNodesShareServiceName(t *testing.T) {
	ports := dynaport.Get(1)
	port := ports[0]
	service := "_workstation._tcp"
	serviceName := "AccountsSystem"
	domain := "local."

	config := Config{
		Service:     service,
		ServiceName: serviceName,
		Domain:      domain,
		Port:        port,
	}

	first := NewDiscovery(&config)
	require.NoError(t, first.Initialize())
	require.NoError(t, first.Register())

	second := NewDiscovery(&config)
	require.NoError(t, second.Initialize())
	require.NoError(t, second.Register())

	// wait for both registrations to be completed
	pause.For(time.Second)

	entries, err := first.queryEntries()
	require.NoError(t, err)

	var names []string
	for _, entry := range entries {
		require.True(t, strings.HasPrefix(entry.Name, serviceName+"-"), "unexpected instance name: %s", entry.Name)
		names = append(names, entry.Name)
	}

	slices.Sort(names)
	names = slices.Compact(names)
	require.GreaterOrEqual(t, len(names), 2)

	require.NoError(t, first.Deregister())
	require.NoError(t, second.Deregister())
}

func TestWithLoggerOption(t *testing.T) {
	config := Config{
		Service:     "_workstation._tcp",
		ServiceName: "AccountsSystem",
		Domain:      "local.",
		Port:        4042,
	}

	provider := NewDiscovery(&config, WithLogger(log.DefaultLogger))
	assert.Equal(t, log.DefaultLogger, provider.logger)

	provider = NewDiscovery(&config)
	assert.Equal(t, log.DiscardLogger, provider.logger)
}
