package discovery

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

const (
	// K8Namespace specifies the kubernetes namespace meta key
	K8Namespace string = "namespace"
	// K8PodLabels specifies the kubernetes pod_labels meta key
	K8PodLabels = "pod_labels"
	// K8LabelSelector specifies the kubernetes label_selector meta key
	K8LabelSelector = "label_selector"
	// K8PortName specifies the kubernetes port_name meta key
	K8PortName = "port_name"
)

// k8Option represents the kubernetes provider option
type k8Option struct {
	// KubeConfig represents the kubernetes configuration
	// TODO may not be needed
	KubeConfig string
	// NameSpace specifies the namespace
	NameSpace string
	// PodLabels defines the pod labels
	PodLabels map[string]string
	// Label Selector
	// TODO may not needed
	LabelSelector string
	// Specifies the port name
	PortName string
}

// Kubernetes represents the kubernetes provider
type Kubernetes struct {
	option    *k8Option
	k8sClient *kubernetes.Clientset
	mu        sync.Mutex

	stopChan   chan struct{}
	publicChan chan Event
	// states whether the actor system has started or not
	isInitialized *atomic.Bool
	logger        log.Logger
}

// enforce compilation
var _ Discovery = &Kubernetes{}

// NewKubernetes returns an instance of the kubernetes discovery provider
func NewKubernetes() *Kubernetes {
	// create an instance of
	k8 := &Kubernetes{
		mu:            sync.Mutex{},
		publicChan:    make(chan Event, 2),
		stopChan:      make(chan struct{}, 1),
		isInitialized: atomic.NewBool(false),
		logger:        log.DefaultLogger,
	}
	return k8
}

// EarliestNode returns the earliest node
func (k *Kubernetes) EarliestNode(ctx context.Context) (*Node, error) {
	// fetch the list of Nodes
	nodes, err := k.Nodes(ctx)
	// handle the error
	if err != nil {
		return nil, errors.Wrap(err, "failed to get the earliest node")
	}
	// let us sort the nodes by their timestamp
	sort.SliceStable(nodes, func(i, j int) bool {
		return nodes[i].Timestamp() < nodes[j].Timestamp()
	})
	// return the first element in the sorted list
	return nodes[0], nil
}

// Nodes returns the list of Nodes at a given time
func (k *Kubernetes) Nodes(ctx context.Context) ([]*Node, error) {
	// acquire the lock
	k.mu.Lock()
	// release the lock once done
	defer k.mu.Unlock()

	// first check whether the actor system has started
	if !k.isInitialized.Load() {
		return nil, errors.New("kubernetes discovery engine not initialized")
	}

	// List all the pods based on the filters we requested
	pods, err := k.k8sClient.CoreV1().Pods(k.option.NameSpace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(k.option.PodLabels).String(),
	})
	// panic when we cannot poll the pods
	if err != nil {
		// TODO maybe do not panic
		// TODO figure out the best approach
		panic(errors.Wrap(err, "failed to fetch kubernetes pods"))
	}

	nodes := make([]*Node, 0, pods.Size())

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
		var node *Node
		// iterate the pod containers and find the named port
		for i := 0; i < len(pod.Spec.Containers) && node == nil; i++ {
			// let us get the container
			container := pod.Spec.Containers[i]
			// iterate the container ports
			for _, port := range container.Ports {
				// find the mapping port
				if port.Name == k.option.PortName {
					// create the node object
					NewNode(
						pod.GetName(),
						pod.Status.PodIP,
						port.ContainerPort,
						pod.Status.StartTime.Time.UnixMilli(),
						map[string]string{})
					break
				}
			}
		}
		// continue the loop when we did not find any node
		if node == nil {
			continue MainLoop
		}
		// add the node to the list of nodes
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// Watch returns event based upon nodes lifecycle
func (k *Kubernetes) Watch(ctx context.Context) (<-chan Event, error) {
	// first check whether the actor system has started
	if !k.isInitialized.Load() {
		return nil, errors.New("kubernetes discovery engine not initialized")
	}
	// run the watcher
	go k.watchPods()
	return k.publicChan, nil
}

// Start the k8 discovery engine
func (k *Kubernetes) Start(ctx context.Context, meta Meta) error {
	// validate the meta
	// let us make sure we have the required options set
	// assert the present of the namespace
	if _, ok := meta[K8Namespace]; !ok {
		return errors.New("k8 namespace is not provided")
	}
	// assert the presence of the label selector
	if _, ok := meta[K8LabelSelector]; !ok {
		return errors.New("k8 label_selector is not provided")
	}
	// assert the port name
	if _, ok := meta[K8PortName]; !ok {
		return errors.New("k8 port_name is not provided")
	}
	// assert the pod labels
	if _, ok := meta[K8PodLabels]; !ok {
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
		return errors.Wrap(err, "failed to create the k8s client api")
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

// Stop shutdown the k8 discovery provider
func (k *Kubernetes) Stop() error {
	// first check whether the actor system has started
	if !k.isInitialized.Load() {
		return errors.New("kubernetes discovery engine not initialized")
	}
	// stop the watchers
	close(k.stopChan)
	// close the public channel
	close(k.publicChan)
	// set the initialized to false
	k.isInitialized = atomic.NewBool(false)
	return nil
}

// handlePodAdded is called when a new pod is added
func (k *Kubernetes) handlePodAdded(pod *v1.Pod) {
	// acquire the lock
	k.mu.Lock()
	// release the lock
	defer k.mu.Unlock()
	// ignore the pod when it is not running
	if pod.Status.Phase != v1.PodRunning {
		return
	}
	// get node
	node := k.toNode(pod)
	// continue the loop when we did not find any node
	if node == nil {
		return
	}
	// here we find a node let us raise the node registered event
	event := &NodeAdded{Node: node}
	// add to the channel
	k.publicChan <- event
}

// handlePodUpdated is called when a pod is updated
func (k *Kubernetes) handlePodUpdated(old *v1.Pod, pod *v1.Pod) {
	// ignore the pod when it is not running
	if pod.Status.Phase != v1.PodRunning {
		return
	}
	// acquire the lock
	k.mu.Lock()
	// release the lock
	defer k.mu.Unlock()
	// grab the old node
	currentNode := k.toNode(old)
	// get the new node
	node := k.toNode(pod)
	// continue the loop when we did not find any node
	if node == nil {
		return
	}
	// here we find a node let us raise the node modified event
	// add to the channel
	k.publicChan <- &NodeModified{
		Node:    node,
		Current: currentNode,
	}
}

// handlePodDeleted is called when pod is deleted
func (k *Kubernetes) handlePodDeleted(pod *v1.Pod) {
	// acquire the lock
	k.mu.Lock()
	// release the lock
	defer k.mu.Unlock()
	// get the new node
	node := k.toNode(pod)
	// here we find a node let us raise the node removed event
	event := &NodeRemoved{Node: node}
	// add to the channel
	k.publicChan <- event
}

// setOptions sets the kubernetes option
func (k *Kubernetes) setOptions(meta Meta) (err error) {
	// create an instance of Option
	option := new(k8Option)
	// extract the namespace
	option.NameSpace, err = meta.GetString(K8Namespace)
	// handle the error in case the namespace value is not properly set
	if err != nil {
		return err
	}
	// extract the label selector
	option.LabelSelector, err = meta.GetString(K8LabelSelector)
	// handle the error in case the label selector value is not properly set
	if err != nil {
		return err
	}
	// extract the port name
	option.PortName, err = meta.GetString(K8PortName)
	// handle the error in case the port name value is not properly set
	if err != nil {
		return err
	}
	// extract the pod labels
	option.PodLabels, err = meta.GetMapString(K8PodLabels)
	// handle the error in case the port labels value is not properly set
	if err != nil {
		return err
	}
	// in case none of the above extraction fails then set the option
	k.option = option
	return nil
}

// toNode takes a kubernetes pod and returns a Node
func (k *Kubernetes) toNode(pod *v1.Pod) *Node {
	// If there is a Ready condition available, we need that to be true.
	// If no ready condition is set, then we accept this pod regardless.
	for _, condition := range pod.Status.Conditions {
		// ignore pod that is not in ready state
		if condition.Type == v1.PodReady && condition.Status != v1.ConditionTrue {
			return nil
		}
	}

	// create a variable holding the node
	var node *Node
	// iterate the pod containers and find the named port
	for i := 0; i < len(pod.Spec.Containers) && node == nil; i++ {
		// let us get the container
		container := pod.Spec.Containers[i]
		// iterate the container ports
		for _, port := range container.Ports {
			// find the mapping port
			if port.Name == k.option.PortName {
				// create the node object
				node = NewNode(pod.GetName(), pod.Status.PodIP, port.ContainerPort, pod.Status.StartTime.Time.UnixMilli(), map[string]string{})
				break
			}
		}
	}

	return node
}

// watchPods keeps a watch on kubernetes pods activities and emit
// respective event when needed
func (k *Kubernetes) watchPods() {
	// create the k8 informer factory
	factory := informers.NewSharedInformerFactoryWithOptions(
		k.k8sClient,
		10*time.Minute, // TODO make it configurable
		informers.WithNamespace(k.option.NameSpace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = labels.SelectorFromSet(k.option.PodLabels).String()
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
			k.handlePodAdded(pod)
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
			k.handlePodUpdated(old, pod)
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
