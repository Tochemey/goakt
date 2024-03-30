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

package kubernetes

import (
	"context"
	"net"
	"strconv"
	"sync"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/pkg/errors"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/strings/slices"

	"github.com/tochemey/goakt/discovery"
)

const (
	Namespace        string = "namespace"         // Namespace specifies the kubernetes namespace
	ActorSystemName         = "actor_system_name" // ActorSystemName specifies the actor system name
	ApplicationName         = "app_name"          // ApplicationName specifies the application name. This often matches the actor system name
	GossipPortName          = "gossip-port"
	ClusterPortName         = "cluster-port"
	RemotingPortName        = "remoting-port"
)

// discoConfig represents the kubernetes provider discoConfig
type discoConfig struct {
	// Provider specifies the provider name
	Provider string
	// NameSpace specifies the namespace
	NameSpace string
	// The actor system name
	ActorSystemName string
	// ApplicationName specifies the running application
	ApplicationName string
}

// Discovery represents the kubernetes discovery
type Discovery struct {
	option *discoConfig
	client kubernetes.Interface
	mu     sync.Mutex

	stopChan chan struct{}
	// states whether the actor system has started or not
	initialized *atomic.Bool
}

// enforce compilation error
var _ discovery.Provider = &Discovery{}

// NewDiscovery returns an instance of the kubernetes discovery provider
func NewDiscovery() *Discovery {
	// create an instance of
	discovery := &Discovery{
		mu:          sync.Mutex{},
		stopChan:    make(chan struct{}, 1),
		initialized: atomic.NewBool(false),
		option:      &discoConfig{},
	}

	return discovery
}

// ID returns the discovery provider id
func (d *Discovery) ID() string {
	return "kubernetes"
}

// Initialize initializes the plugin: registers some internal data structures, clients etc.
func (d *Discovery) Initialize() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	if d.option.Provider == "" {
		d.option.Provider = d.ID()
	}

	return nil
}

// SetConfig registers the underlying discovery configuration
func (d *Discovery) SetConfig(meta discovery.Config) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	return d.setConfig(meta)
}

// Register registers this node to a service discovery directory.
func (d *Discovery) Register() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.initialized.Load() {
		return discovery.ErrAlreadyRegistered
	}

	config, err := rest.InClusterConfig()
	if err != nil {
		return errors.Wrap(err, "failed to get the in-cluster config of the kubernetes provider")
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return errors.Wrap(err, "failed to create the kubernetes client api")
	}

	d.client = client
	d.initialized = atomic.NewBool(true)
	return nil
}

// Deregister removes this node from a service discovery directory.
func (d *Discovery) Deregister() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.initialized.Load() {
		return discovery.ErrNotInitialized
	}
	d.initialized = atomic.NewBool(false)
	close(d.stopChan)
	return nil
}

// DiscoverPeers returns a list of known nodes.
func (d *Discovery) DiscoverPeers() ([]string, error) {
	if !d.initialized.Load() {
		return nil, discovery.ErrNotInitialized
	}

	// let us create the pod labels map
	// TODO: make sure to document it on k8 discovery
	podLabels := map[string]string{
		"app.kubernetes.io/part-of":   d.option.ActorSystemName,
		"app.kubernetes.io/component": d.option.ApplicationName, // TODO: redefine it
		"app.kubernetes.io/name":      d.option.ApplicationName,
	}

	ctx := context.Background()

	pods, err := d.client.CoreV1().Pods(d.option.NameSpace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(podLabels).String(),
	})

	if err != nil {
		return nil, err
	}

	validPortNames := []string{ClusterPortName, GossipPortName, RemotingPortName}

	// define the addresses list
	addresses := goset.NewSet[string]()

MainLoop:
	for _, pod := range pods.Items {
		pod := pod

		if pod.Status.Phase != corev1.PodRunning {
			continue MainLoop
		}
		// If there is a Ready condition available, we need that to be true.
		// If no ready condition is set, then we accept this pod regardless.
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady && condition.Status != corev1.ConditionTrue {
				continue MainLoop
			}
		}

		// iterate the pod containers and find the named port
		for _, container := range pod.Spec.Containers {
			for _, port := range container.Ports {
				if !slices.Contains(validPortNames, port.Name) {
					continue
				}

				if port.Name == GossipPortName {
					addresses.Add(net.JoinHostPort(pod.Status.PodIP, strconv.Itoa(int(port.ContainerPort))))
				}
			}
		}
	}
	return addresses.ToSlice(), nil
}

// Close closes the provider
func (d *Discovery) Close() error {
	return nil
}

// setConfig sets the kubernetes discoConfig
func (d *Discovery) setConfig(config discovery.Config) (err error) {
	option := new(discoConfig)

	option.NameSpace, err = config.GetString(Namespace)
	if err != nil {
		return err
	}

	option.ActorSystemName, err = config.GetString(ActorSystemName)
	if err != nil {
		return err
	}

	option.ApplicationName, err = config.GetString(ApplicationName)

	if err != nil {
		return err
	}

	d.option = option
	return nil
}
