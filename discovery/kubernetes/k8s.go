package kubernetes

import (
	"context"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
	v1 "k8s.io/api/core/v1"
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
func (d *Discovery) ID() string {
	return "kubernetes"
}

// Nodes returns the list of Nodes at a given time
func (d *Discovery) Nodes(ctx context.Context) ([]*discovery.Node, error) {
	// first check whether the actor system has started
	if !d.isInitialized.Load() {
		return nil, errors.New("kubernetes discovery engine not initialized")
	}

	// List all the pods based on the filters we requested
	pods, err := d.k8sClient.CoreV1().Pods(d.option.NameSpace).List(ctx, k8meta.ListOptions{
		LabelSelector: labels.SelectorFromSet(d.option.PodLabels).String(),
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
		// only consider running pods
		if pod.Status.Phase != v1.PodRunning {
			continue MainLoop
		}
		// If there is a Ready condition available, we need that to be true.
		// If no ready condition is set, then we accept this pod regardless.
		for _, condition := range pod.Status.Conditions {
			// ignore pod that is not in ready state
			if condition.Type == v1.PodReady && condition.Status != v1.ConditionTrue {
				continue MainLoop
			}
		}

		// create a variable holding the node
		node := d.podToNode(&pod)
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
func (d *Discovery) Watch(ctx context.Context) (<-chan discovery.Event, error) {
	// first check whether the actor system has started
	if !d.isInitialized.Load() {
		return nil, errors.New("kubernetes discovery engine not initialized")
	}
	// run the watcher
	go d.watchPods()
	return d.publicChan, nil
}

// Start the discovery engine
func (d *Discovery) Start(ctx context.Context, meta discovery.Meta) error {
	// validate the meta
	// let us make sure we have the required options set
	// assert the present of the namespace
	if _, ok := meta[Namespace]; !ok {
		return errors.New("k8 namespace is not provided")
	}
	// assert the presence of the label selector
	if _, ok := meta[LabelSelector]; !ok {
		return errors.New("k8 label_selector is not provided")
	}
	// assert the port name
	if _, ok := meta[PortName]; !ok {
		return errors.New("k8 port_name is not provided")
	}
	// assert the pod labels
	if _, ok := meta[PodLabels]; !ok {
		return errors.New("k8 pod_labels is not provided")
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
	d.k8sClient = k8sClient
	// set the options
	if err := d.setOptions(meta); err != nil {
		return errors.Wrap(err, "failed to instantiate the kubernetes discovery provider")
	}
	// set initialized
	d.isInitialized = atomic.NewBool(true)
	return nil
}

// Stop shutdown the discovery engine
func (d *Discovery) Stop() error {
	// first check whether the actor system has started
	if !d.isInitialized.Load() {
		return errors.New("kubernetes discovery engine not initialized")
	}
	// stop the watchers
	close(d.stopChan)
	// close the public channel
	close(d.publicChan)
	return nil
}

// handlePodAdded is called when a new pod is added
func (d *Discovery) handlePodAdded(pod *v1.Pod) {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()
	// ignore the pod when it is not running
	if pod.Status.Phase != v1.PodRunning {
		return
	}
	// get node
	node := d.podToNode(pod)
	// continue the loop when we did not find any node
	if node == nil {
		return
	}
	// here we find a node let us raise the node registered event
	event := &discovery.NodeAdded{Node: node}
	// add to the channel
	d.publicChan <- event
}

// handlePodUpdated is called when a pod is updated
func (d *Discovery) handlePodUpdated(old *v1.Pod, pod *v1.Pod) {
	// ignore the pod when it is not running
	if pod.Status.Phase != v1.PodRunning {
		return
	}
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()
	// grab the old node
	oldNode := d.podToNode(old)
	// get the new node
	node := d.podToNode(pod)
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
	d.publicChan <- event
}

// handlePodDeleted is called when pod is deleted
func (d *Discovery) handlePodDeleted(pod *v1.Pod) {
	// acquire the lock
	d.mu.Lock()
	// release the lock
	defer d.mu.Unlock()
	// get the new node
	node := d.podToNode(pod)
	// here we find a node let us raise the node removed event
	event := &discovery.NodeRemoved{Node: node}
	// add to the channel
	d.publicChan <- event
}

// setOptions sets the kubernetes option
func (d *Discovery) setOptions(meta discovery.Meta) (err error) {
	// create an instance of Option
	option := new(Option)
	// extract the namespace
	option.NameSpace, err = meta.GetString(Namespace)
	// handle the error in case the namespace value is not properly set
	if err != nil {
		return err
	}
	// extract the label selector
	option.LabelSelector, err = meta.GetString(LabelSelector)
	// handle the error in case the label selector value is not properly set
	if err != nil {
		return err
	}
	// extract the port name
	option.PortName, err = meta.GetString(PortName)
	// handle the error in case the port name value is not properly set
	if err != nil {
		return err
	}
	// extract the pod labels
	option.PodLabels, err = meta.GetMapString(PodLabels)
	// handle the error in case the port labels value is not properly set
	if err != nil {
		return err
	}
	// extract the remoting port name
	option.RemotingPortName, err = meta.GetString(RemotingPortName)
	// handle the error in case the remoting port name value is not properly set
	if err != nil {
		return err
	}

	// in case none of the above extraction fails then set the option
	d.option = option
	return nil
}

// podToNode takes a kubernetes pod and returns a Node
func (d *Discovery) podToNode(pod *v1.Pod) *discovery.Node {
	// If there is a Ready condition available, we need that to be true.
	// If no ready condition is set, then we accept this pod regardless.
	for _, condition := range pod.Status.Conditions {
		// ignore pod that is not in ready state
		if condition.Type == v1.PodReady && condition.Status != v1.ConditionTrue {
			return nil
		}
	}

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
			Name:         pod.GetName(),
			Host:         pod.Status.PodIP,
			StartTime:    pod.Status.StartTime.Time.UnixMilli(),
			JoinPort:     portMap[d.option.PortName],
			RemotingPort: portMap[d.option.RemotingPortName],
		}
	}

	return node
}

// watchPods keeps a watch on kubernetes pods activities and emit
// respective event when needed
func (d *Discovery) watchPods() {
	// create the k8 informer factory
	factory := informers.NewSharedInformerFactoryWithOptions(
		d.k8sClient,
		10*time.Minute, // TODO make it configurable
		informers.WithNamespace(d.option.NameSpace),
		informers.WithTweakListOptions(func(options *k8meta.ListOptions) {
			options.LabelSelector = labels.SelectorFromSet(d.option.PodLabels).String()
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
			pod := obj.(*v1.Pod)
			// handle the newly added pod
			d.handlePodAdded(pod)
		},
		UpdateFunc: func(current, node any) {
			mux.RLock()
			defer mux.RUnlock()
			if !synced {
				return
			}

			// Handler logic
			old := current.(*v1.Pod)
			pod := node.(*v1.Pod)
			d.handlePodUpdated(old, pod)
		},
		DeleteFunc: func(obj any) {
			mux.RLock()
			defer mux.RUnlock()
			if !synced {
				return
			}

			// Handler logic
			pod := obj.(*v1.Pod)
			// handle the newly added pod
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
