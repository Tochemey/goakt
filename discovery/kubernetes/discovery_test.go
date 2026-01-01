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

package kubernetes

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	testclient "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/tochemey/goakt/v3/discovery"
)

const (
	gossipPortName   = "gossip-port"
	peersPortName    = "peers-port"
	remotingPortName = "remoting-port"
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
		assert.Equal(t, "kubernetes", provider.ID())
	})
	t.Run("With DiscoverPeers", func(t *testing.T) {
		// create the namespace
		ns := "test"
		appName := "test"
		ts1 := time.Now()
		ts2 := time.Now()

		labels := map[string]string{
			"app.kubernetes.io/part-of":   "some-part-of",
			"app.kubernetes.io/component": "component",
			"app.kubernetes.io/name":      appName,
		}

		config := &Config{
			Namespace:         "test",
			DiscoveryPortName: gossipPortName,
			RemotingPortName:  remotingPortName,
			PeersPortName:     peersPortName,
			PodLabels:         labels,
		}

		// create some bunch of mock pods
		pods := []runtime.Object{
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod1",
					Namespace: ns,
					Labels:    labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          gossipPortName,
									ContainerPort: 3379,
								},
								{
									Name:          peersPortName,
									ContainerPort: 3380,
								},
								{
									Name:          remotingPortName,
									ContainerPort: 9000,
								},
							},
						},
					},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					PodIP: "10.0.0.23",
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
					StartTime: &metav1.Time{
						Time: ts1,
					},
				},
			},
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod2",
					Namespace: ns,
					Labels:    labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          gossipPortName,
									ContainerPort: 3379,
								},
								{
									Name:          peersPortName,
									ContainerPort: 3380,
								},
								{
									Name:          remotingPortName,
									ContainerPort: 9000,
								},
							},
						},
					},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					PodIP: "10.0.0.24",
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
					StartTime: &metav1.Time{
						Time: ts2,
					},
				},
			},
		}
		// create a mock kubernetes client
		client := testclient.NewClientset(pods...)
		// create the kubernetes discovery provider
		provider := Discovery{
			client:      client,
			initialized: atomic.NewBool(true),
			config:      config,
		}
		// discover some nodes
		actual, err := provider.DiscoverPeers()
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.NotEmpty(t, actual)
		require.Len(t, actual, 2)

		expected := []string{
			"10.0.0.23:3379",
			"10.0.0.24:3379",
		}

		assert.ElementsMatch(t, expected, actual)
		assert.NoError(t, provider.Close())
	})
	t.Run("With DiscoverPeers: not initialized", func(t *testing.T) {
		provider := NewDiscovery(nil)
		peers, err := provider.DiscoverPeers()
		assert.Error(t, err)
		assert.Empty(t, peers)
		assert.EqualError(t, err, discovery.ErrNotInitialized.Error())
	})
	t.Run("With Initialize", func(t *testing.T) {
		// create the various config option
		namespace := "default"
		labels := map[string]string{
			"app.kubernetes.io/part-of":   "some-part-of",
			"app.kubernetes.io/component": "component",
			"app.kubernetes.io/name":      "test",
		}

		config := &Config{
			Namespace:         namespace,
			DiscoveryPortName: gossipPortName,
			RemotingPortName:  remotingPortName,
			PeersPortName:     peersPortName,
			PodLabels:         labels,
		}

		// create the instance of provider
		provider := NewDiscovery(config)
		assert.NoError(t, provider.Initialize())
	})
	t.Run("With Initialize: already initialized", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery(nil)
		provider.initialized = atomic.NewBool(true)
		assert.Error(t, provider.Initialize())
	})
	t.Run("With Deregister", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery(nil)
		// for the sake of the test
		provider.initialized = atomic.NewBool(true)
		assert.NoError(t, provider.Deregister())
	})
	t.Run("With Deregister when not initialized", func(t *testing.T) {
		// create the instance of provider
		provider := NewDiscovery(nil)
		// for the sake of the test
		provider.initialized = atomic.NewBool(false)
		err := provider.Deregister()
		assert.Error(t, err)
		assert.EqualError(t, err, discovery.ErrNotInitialized.Error())
	})
}

func TestRegister(t *testing.T) {
	t.Run("already registered", func(t *testing.T) {
		provider := NewDiscovery(nil)
		provider.initialized = atomic.NewBool(true)

		err := provider.Register()
		require.Error(t, err)
		require.EqualError(t, err, discovery.ErrAlreadyRegistered.Error())
	})

	t.Run("in-cluster config error", func(t *testing.T) {
		// No cluster environment present; InClusterConfig should fail
		provider := NewDiscovery(nil)

		err := provider.Register()
		require.Error(t, err)
		// Check error context while staying resilient to client-go message details
		require.ErrorContains(t, err, "failed to get the in-cluster config")
		// Ensure client is not set and not marked initialized
		require.Nil(t, provider.client)
		require.False(t, provider.initialized.Load())
	})
}

func TestDiscoverPeersFailures(t *testing.T) {
	newConfig := func() *Config {
		return &Config{
			Namespace:         "test",
			DiscoveryPortName: gossipPortName,
			RemotingPortName:  remotingPortName,
			PeersPortName:     peersPortName,
			PodLabels: map[string]string{
				"app.kubernetes.io/name": "test",
			},
		}
	}

	t.Run("kubernetes client failure", func(t *testing.T) {
		config := newConfig()
		client := testclient.NewSimpleClientset()
		client.PrependReactor("list", "pods", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, errors.New("list failure")
		})

		provider := Discovery{
			client:      client,
			initialized: atomic.NewBool(true),
			config:      config,
		}

		peers, err := provider.DiscoverPeers()
		require.Error(t, err)
		require.Nil(t, peers)
	})

	t.Run("filters out non ready pods", func(t *testing.T) {
		config := newConfig()
		ns := "test"

		labels := map[string]string{
			"app.kubernetes.io/name": "test",
		}

		pods := []runtime.Object{
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pending-pod",
					Namespace: ns,
					Labels:    labels,
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodPending,
					PodIP: "10.0.0.30",
				},
			},
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "not-ready",
					Namespace: ns,
					Labels:    labels,
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					PodIP: "10.0.0.31",
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionFalse,
						},
					},
				},
			},
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-discovery-port",
					Namespace: ns,
					Labels:    labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Ports: []corev1.ContainerPort{
								{
									Name:          "metrics",
									ContainerPort: 9090,
								},
							},
						},
					},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					PodIP: "10.0.0.32",
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
		}

		client := testclient.NewClientset(pods...)
		provider := Discovery{
			client:      client,
			initialized: atomic.NewBool(true),
			config:      config,
		}

		peers, err := provider.DiscoverPeers()
		require.NoError(t, err)
		assert.Empty(t, peers)
	})
}
