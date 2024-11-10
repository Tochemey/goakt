/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package mdns

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/internal/lib"
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
		var p interface{} = provider
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

		lib.Pause(time.Second)
		require.True(t, provider.initialized.Load())
		require.NoError(t, provider.Deregister())
		lib.Pause(time.Second)
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

		lib.Pause(time.Second)
		require.True(t, provider.initialized.Load())
		err := provider.Register()
		require.Error(t, err)
		require.EqualError(t, err, discovery.ErrAlreadyRegistered.Error())
		require.NoError(t, provider.Deregister())
		lib.Pause(time.Second)
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
		lib.Pause(time.Second)
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
		lib.Pause(time.Second)
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
