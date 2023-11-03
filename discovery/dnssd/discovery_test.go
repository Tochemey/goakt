/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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

package dnssd

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/discovery"
	"go.uber.org/atomic"
)

func TestDiscovery(t *testing.T) {
	t.Run("With a new instance", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
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
		provider := NewDiscovery()
		require.NotNil(t, provider)
		assert.Equal(t, "dns-sd", provider.ID())
	})
	t.Run("With SetConfig", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			DomainName: "google.com",
			IPv6:       false,
		}
		// set config
		assert.NoError(t, provider.SetConfig(config))
	})
	t.Run("With SetConfig dns name not set", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			DomainName: "",
			IPv6:       false,
		}
		// set config
		assert.Error(t, provider.SetConfig(config))
	})
	t.Run("With SetConfig dns name not provided", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			IPv6: false,
		}
		// set config
		assert.Error(t, provider.SetConfig(config))
	})
	t.Run("With SetConfig ipv6 not provided", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			DomainName: "google.com",
		}
		// set config
		assert.Error(t, provider.SetConfig(config))
	})
	t.Run("With SetConfig ipv6 wrong boolean value", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			DomainName: "google.com",
			IPv6:       "wrong-value",
		}
		// set config
		assert.Error(t, provider.SetConfig(config))
	})
	t.Run("With SetConfig: already initialized", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		provider.initialized = atomic.NewBool(true)
		// create the config
		config := discovery.Config{
			DomainName: "google.com",
			IPv6:       false,
		}
		// set config
		err := provider.SetConfig(config)
		assert.Error(t, err)
		assert.EqualError(t, err, discovery.ErrAlreadyInitialized.Error())
	})
	t.Run("With Initialize", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			DomainName: "google.com",
			IPv6:       false,
		}
		// set config
		assert.NoError(t, provider.SetConfig(config))
		assert.NoError(t, provider.Initialize())
	})
	t.Run("With Initialize: already initialized", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		provider.initialized = atomic.NewBool(true)
		assert.Error(t, provider.Initialize())
	})
	t.Run("With Register", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			DomainName: "google.com",
			IPv6:       false,
		}
		require.NoError(t, provider.SetConfig(config))
		require.NoError(t, provider.Initialize())
		require.NoError(t, provider.Register())

		time.Sleep(time.Second)
		require.True(t, provider.initialized.Load())
		require.NoError(t, provider.Deregister())
		time.Sleep(time.Second)
		assert.False(t, provider.initialized.Load())
	})
	t.Run("With Register when already registered", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			DomainName: "google.com",
			IPv6:       false,
		}
		require.NoError(t, provider.SetConfig(config))
		require.NoError(t, provider.Initialize())
		require.NoError(t, provider.Register())

		time.Sleep(time.Second)
		require.True(t, provider.initialized.Load())
		err := provider.Register()
		require.Error(t, err)
		require.EqualError(t, err, discovery.ErrAlreadyRegistered.Error())
		require.NoError(t, provider.Deregister())
		time.Sleep(time.Second)
		assert.False(t, provider.initialized.Load())
	})
	t.Run("With Deregister", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		// for the sake of the test
		provider.initialized = atomic.NewBool(true)
		assert.NoError(t, provider.Deregister())
	})
	t.Run("With Deregister when not initialized", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		// for the sake of the test
		provider.initialized = atomic.NewBool(false)
		err := provider.Deregister()
		assert.Error(t, err)
		assert.EqualError(t, err, discovery.ErrNotInitialized.Error())
	})
	t.Run("With DiscoverPeers: not initialized", func(t *testing.T) {
		provider := NewDiscovery()
		peers, err := provider.DiscoverPeers()
		assert.Error(t, err)
		assert.Empty(t, peers)
		assert.EqualError(t, err, discovery.ErrNotInitialized.Error())
	})
	t.Run("With DiscoverPeers", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		config := discovery.Config{
			DomainName: "google.com",
			IPv6:       false,
		}
		require.NoError(t, provider.SetConfig(config))
		require.NoError(t, provider.Initialize())
		require.NoError(t, provider.Register())

		// wait for registration to be completed
		time.Sleep(time.Second)
		require.True(t, provider.initialized.Load())

		// discover peers
		peers, err := provider.DiscoverPeers()
		require.NoError(t, err)
		require.NotEmpty(t, peers)
		require.NoError(t, validateAddrs(peers))
		assert.NoError(t, provider.Deregister())
	})
}

func validateAddrs(addrs []string) error {
	for _, addr := range addrs {
		ipaddr := net.ParseIP(addr)
		if ipaddr == nil {
			return fmt.Errorf("invalid IP address format: %s", addr)
		}
	}
	return nil
}
