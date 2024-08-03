/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package cluster

import (
	"fmt"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/log"
	testkit "github.com/tochemey/goakt/v2/mocks/discovery"
)

func TestDiscoveryProvider(t *testing.T) {
	t.Run("With Initialize: happy path", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		provider.
			On("Initialize").Return(nil)
		wrapper := &discoveryProvider{
			provider: provider,
			log:      log.DefaultLogger.StdLogger(),
		}

		err := wrapper.Initialize()
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With Initialize: failure", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		provider.
			On("Initialize").Return(errors.New("failed"))
		wrapper := &discoveryProvider{
			provider: provider,
			log:      log.DefaultLogger.StdLogger(),
		}

		err := wrapper.Initialize()
		assert.Error(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With Initialize: already done", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		provider.
			On("Initialize").Return(discovery.ErrAlreadyInitialized)
		wrapper := &discoveryProvider{
			provider: provider,
			log:      log.DefaultLogger.StdLogger(),
		}

		err := wrapper.Initialize()
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With SetConfig: happy path", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		provider.
			On("ID").Return("testDisco")
		// create the config
		options := map[string]any{
			"id": "testDisco",
		}
		// create the instance of the wrapper
		wrapper := &discoveryProvider{
			provider: provider,
			log:      log.DefaultLogger.StdLogger(),
		}
		// set the options
		err := wrapper.SetConfig(options)
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With SetConfig: id not set", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)

		// create the config
		options := map[string]any{}
		// create the instance of the wrapper
		wrapper := &discoveryProvider{
			provider: provider,
			log:      log.DefaultLogger.StdLogger(),
		}
		// set the options
		err := wrapper.SetConfig(options)
		assert.Error(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With SetConfig: invalid id set", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		provider.
			On("ID").Return("testDisco")
		// create the config
		options := map[string]any{
			"id": "fake",
		}
		// create the instance of the wrapper
		wrapper := &discoveryProvider{
			provider: provider,
			log:      log.DefaultLogger.StdLogger(),
		}
		// set the options
		err := wrapper.SetConfig(options)
		assert.Error(t, err)
		assert.EqualError(t, err, "invalid discovery provider id")
		provider.AssertExpectations(t)
	})

	t.Run("With Register: happy path", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		provider.
			On("Register").Return(nil)
		wrapper := &discoveryProvider{
			provider: provider,
			log:      log.DefaultLogger.StdLogger(),
		}

		err := wrapper.Register()
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With Register: provider not set", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		wrapper := &discoveryProvider{
			log: log.DefaultLogger.StdLogger(),
		}

		err := wrapper.Register()
		assert.Error(t, err)
		assert.EqualError(t, err, "discovery provider is not set")
		provider.AssertNotCalled(t, "Register")
	})
	t.Run("With Register: failure", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		provider.
			On("Register").Return(errors.New("failed to register"))
		wrapper := &discoveryProvider{
			log:      log.DefaultLogger.StdLogger(),
			provider: provider,
		}

		err := wrapper.Register()
		assert.Error(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With Deregister: happy path", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		provider.
			On("Deregister").Return(nil)
		wrapper := &discoveryProvider{
			provider: provider,
			log:      log.DefaultLogger.StdLogger(),
		}

		err := wrapper.Deregister()
		assert.NoError(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With Deregister: provider not set", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		wrapper := &discoveryProvider{
			log: log.DefaultLogger.StdLogger(),
		}

		err := wrapper.Deregister()
		assert.Error(t, err)
		assert.EqualError(t, err, "discovery provider is not set")
		provider.AssertNotCalled(t, "Deregister")
	})
	t.Run("With Deregister: failure", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		provider.
			On("Deregister").Return(errors.New("failed to register"))
		wrapper := &discoveryProvider{
			log:      log.DefaultLogger.StdLogger(),
			provider: provider,
		}

		err := wrapper.Deregister()
		assert.Error(t, err)
		provider.AssertExpectations(t)
	})
	t.Run("With DiscoverPeers: happy path", func(t *testing.T) {
		nodePorts := dynaport.Get(1)
		gossipPort := nodePorts[0]

		addrs := []string{
			fmt.Sprintf("127.0.0.1:%d", gossipPort),
		}
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		provider.
			On("DiscoverPeers").Return(addrs, nil)
		wrapper := &discoveryProvider{
			provider: provider,
			log:      log.DefaultLogger.StdLogger(),
		}

		actual, err := wrapper.DiscoverPeers()
		assert.NoError(t, err)
		assert.NotEmpty(t, actual)
		assert.Len(t, actual, 1)
		provider.AssertExpectations(t)
	})
	t.Run("With DiscoverPeers: provider not set", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		wrapper := &discoveryProvider{
			log: log.DefaultLogger.StdLogger(),
		}

		actual, err := wrapper.DiscoverPeers()
		assert.Empty(t, actual)
		assert.Error(t, err)
		assert.EqualError(t, err, "discovery provider is not set")
		provider.AssertNotCalled(t, "DiscoverPeers")
	})
	t.Run("With Close", func(t *testing.T) {
		// mock the underlying discovery provider
		provider := new(testkit.Provider)
		provider.EXPECT().Close().Return(nil)
		wrapper := &discoveryProvider{
			provider: provider,
			log:      log.DefaultLogger.StdLogger(),
		}

		err := wrapper.Close()
		assert.NoError(t, err)
	})
}
