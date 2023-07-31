package kubernetes

import (
	"context"
	"sync"

	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/telemetry"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	Namespace       string = "namespace"         // Namespace specifies the kubernetes namespace
	ActorSystemName        = "actor_system_name" // ActorSystemName specifies the actor system name
	ApplicationName        = "app_name"          // ApplicationName specifies the application name. This often matches the actor system name
)

// option represents the kubernetes provider option
type option struct {
	// KubeConfig represents the kubernetes configuration
	KubeConfig string
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
	logger        log.Logger
}

// enforce compilation error
var _ discovery.Discovery = &Discovery{}

// NewDiscovery returns an instance of the kubernetes discovery provider
func NewDiscovery(ctx context.Context, logger log.Logger, meta discovery.Meta) (*Discovery, error) {
	// create an instance of
	k8 := &Discovery{
		mu:            sync.Mutex{},
		stopChan:      make(chan struct{}, 1),
		isInitialized: atomic.NewBool(false),
		logger:        logger,
	}
	// set the options
	if err := k8.setOptions(meta); err != nil {
		return nil, errors.Wrap(err, "failed to instantiate the kubernetes discovery provider")
	}

	return k8, nil
}

// ID returns the discovery provider id
func (d *Discovery) ID() string {
	return "kubernetes"
}

// Nodes returns the list of up and running Nodes at a given time
func (d *Discovery) Nodes(ctx context.Context) ([]*discovery.Node, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Nodes")
	defer span.End()

	// first check whether the discovery provider is running
	if !d.isInitialized.Load() {
		return nil, errors.New("kubernetes discovery engine not initialized")
	}

	// let us create the pod labels map
	// TODO: make sure to document it on k8 discovery
	podLabels := map[string]string{
		"app.kubernetes.io/part-of":   d.option.ActorSystemName,
		"app.kubernetes.io/component": d.option.ApplicationName, // TODO: redefine it
		"app.kubernetes.io/name":      d.option.ApplicationName,
	}

	// List all the pods based on the filters we requested
	pods, err := d.client.CoreV1().Pods(d.option.NameSpace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(podLabels).String(),
	})
	// panic when we cannot poll the pods
	if err != nil {
		// TODO maybe do not panic
		// TODO figure out the best approach
		d.logger.Panic(errors.Wrap(err, "failed to fetch kubernetes pods"))
	}

	nodes := make([]*discovery.Node, 0, pods.Size())

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

		// create a variable holding the node
		node := d.podToNode(&pod)
		// continue the loop when we did not find any node
		if node == nil || !node.IsValid() {
			continue MainLoop
		}
		// add the node to the list of nodes
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// Start the discovery engine
func (d *Discovery) Start(ctx context.Context) error {
	// add a span context
	_, span := telemetry.SpanContext(ctx, "Start")
	defer span.End()

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

// Stop shutdown the discovery engine
func (d *Discovery) Stop() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider has started
	if !d.isInitialized.Load() {
		return errors.New("kubernetes discovery engine not initialized")
	}
	// set the initialized to false
	d.isInitialized = atomic.NewBool(false)
	// stop the watchers
	close(d.stopChan)
	// return
	return nil
}

// setOptions sets the kubernetes option
func (d *Discovery) setOptions(meta discovery.Meta) (err error) {
	// create an instance of option
	option := new(option)
	// extract the namespace
	option.NameSpace, err = meta.GetString(Namespace)
	// handle the error in case the namespace value is not properly set
	if err != nil {
		return err
	}
	// extract the actor system name
	option.ActorSystemName, err = meta.GetString(ActorSystemName)
	// handle the error in case the actor system name value is not properly set
	if err != nil {
		return err
	}
	// extract the application name
	option.ApplicationName, err = meta.GetString(ApplicationName)
	// handle the error in case the application name value is not properly set
	if err != nil {
		return err
	}
	// in case none of the above extraction fails then set the option
	d.option = option
	return nil
}

// podToNode takes a kubernetes pod and returns a Node
func (d *Discovery) podToNode(pod *corev1.Pod) *discovery.Node {
	// create a variable holding the node
	var node *discovery.Node
	// iterate the pod containers and find the named port
	for i := 0; i < len(pod.Spec.Containers) && node == nil; i++ {
		// let us get the container
		container := pod.Spec.Containers[i]

		// create a map of port name and port number
		portMap := make(map[string]int32)

		// iterate the container ports to set the join port
		for _, port := range container.Ports {
			// build the map
			portMap[port.Name] = port.ContainerPort
		}

		// set the node
		node = &discovery.Node{
			Name:      pod.GetName(),
			Host:      pod.Status.PodIP,
			StartTime: pod.Status.StartTime.Time.UnixMilli(),
			Ports:     portMap,
			IsRunning: pod.Status.Phase == corev1.PodRunning,
		}
	}

	return node
}
