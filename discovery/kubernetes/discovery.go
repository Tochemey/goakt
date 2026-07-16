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
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	goset "github.com/deckarep/golang-set/v2"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/internal/locker"
)

const discoverPeersTimeout = 30 * time.Second

// ErrNoPodsAvailable is returned by DiscoverPeers when the pod listing yields no
// usable peer address. Once the kubelet reports the calling pod Running, that pod
// must at minimum discover itself, so an empty result indicates Kubernetes API
// status lag. Callers should treat it as retryable instead of bootstrapping a
// standalone cluster.
var ErrNoPodsAvailable = errors.New("no running pods with a usable address found")

// Discovery represents the kubernetes discovery
type Discovery struct {
	_      locker.NoCopy
	config *Config
	client kubernetes.Interface
	mu     sync.Mutex

	// labelSelector is the cached pod label selector string built from config.PodLabels
	labelSelector string
	// states whether the actor system has started or not
	initialized *atomic.Bool

	// Test seams: overridden in unit tests so Register can run without a live cluster.
	inClusterConfig func() (*rest.Config, error)
	newForConfig    func(*rest.Config) (kubernetes.Interface, error)
}

// enforce compilation error
var _ discovery.Provider = &Discovery{}

// NewDiscovery returns an instance of the kubernetes discovery provider
func NewDiscovery(config *Config) *Discovery {
	return &Discovery{
		mu:              sync.Mutex{},
		initialized:     atomic.NewBool(false),
		config:          config,
		inClusterConfig: rest.InClusterConfig,
		newForConfig: func(c *rest.Config) (kubernetes.Interface, error) {
			return kubernetes.NewForConfig(c)
		},
	}
}

// ID returns the discovery provider id
func (d *Discovery) ID() string {
	return discovery.ProviderKubernetes
}

// Initialize initializes the plugin: registers some internal data structures, clients etc.
func (d *Discovery) Initialize() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	if err := d.config.Validate(); err != nil {
		return err
	}

	d.labelSelector = labels.SelectorFromSet(d.config.PodLabels).String()
	return nil
}

// Register registers this node to a service discovery directory.
func (d *Discovery) Register() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.initialized.Load() {
		return discovery.ErrAlreadyRegistered
	}

	config, err := d.inClusterConfig()
	if err != nil {
		return fmt.Errorf("failed to get the in-cluster config of the kubernetes provider: %w", err)
	}

	client, err := d.newForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create the kubernetes client api: %w", err)
	}

	d.client = client
	d.initialized.Store(true)
	return nil
}

// Deregister removes this node from a service discovery directory.
func (d *Discovery) Deregister() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.initialized.Load() {
		return discovery.ErrNotInitialized
	}
	d.initialized.Store(false)
	return nil
}

// DiscoverPeers returns a list of known nodes.
//
// Candidate pods are matched by the configured labels and status.phase=Running.
// The Ready condition is deliberately not required: on a cold start no pod is
// Ready until its actor system has joined the cluster, so gating discovery on
// readiness would prevent any cluster from ever forming. Liveness of candidates
// is delegated to the memberlist failure detector. Terminating pods (which keep
// status.phase=Running until they exit) and pods without an assigned IP are
// excluded. An empty filtered result returns ErrNoPodsAvailable so that the
// cluster engine retries the join instead of bootstrapping a standalone cluster.
func (d *Discovery) DiscoverPeers() ([]string, error) {
	if !d.initialized.Load() {
		return nil, discovery.ErrNotInitialized
	}

	ctx, cancel := context.WithTimeout(context.Background(), discoverPeersTimeout)
	defer cancel()

	pods, err := d.client.CoreV1().Pods(d.config.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: d.labelSelector,
		FieldSelector: "status.phase=" + string(corev1.PodRunning),
	})

	if err != nil {
		return nil, err
	}

	addresses := goset.NewSet[string]()

	for _, pod := range pods.Items {
		// Terminating pods keep status.phase=Running until their containers exit;
		// skip them so rolling updates do not feed dying IPs to memberlist.
		if pod.DeletionTimestamp != nil {
			continue
		}

		// A Running pod may not have an IP assigned yet in the API server's view.
		if pod.Status.PodIP == "" {
			continue
		}

		if port, ok := discoveryPort(&pod, d.config.DiscoveryPortName); ok {
			addresses.Add(net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(int(port))))
		}
	}

	if addresses.Cardinality() == 0 {
		return nil, ErrNoPodsAvailable
	}

	return addresses.ToSlice(), nil
}

// discoveryPort returns the container port named portName from the pod spec.
// Kubernetes enforces that port names are unique across all containers of a pod,
// so at most one match exists.
func discoveryPort(pod *corev1.Pod, portName string) (int32, bool) {
	for _, container := range pod.Spec.Containers {
		for _, port := range container.Ports {
			if port.Name == portName {
				return port.ContainerPort, true
			}
		}
	}

	return 0, false
}

// Close closes the provider
func (d *Discovery) Close() error {
	return nil
}
