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
	k8meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

// Discovery represents the kubernetes discovery
type Discovery struct {
	option    *Option
	k8sClient *kubernetes.Clientset
	mu        sync.Mutex

	stopChan   chan struct{}
	publicChan chan discovery.Event
	// states whether the actor system has started or not
	isInitialized *atomic.Bool
	logger        log.Logger
}

// enforce compilation error
var _ discovery.Discovery = &Discovery{}

// New returns an instance of the kubernetes discovery provider
func New(logger log.Logger) *Discovery {
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

// ID returns the discovery id
func (k *Discovery) ID() string {
	return "kubernetes"
}

// Nodes returns the list of up and running Nodes at a given time
func (k *Discovery) Nodes(ctx context.Context) ([]*discovery.Node, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Nodes")
	defer span.End()

	// first check whether the actor system has started
	if !k.isInitialized.Load() {
		return nil, errors.New("kubernetes discovery engine not initialized")
	}

	// let us create the pod labels map
	// TODO: make sure to document it on k8 discovery
	podLabels := map[string]string{
		"app.kubernetes.io/part-of":   k.option.ActorSystemName,
		"app.kubernetes.io/component": k.option.ApplicationName, // TODO: redefine it
		"app.kubernetes.io/name":      k.option.ApplicationName,
	}

	// List all the pods based on the filters we requested
	pods, err := k.k8sClient.CoreV1().Pods(k.option.NameSpace).List(ctx, k8meta.ListOptions{
		LabelSelector: labels.SelectorFromSet(podLabels).String(),
	})
	// panic when we cannot poll the pods
	if err != nil {
		// TODO maybe do not panic
		// TODO figure out the best approach
		k.logger.Panic(errors.Wrap(err, "failed to fetch kubernetes pods"))
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
		node := k.podToNode(&pod)
		// continue the loop when we did not find any node
		if node == nil {
			continue MainLoop
		}
		// add the node to the list of nodes
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// Watch returns event based upon node lifecycle
func (k *Discovery) Watch(ctx context.Context) (<-chan discovery.Event, error) {
	// add a span context
	_, span := telemetry.SpanContext(ctx, "Watch")
	defer span.End()
	// first check whether the actor system has started
	if !k.isInitialized.Load() {
		return nil, errors.New("kubernetes discovery engine not initialized")
	}
	// run the watcher
	go k.watchPods()
	return k.publicChan, nil
}

// Start the discovery engine
func (k *Discovery) Start(ctx context.Context, meta discovery.Meta) error {
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
	k8sClient, err := kubernetes.NewForConfig(config)
	// handle the error
	if err != nil {
		return errors.Wrap(err, "failed to create the kubernetes client api")
	}

	// set the k8 client
	k.k8sClient = k8sClient
	// set the options
	if err := k.setOptions(meta); err != nil {
		return errors.Wrap(err, "failed to instantiate the kubernetes discovery provider")
	}
	// set initialized
	k.isInitialized = atomic.NewBool(true)
	return nil
}

// EarliestNode returns the earliest node. This is based upon the node timestamp
func (k *Discovery) EarliestNode(ctx context.Context) (*discovery.Node, error) {
	// fetch the list of Nodes
	nodes, err := k.Nodes(ctx)
	// handle the error
	if err != nil {
		return nil, errors.Wrap(err, "failed to get the earliest node")
	}
	// let us sort the nodes by their timestamp
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].StartTime < nodes[j].StartTime
	})
	// return the first element in the sorted list
	return nodes[0], nil
}

// Stop shutdown the discovery engine
func (k *Discovery) Stop() error {
	// first check whether the actor system has started
	if !k.isInitialized.Load() {
		return errors.New("kubernetes discovery engine not initialized")
	}
	// stop the watchers
	close(k.stopChan)
	// close the public channel
	close(k.publicChan)
	return nil
}

// handlePodAdded is called when a new pod is added
func (k *Discovery) handlePodAdded(pod *corev1.Pod) {
	// acquire the lock
	k.mu.Lock()
	// release the lock
	defer k.mu.Unlock()
	// ignore the pod when it is not running
	if pod.Status.Phase != corev1.PodRunning {
		return
	}
	// get node
	node := k.podToNode(pod)
	// continue the loop when we did not find any node
	if node == nil {
		return
	}
	// here we find a node let us raise the node registered event
	event := &discovery.NodeAdded{Node: node}
	// add to the channel
	k.publicChan <- event
}

// handlePodUpdated is called when a pod is updated
func (k *Discovery) handlePodUpdated(old *corev1.Pod, pod *corev1.Pod) {
	// ignore the pod when it is not running
	if pod.Status.Phase != corev1.PodRunning {
		return
	}
	// acquire the lock
	k.mu.Lock()
	// release the lock
	defer k.mu.Unlock()
	// grab the old node
	oldNode := k.podToNode(old)
	// get the new node
	node := k.podToNode(pod)
	// continue the loop when we did not find any node
	if node == nil {
		return
	}
	// here we find a node let us raise the node modified event
	event := &discovery.NodeModified{
		Node:    node,
		Current: oldNode,
	}
	// add to the channel
	k.publicChan <- event
}

// handlePodDeleted is called when pod is deleted
func (k *Discovery) handlePodDeleted(pod *corev1.Pod) {
	// acquire the lock
	k.mu.Lock()
	// release the lock
	defer k.mu.Unlock()
	// get the new node
	node := k.podToNode(pod)
	// here we find a node let us raise the node removed event
	event := &discovery.NodeRemoved{Node: node}
	// add to the channel
	k.publicChan <- event
}

// setOptions sets the kubernetes option
func (k *Discovery) setOptions(meta discovery.Meta) (err error) {
	// create an instance of Option
	option := new(Option)
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
	k.option = option
	return nil
}

// podToNode takes a kubernetes pod and returns a Node
func (k *Discovery) podToNode(pod *corev1.Pod) *discovery.Node {
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
func (k *Discovery) watchPods() {
	// TODO: make sure to document it on k8 discovery
	podLabels := map[string]string{
		"app.kubernetes.io/part-of":   k.option.ActorSystemName,
		"app.kubernetes.io/component": k.option.ApplicationName, // TODO: redefine it
		"app.kubernetes.io/name":      k.option.ApplicationName,
	}
	// create the k8 informer factory
	factory := informers.NewSharedInformerFactoryWithOptions(
		k.k8sClient,
		10*time.Minute, // TODO make it configurable
		informers.WithNamespace(k.option.NameSpace),
		informers.WithTweakListOptions(func(options *k8meta.ListOptions) {
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
				return
			}

			// Handler logic
			pod := obj.(*corev1.Pod)
			// handle the newly added pod
			k.handlePodAdded(pod)
		},
		UpdateFunc: func(current, node any) {
			mux.RLock()
			defer mux.RUnlock()
			if !synced {
				return
			}

			// Handler logic
			old := current.(*corev1.Pod)
			pod := node.(*corev1.Pod)
			k.handlePodUpdated(old, pod)
		},
		DeleteFunc: func(obj any) {
			mux.RLock()
			defer mux.RUnlock()
			if !synced {
				return
			}

			// Handler logic
			pod := obj.(*corev1.Pod)
			// handle the newly added pod
			k.handlePodDeleted(pod)
		},
	})
	if err != nil {
		return
	}

	// run the informer
	go informer.Run(k.stopChan)

	// wait for caches to sync
	isSynced := cache.WaitForCacheSync(k.stopChan, informer.HasSynced)
	mux.Lock()
	synced = isSynced
	mux.Unlock()

	// caches failed to sync
	if !synced {
		k.logger.Fatal("caches failed to sync")
	}
}
