package mdns

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/discovery"
	"github.com/travisjeffery/go-dynaport"
	"go.uber.org/atomic"
)

func TestDiscovery(t *testing.T) {
	t.Run("With new instance", func(t *testing.T) {
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
		assert.Equal(t, "mdns", provider.ID())
	})
	t.Run("With SetConfig", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := strconv.Itoa(ports[0])
		serviceType := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local."
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			Service:     serviceType,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
			IPv6:        false,
		}

		// set config
		assert.NoError(t, provider.SetConfig(config))
	})
	t.Run("With SetConfig: already initialized", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := strconv.Itoa(ports[0])
		serviceType := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local."
		// create the instance of provider
		provider := NewDiscovery()
		provider.isInitialized = atomic.NewBool(true)
		// create the config
		config := discovery.Config{
			Service:     serviceType,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
			IPv6:        false,
		}

		// set config
		err := provider.SetConfig(config)
		assert.Error(t, err)
		assert.EqualError(t, err, discovery.ErrAlreadyInitialized.Error())
	})
	t.Run("With Initialize", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := strconv.Itoa(ports[0])
		serviceType := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local."
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			Service:     serviceType,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
			IPv6:        false,
		}
		// set config
		assert.NoError(t, provider.SetConfig(config))
		assert.NoError(t, provider.Initialize())
	})
	t.Run("With Initialize: already initialized", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		provider.isInitialized = atomic.NewBool(true)
		assert.Error(t, provider.Initialize())
	})
	t.Run("With Register", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := strconv.Itoa(ports[0])
		serviceType := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local"
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			Service:     serviceType,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
			IPv6:        false,
		}
		require.NoError(t, provider.SetConfig(config))
		require.NoError(t, provider.Initialize())
		require.NoError(t, provider.Register())

		time.Sleep(time.Second)
		require.True(t, provider.isInitialized.Load())
		require.NoError(t, provider.Deregister())
		time.Sleep(time.Second)
		assert.False(t, provider.isInitialized.Load())
	})
	t.Run("With Deregister", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery()
		// for the sake of the test
		provider.isInitialized = atomic.NewBool(true)
		assert.NoError(t, provider.Deregister())
	})
	t.Run("With DiscoverPeers", func(t *testing.T) {
		// create the various config option
		ports := dynaport.Get(1)
		port := ports[0]
		service := "_workstation._tcp"
		serviceName := "AccountsSystem"
		domain := "local."
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			Service:     service,
			ServiceName: serviceName,
			Domain:      domain,
			Port:        port,
			IPv6:        false,
		}
		require.NoError(t, provider.SetConfig(config))
		require.NoError(t, provider.Initialize())
		require.NoError(t, provider.Register())

		// wait for registration to be completed
		time.Sleep(time.Second)
		require.True(t, provider.isInitialized.Load())

		// discover peers
		peers, err := provider.DiscoverPeers()
		require.NoError(t, err)
		require.NotEmpty(t, peers)
		require.Len(t, peers, 1)

		assert.NoError(t, provider.Deregister())
	})
}
