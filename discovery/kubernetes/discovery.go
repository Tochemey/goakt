package kubernetes

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/telemetry"
	"go.uber.org/atomic"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
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

	stopChan   chan struct{}
	publicChan chan discovery.Event
	// states whether the actor system has started or not
	isInitialized *atomic.Bool
	logger        log.Logger
}

// enforce compilation error
var _ discovery.Discovery = &Discovery{}

// NewDiscovery returns an instance of the kubernetes discovery provider
func NewDiscovery(logger log.Logger) *Discovery {
	// create an instance of
	k8 := &Discovery{
		mu:            sync.Mutex{},
		publicChan:    make(chan discovery.Event, 2),
		stopChan:      make(chan struct{}, 1),
		isInitialized: atomic.NewBool(false),
		logger:        logger,
	}
	return k8
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

// Watch returns event based upon node lifecycle
func (d *Discovery) Watch(ctx context.Context) (<-chan discovery.Event, error) {
	// add a span context
	_, span := telemetry.SpanContext(ctx, "Watch")
	defer span.End()
	// first check whether the discovery provider is running
	if !d.isInitialized.Load() {
		return nil, errors.New("kubernetes discovery engine not initialized")
	}
	// run the watcher
	go d.watchPods()
	return d.publicChan, nil
}

// Start the discovery engine
func (d *Discovery) Start(ctx context.Context, meta discovery.Meta) error {
	// add a span context
	_, span := telemetry.SpanContext(ctx, "Start")
	defer span.End()
	// validate the meta
	// let us make sure we have the required options set
	// assert the present of the namespace
	if _, ok := meta[Namespace]; !ok {
		return errors.New("k8 namespace is not provided")
	}
	// assert the presence of the label selector
	if _, ok := meta[ActorSystemName]; !ok {
		return errors.New("actor system name is not provided")
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
	// set the options
	if err := d.setOptions(meta); err != nil {
		return errors.Wrap(err, "failed to instantiate the kubernetes discovery provider")
	}
	// set initialized
	d.isInitialized = atomic.NewBool(true)
	return nil
}

// EarliestNode returns the earliest node. This is based upon the node timestamp
func (d *Discovery) EarliestNode(ctx context.Context) (*discovery.Node, error) {
	// fetch the list of Nodes
	nodes, err := d.Nodes(ctx)
	// handle the error
	if err != nil {
		return nil, errors.Wrap(err, "failed to get the earliest node")
	}

	// check whether the list of nodes is not empty
	if len(nodes) == 0 {
		return nil, errors.New("no nodes are found")
	}

	// let us sort the nodes by their timestamp
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].StartTime < nodes[j].StartTime
	})
	// return the first element in the sorted list
	return nodes[0], nil
}

// Stop shutdown the discovery engine
func (d *Discovery) Stop() error {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the actor system has started
	if !d.isInitialized.Load() {
		return errors.New("kubernetes discovery engine not initialized")
	}
	// set the initialized to false
	d.isInitialized = atomic.NewBool(false)
	// close the public channel
	close(d.publicChan)
	// stop the watchers
	close(d.stopChan)
	// return
	return nil
}

// handlePodAdded is called when a new pod is added
func (d *Discovery) handlePodAdded(pod *corev1.Pod) {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider is running
	if !d.isInitialized.Load() {
		return
	}

	// ignore the pod when it is not running
	if pod.Status.Phase != corev1.PodRunning {
		// add some debug logging
		d.logger.Debugf("pod=%s added is not running. Status=%s", pod.GetName(), pod.Status.Phase)
		return
	}
	// get node
	node := d.podToNode(pod)
	// continue the loop when we did not find any node
	if node == nil || !node.IsValid() {
		return
	}
	// here we find a node let us raise the node registered event
	event := &discovery.NodeAdded{Node: node}
	// add to the channel
	d.publicChan <- event
}

// handlePodUpdated is called when a pod is updated
func (d *Discovery) handlePodUpdated(old *corev1.Pod, pod *corev1.Pod) {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider is running
	if !d.isInitialized.Load() {
		return
	}

	// ignore the pod when it is not running
	if old.Status.Phase != corev1.PodRunning {
		// add some debug logging
		d.logger.Debugf("pod=%s to be modified is not running. Status=%s", old.GetName(), old.Status.Phase)
		return
	}

	// ignore the pod when it is not running
	if pod.Status.Phase != corev1.PodRunning {
		// add some debug logging
		d.logger.Debugf("modified pod=%s is not running. Status=%s", pod.GetName(), pod.Status.Phase)
		return
	}

	// grab the old node
	oldNode := d.podToNode(old)
	// get the new node
	node := d.podToNode(pod)
	// continue the loop when we did not find any node
	if oldNode == nil ||
		node == nil || !node.IsValid() ||
		!oldNode.IsValid() {
		return
	}
	// here we find a node let us raise the node modified event
	event := &discovery.NodeModified{
		Node:    node,
		Current: oldNode,
	}
	// add to the channel
	d.publicChan <- event
}

// handlePodDeleted is called when pod is deleted
func (d *Discovery) handlePodDeleted(pod *corev1.Pod) {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()

	// first check whether the discovery provider is running
	if !d.isInitialized.Load() {
		return
	}

	// get the new node
	node := d.podToNode(pod)
	// here we find a node let us raise the node removed event
	event := &discovery.NodeRemoved{Node: node}
	// add to the channel
	d.publicChan <- event
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

// watchPods keeps a watch on kubernetes pods activities and emit
// respective event when needed
func (d *Discovery) watchPods() {
	// add some debug logging
	d.logger.Debugf("%s start watching pods activities...", d.ID())
	// TODO: make sure to document it on k8 discovery
	podLabels := map[string]string{
		"app.kubernetes.io/part-of":   d.option.ActorSystemName,
		"app.kubernetes.io/component": d.option.ApplicationName, // TODO: redefine it
		"app.kubernetes.io/name":      d.option.ApplicationName,
	}
	// create the k8 informer factory
	factory := informers.NewSharedInformerFactoryWithOptions(
		d.client,
		time.Second, // TODO make it configurable
		informers.WithNamespace(d.option.NameSpace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = labels.SelectorFromSet(podLabels).String()
		}))
	// create the pods informer instance
	informer := factory.Core().V1().Pods().Informer()
	synced := false
	mux := &sync.RWMutex{}
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			mux.RLock()
			defer mux.RUnlock()
			if !synced {
				// add some debug logging
				d.logger.Debugf("%s pods watching synchronization not yet done", d.ID())
				return
			}

			// Handler logic
			pod := obj.(*corev1.Pod)
			// add some debug logging
			d.logger.Debugf("%s has been added", pod.Name)
			// handle the newly added pod
			d.handlePodAdded(pod)
		},
		UpdateFunc: func(current, node any) {
			mux.RLock()
			defer mux.RUnlock()
			if !synced {
				// add some debug logging
				d.logger.Debugf("%s pods watching synchronization not yet done", d.ID())
				return
			}

			// Handler logic
			old := current.(*corev1.Pod)
			pod := node.(*corev1.Pod)

			// add some debug logging
			d.logger.Debugf("%s has been modified", old.Name)

			d.handlePodUpdated(old, pod)
		},
		DeleteFunc: func(obj any) {
			mux.RLock()
			defer mux.RUnlock()
			if !synced {
				// add some debug logging
				d.logger.Debugf("%s pods watching synchronization not yet done", d.ID())
				return
			}

			// Handler logic
			pod := obj.(*corev1.Pod)

			// add some debug logging
			d.logger.Debugf("%s has been deleted", pod.Name)

			// handle the deleted pod
			d.handlePodDeleted(pod)
		},
	})
	if err != nil {
		return
	}

	// run the informer
	go informer.Run(d.stopChan)

	// wait for caches to sync
	isSynced := cache.WaitForCacheSync(d.stopChan, informer.HasSynced)
	mux.Lock()
	synced = isSynced
	mux.Unlock()

	// caches failed to sync
	if !synced {
		d.logger.Fatal("caches failed to sync")
	}
}
