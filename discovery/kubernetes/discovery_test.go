package kubernetes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/discovery"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	testclient "k8s.io/client-go/kubernetes/fake"
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
		assert.Equal(t, "kubernetes", provider.ID())
	})
	t.Run("With DiscoverPeers", func(t *testing.T) {
		// create the namespace
		ns := "test"
		actorSystemName := "test"
		appName := "test"
		ts1 := time.Now()
		ts2 := time.Now()

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
									Name:          GossipPortName,
									ContainerPort: 3379,
								},
								{
									Name:          ClusterPortName,
									ContainerPort: 3380,
								},
								{
									Name:          RemotingPortName,
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
									Name:          GossipPortName,
									ContainerPort: 3379,
								},
								{
									Name:          ClusterPortName,
									ContainerPort: 3380,
								},
								{
									Name:          RemotingPortName,
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
			client:        client,
			isInitialized: atomic.NewBool(true),
			option: &option{
				NameSpace:       ns,
				ActorSystemName: actorSystemName,
				ApplicationName: appName,
			},
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
	})
	t.Run("With SetConfig", func(t *testing.T) {
		// create the various config option
		namespace := "default"
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			Namespace:       namespace,
		}

		// set config
		assert.NoError(t, provider.SetConfig(config))
	})
	t.Run("With Initialize", func(t *testing.T) {
		// create the various config option
		namespace := "default"
		applicationName := "accounts"
		actorSystemName := "AccountsSystem"
		// create the instance of provider
		provider := NewDiscovery()
		// create the config
		config := discovery.Config{
			ApplicationName: applicationName,
			ActorSystemName: actorSystemName,
			Namespace:       namespace,
		}

		// set config
		assert.NoError(t, provider.SetConfig(config))
		assert.NoError(t, provider.Initialize())
	})
}
