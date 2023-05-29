package kubernetes

import (
	"context"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/slices"
	"go.uber.org/atomic"
	"go.uber.org/goleak"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	testclient "k8s.io/client-go/kubernetes/fake"
)

func TestKubernetesProvider(t *testing.T) {
	t.Run("With new instance", func(t *testing.T) {
		// create a logger
		logger := log.DefaultLogger
		// create the instance of provider
		provider := New(logger)
		require.NotNil(t, provider)
		// assert that provider implements the Discovery interface
		// this is a cheap test
		// assert the type of svc
		assert.IsType(t, &Discovery{}, provider)
		var p interface{} = provider
		_, ok := p.(discovery.Discovery)
		assert.True(t, ok)
	})
	t.Run("With id assertion", func(t *testing.T) {
		// cheap test
		// create a logger
		logger := log.DefaultLogger
		// create the instance of provider
		provider := New(logger)
		require.NotNil(t, provider)
		assert.Equal(t, "kubernetes", provider.ID())
	})
	t.Run("With Nodes discovered", func(t *testing.T) {
		// create the go context
		ctx := context.TODO()
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
									Name:          "clients-port",
									ContainerPort: 3379,
								},
								{
									Name:          "peers-port",
									ContainerPort: 3380,
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
									Name:          "clients-port",
									ContainerPort: 4379,
								},
								{
									Name:          "peers-port",
									ContainerPort: 4380,
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
		actual, err := provider.Nodes(ctx)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.NotEmpty(t, actual)
		require.Len(t, actual, 2)

		expected := []*discovery.Node{
			{
				Name:      "pod1",
				Host:      "10.0.0.23",
				StartTime: ts1.UnixMilli(),
				Ports: map[string]int32{
					"clients-port": 3379,
					"peers-port":   3380,
				},
				IsRunning: true,
			},
			{
				Name:      "pod2",
				Host:      "10.0.0.24",
				StartTime: ts2.UnixMilli(),
				Ports: map[string]int32{
					"clients-port": 4379,
					"peers-port":   4380,
				},
				IsRunning: true,
			},
		}

		// sort both actual and expected
		sort.SliceStable(actual, func(i, j int) bool {
			return actual[i].Name < actual[j].Name
		})
		sort.SliceStable(expected, func(i, j int) bool {
			return expected[i].Name < expected[j].Name
		})
		assert.ElementsMatch(t, expected, actual)
	})
	t.Run("With Earliest node", func(t *testing.T) {
		// create the go context
		ctx := context.TODO()
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
									Name:          "clients-port",
									ContainerPort: 3379,
								},
								{
									Name:          "peers-port",
									ContainerPort: 3380,
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
									Name:          "clients-port",
									ContainerPort: 4379,
								},
								{
									Name:          "peers-port",
									ContainerPort: 4380,
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
		actual, err := provider.EarliestNode(ctx)
		require.NoError(t, err)
		require.NotNil(t, actual)

		expected := &discovery.Node{
			Name:      "pod1",
			Host:      "10.0.0.23",
			StartTime: ts1.UnixMilli(),
			Ports: map[string]int32{
				"clients-port": 3379,
				"peers-port":   3380,
			},
			IsRunning: true,
		}
		assert.True(t, cmp.Equal(expected, actual))
	})
	t.Run("With Watch nodes", func(t *testing.T) {
		// detest leaking go routines
		defer goleak.VerifyNone(t, goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"))
		// create the go context
		ctx := context.TODO()
		// create the namespace
		ns := "test"
		actorSystemName := "test"
		appName := "test"
		ts1 := time.Now()
		ts2 := time.Now()
		logger := log.New(log.DebugLevel, os.Stdout)

		// create a mock kubernetes client
		client := testclient.NewSimpleClientset()

		// create the kubernetes discovery provider
		provider := Discovery{
			client:        client,
			isInitialized: atomic.NewBool(true),
			logger:        logger,
			publicChan:    make(chan discovery.Event, 2),
			stopChan:      make(chan struct{}, 1),
			option: &option{
				NameSpace:       ns,
				ActorSystemName: actorSystemName,
				ApplicationName: appName,
			},
		}

		// start watching
		stopSig := make(chan struct{}, 1)
		// close the channels
		defer close(stopSig)

		watchChan, err := provider.Watch(ctx)
		assert.NoError(t, err)
		// watchedNodes handler
		watchedNodes := slices.NewConcurrentSlice[*discovery.Node]()
		seen := make(map[string]bool)
		mu := sync.Mutex{}
		// create pods watch handler
		go func() {
			for {
				select {
				case <-stopSig:
					return
				case event := <-watchChan:
					switch x := event.(type) {
					case *discovery.NodeAdded:
						logger.Debugf("test node=%s added", x.Node.Name)
						mu.Lock()
						if _, ok := seen[x.Node.Name]; !ok {
							watchedNodes.Append(x.Node)
							seen[x.Node.Name] = true
						}
						mu.Unlock()
					case *discovery.NodeModified:
						logger.Debugf("test node=%s modified", x.Node.Name)
						mu.Lock()
						if _, ok := seen[x.Node.Name]; !ok {
							watchedNodes.Append(x.Node)
							seen[x.Node.Name] = true
						}
						mu.Unlock()
					}
				}
			}
		}()

		// create some bunch of mock pods
		pods := []*corev1.Pod{
			{
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
									Name:          "clients-port",
									ContainerPort: 3379,
								},
								{
									Name:          "peers-port",
									ContainerPort: 3380,
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
			{
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
									Name:          "clients-port",
									ContainerPort: 4379,
								},
								{
									Name:          "peers-port",
									ContainerPort: 4380,
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
		// let us add some pods
		for _, pod := range pods {
			res, err := client.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{})
			require.NoError(t, err)
			require.NotNil(t, res)
		}

		// let us wait for pods synchronization
		// this is because the default kubernetes watcher cache synchronization used is 1s
		// any value greater than 1s should work here
		time.Sleep(2 * time.Second)

		require.NotNil(t, watchedNodes)
		require.EqualValues(t, 2, watchedNodes.Len())

		expected := []*discovery.Node{
			{
				Name:      "pod1",
				Host:      "10.0.0.23",
				StartTime: ts1.UnixMilli(),
				Ports: map[string]int32{
					"clients-port": 3379,
					"peers-port":   3380,
				},
				IsRunning: true,
			},
			{
				Name:      "pod2",
				Host:      "10.0.0.24",
				StartTime: ts2.UnixMilli(),
				Ports: map[string]int32{
					"clients-port": 4379,
					"peers-port":   4380,
				},
				IsRunning: true,
			},
		}

		actual := make([]*discovery.Node, 0, watchedNodes.Len())
		for node := range watchedNodes.Iter() {
			actual = append(actual, node.Value)
		}

		// sort both actual and expected
		sort.SliceStable(actual, func(i, j int) bool {
			return actual[i].Name < actual[j].Name
		})
		sort.SliceStable(expected, func(i, j int) bool {
			return expected[i].Name < expected[j].Name
		})
		assert.ElementsMatch(t, expected, actual)
		assert.NoError(t, provider.Stop())
	})
	t.Run("With Watch with deletion", func(t *testing.T) {
		// detest leaking go routines
		defer goleak.VerifyNone(t, goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"))
		// create the go context
		ctx := context.TODO()
		// create the namespace
		ns := "test"
		actorSystemName := "test"
		appName := "test"
		ts1 := time.Now()
		ts2 := time.Now()
		logger := log.New(log.DebugLevel, os.Stdout)

		// create a mock kubernetes client
		client := testclient.NewSimpleClientset()

		// create the kubernetes discovery provider
		provider := Discovery{
			client:        client,
			isInitialized: atomic.NewBool(true),
			logger:        logger,
			publicChan:    make(chan discovery.Event, 2),
			stopChan:      make(chan struct{}, 1),
			option: &option{
				NameSpace:       ns,
				ActorSystemName: actorSystemName,
				ApplicationName: appName,
			},
		}

		// start watching
		stopSig := make(chan struct{}, 1)
		// close the channels
		defer close(stopSig)

		// start watching pods
		watchChan, err := provider.Watch(ctx)
		assert.NoError(t, err)
		// nodes handler
		watchedNodes := slices.NewConcurrentSlice[*discovery.Node]()
		seen := make(map[string]bool)
		mu := sync.Mutex{}
		// create pods watch handler
		go func() {
			for {
				select {
				case <-stopSig:
					return
				case event := <-watchChan:
					switch x := event.(type) {
					case *discovery.NodeModified:
					// pass
					case *discovery.NodeAdded:
						// pass
					case *discovery.NodeRemoved:
						logger.Debugf("test node=%s removed", x.Node.Name)
						mu.Lock()
						if _, ok := seen[x.Node.Name]; !ok {
							watchedNodes.Append(x.Node)
							seen[x.Node.Name] = true
						}
						mu.Unlock()
					}
				}
			}
		}()

		// create some bunch of mock pods
		pods := []*corev1.Pod{
			{
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
									Name:          "clients-port",
									ContainerPort: 3379,
								},
								{
									Name:          "peers-port",
									ContainerPort: 3380,
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
			{
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
									Name:          "clients-port",
									ContainerPort: 4379,
								},
								{
									Name:          "peers-port",
									ContainerPort: 4380,
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
		// let us add some pods
		for _, pod := range pods {
			res, err := client.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{})
			require.NoError(t, err)
			require.NotNil(t, res)
		}

		// let us wait for pods synchronization
		// this is because the default kubernetes watcher cache synchronization used is 1s
		// any value greater than 1s should work here
		time.Sleep(2 * time.Second)

		// let us delete pod pod2
		err = client.CoreV1().Pods(ns).Delete(ctx, "pod2", metav1.DeleteOptions{})
		require.NoError(t, err)

		// let us wait for pods synchronization
		// this is because the default kubernetes watcher cache synchronization used is 1s
		// any value greater than 1s should work here
		time.Sleep(2 * time.Second)

		require.NotNil(t, watchedNodes)
		require.EqualValues(t, 1, watchedNodes.Len())

		expected := []*discovery.Node{
			{
				Name:      "pod2",
				Host:      "10.0.0.24",
				StartTime: ts2.UnixMilli(),
				Ports: map[string]int32{
					"clients-port": 4379,
					"peers-port":   4380,
				},
				IsRunning: true,
			},
		}

		actual := make([]*discovery.Node, 0, watchedNodes.Len())
		for node := range watchedNodes.Iter() {
			actual = append(actual, node.Value)
		}

		// sort both actual and expected
		sort.SliceStable(actual, func(i, j int) bool {
			return actual[i].Name < actual[j].Name
		})
		sort.SliceStable(expected, func(i, j int) bool {
			return expected[i].Name < expected[j].Name
		})
		assert.ElementsMatch(t, expected, actual)
		assert.NoError(t, provider.Stop())
	})
}
