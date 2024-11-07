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

package kubernetes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	testclient "k8s.io/client-go/kubernetes/fake"

	"github.com/tochemey/goakt/v2/discovery"
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
		actorSystemName := "test"
		appName := "test"
		ts1 := time.Now()
		ts2 := time.Now()

		config := &Config{
			Namespace:         "test",
			ActorSystemName:   "test",
			ApplicationName:   "test",
			DiscoveryPortName: gossipPortName,
			RemotingPortName:  remotingPortName,
			PeersPortName:     peersPortName,
		}

		// create some bunch of mock pods
		pods := []runtime.Object{
			&corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod1",
					Namespace: ns,
					Labels: map[string]string{
						"app.kubernetes.io/part-of":   actorSystemName,
						"app.kubernetes.io/component": appName,
						"app.kubernetes.io/name":      appName,
					},
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
					Labels: map[string]string{
						"app.kubernetes.io/part-of":   actorSystemName,
						"app.kubernetes.io/component": appName,
						"app.kubernetes.io/name":      appName,
					},
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
		client := testclient.NewSimpleClientset(pods...)
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
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"

		config := &Config{
			Namespace:         namespace,
			ActorSystemName:   actorSystemName,
			ApplicationName:   applicationName,
			DiscoveryPortName: gossipPortName,
			RemotingPortName:  remotingPortName,
			PeersPortName:     peersPortName,
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
