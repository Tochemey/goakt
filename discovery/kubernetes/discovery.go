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

package kubernetes

import (
	"context"
	"net"
	"strconv"
	"sync"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/strings/slices"
)

const (
	Namespace        string = "namespace"         // Namespace specifies the kubernetes namespace
	ActorSystemName         = "actor_system_name" // ActorSystemName specifies the actor system name
	ApplicationName         = "app_name"          // ApplicationName specifies the application name. This often matches the actor system name
	GossipPortName          = "gossip-port"
	ClusterPortName         = "cluster-port"
	RemotingPortName        = "remoting-port"
)

// option represents the kubernetes provider option
type option struct {
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
	option *option
	client kubernetes.Interface
	mu     sync.Mutex

	stopChan chan struct{}
	// states whether the actor system has started or not
	isInitialized *atomic.Bool
}

// enforce compilation error
var _ discovery.Provider = &Discovery{}

// NewDiscovery returns an instance of the kubernetes discovery provider
func NewDiscovery() *Discovery {
	// create an instance of
	k8 := &Discovery{
		mu:            sync.Mutex{},
		stopChan:      make(chan struct{}, 1),
		isInitialized: atomic.NewBool(false),
		option:        &option{},
	}

	return k8
}

// ID returns the discovery provider id
func (d *Discovery) ID() string {
	return "kubernetes"
}

// Initialize initializes the plugin: registers some internal data structures, clients etc.
func (d *Discovery) Initialize() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()
	// first check whether the discovery provider is running
	if d.isInitialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	// check the options
	if d.option.Provider == "" {
		d.option.Provider = d.ID()
	}

	return nil
}

// SetConfig registers the underlying discovery configuration
func (d *Discovery) SetConfig(meta discovery.Config) error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider is running
	if d.isInitialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	return d.setConfig(meta)
}

// Register registers this node to a service discovery directory.
func (d *Discovery) Register() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider has started
	// avoid to re-register the discovery
	if d.isInitialized.Load() {
		return discovery.ErrAlreadyRegistered
	}

	// create the k8 config
	config, err := rest.InClusterConfig()
	// handle the error
	if err != nil {
		return errors.Wrap(err, "failed to get the in-cluster config of the kubernetes provider")
	}
	// create an instance of the k8 client set
	client, err := kubernetes.NewForConfig(config)
	// handle the error
	if err != nil {
		return errors.Wrap(err, "failed to create the kubernetes client api")
	}
	// set the k8 client
	d.client = client
	// set initialized
	d.isInitialized = atomic.NewBool(true)
	return nil
}

// Deregister removes this node from a service discovery directory.
func (d *Discovery) Deregister() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider has started
	if !d.isInitialized.Load() {
		return discovery.ErrNotInitialized
	}
	// set the initialized to false
	d.isInitialized = atomic.NewBool(false)
	// stop the watchers
	close(d.stopChan)
	// return
	return nil
}

// DiscoverPeers returns a list of known nodes.
func (d *Discovery) DiscoverPeers() ([]string, error) {
	// first check whether the discovery provider is running
	if !d.isInitialized.Load() {
		return nil, discovery.ErrNotInitialized
	}

	// let us create the pod labels map
	// TODO: make sure to document it on k8 discovery
	podLabels := map[string]string{
		"app.kubernetes.io/part-of":   d.option.ActorSystemName,
		"app.kubernetes.io/component": d.option.ApplicationName, // TODO: redefine it
		"app.kubernetes.io/name":      d.option.ApplicationName,
	}

	// create a context
	ctx := context.Background()

	// List all the pods based on the filters we requested
	pods, err := d.client.CoreV1().Pods(d.option.NameSpace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(podLabels).String(),
	})
	// panic when we cannot poll the pods
	if err != nil {
		return nil, err
	}

	// define valid port names
	validPortNames := []string{ClusterPortName, GossipPortName, RemotingPortName}

	// define the addresses list
	addresses := goset.NewSet[string]()
	// iterate the pods list and only the one that are running
MainLoop:
	for _, pod := range pods.Items {
		// create a variable copy of pod
		pod := pod
		// only consider running pods
		if pod.Status.Phase != corev1.PodRunning {
			continue MainLoop
		}
		// If there is a Ready condition available, we need that to be true.
		// If no ready condition is set, then we accept this pod regardless.
		for _, condition := range pod.Status.Conditions {
			// ignore pod that is not in ready state
			if condition.Type == corev1.PodReady && condition.Status != corev1.ConditionTrue {
				continue MainLoop
			}
		}

		// iterate the pod containers and find the named port
		for _, container := range pod.Spec.Containers {
			// iterate the container ports to set the join port
			for _, port := range container.Ports {
				// make sure we have the gossip and cluster port defined
				if !slices.Contains(validPortNames, port.Name) {
					// skip that port
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

// setConfig sets the kubernetes option
func (d *Discovery) setConfig(config discovery.Config) (err error) {
	// create an instance of option
	option := new(option)
	// extract the namespace
	option.NameSpace, err = config.GetString(Namespace)
	// handle the error in case the namespace value is not properly set
	if err != nil {
		return err
	}
	// extract the actor system name
	option.ActorSystemName, err = config.GetString(ActorSystemName)
	// handle the error in case the actor system name value is not properly set
	if err != nil {
		return err
	}
	// extract the application name
	option.ApplicationName, err = config.GetString(ApplicationName)
	// handle the error in case the application name value is not properly set
	if err != nil {
		return err
	}
	// in case none of the above extraction fails then set the option
	d.option = option
	return nil
}
