/*
 * MIT License
 *
 * Copyright (c2) 2022-2025  Arsene Tochemey Gandote
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

package actor

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	nethttp "net/http"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	goset "github.com/deckarep/golang-set/v2"
	"github.com/google/uuid"
	"go.uber.org/atomic"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/hash"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/errorschain"
	"github.com/tochemey/goakt/v3/internal/eventstream"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v3/internal/tcp"
	"github.com/tochemey/goakt/v3/internal/ticker"
	"github.com/tochemey/goakt/v3/internal/types"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/remote"
)

// ActorSystem defines the contract of an actor system
type ActorSystem interface {
	// Metric returns the actor system metric.
	// The metric does not include any cluster data
	Metric(ctx context.Context) *Metric
	// Name returns the actor system name
	Name() string
	// Actors returns the list of Actors that are alive on a given running node.
	// This does not account for the total number of actors in the cluster
	Actors() []*PID
	// ActorRefs retrieves a list of active actors, including both local actors
	// and, when cluster mode is enabled, actors across the cluster. Use this
	// method cautiously, as the scanning process may impact system performance.
	// If the cluster request fails, only locally active actors will be returned.
	// The timeout parameter defines the maximum duration for cluster-based requests
	// before they are terminated.
	ActorRefs(ctx context.Context, timeout time.Duration) []ActorRef
	// Start initializes the actor system.
	// To guarantee a clean shutdown during unexpected system terminations,
	// developers must handle SIGTERM and SIGINT signals appropriately and invoke Stop.
	Start(ctx context.Context) error
	// Stop stops the actor system and does not terminate the program.
	// One needs to explicitly call os.Exit to terminate the program.
	Stop(ctx context.Context) error
	// Spawn creates an actor in the system and starts it
	Spawn(ctx context.Context, name string, actor Actor, opts ...SpawnOption) (*PID, error)
	// SpawnFromFunc creates an actor with the given receive function. One can set the PreStart and PostStop lifecycle hooks
	// in the given optional options
	SpawnFromFunc(ctx context.Context, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error)
	// SpawnNamedFromFunc creates an actor with the given receive function and provided name. One can set the PreStart and PostStop lifecycle hooks
	// in the given optional options
	SpawnNamedFromFunc(ctx context.Context, name string, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error)
	// SpawnRouter creates a new router. One can additionally set the router options.
	// A router is a special type of actor that helps distribute messages of the same type over a set of actors, so that messages can be processed in parallel.
	// A single actor will only process one message at a time.
	SpawnRouter(ctx context.Context, poolSize int, routeesKind Actor, opts ...RouterOption) (*PID, error)
	// SpawnSingleton creates a singleton actor in the system.
	//
	// A singleton actor is instantiated when cluster mode is enabled.
	// A singleton actor like any other actor is created only once within the system and in the cluster.
	// A singleton actor is created with the default supervisor strategy and directive.
	// A singleton actor once created lives throughout the lifetime of the given actor system.
	//
	// The cluster singleton is automatically started on the oldest node in the cluster.
	// If the oldest node leaves the cluster, the singleton is restarted on the new oldest node.
	// This is useful for managing shared resources or coordinating tasks that should be handled by a single actor.
	SpawnSingleton(ctx context.Context, name string, actor Actor) error
	// Kill stops a given actor in the system
	Kill(ctx context.Context, name string) error
	// ReSpawn recreates a given actor in the system
	// During restart all messages that are in the mailbox and not yet processed will be ignored.
	// Only the direct alive children of the given actor will be shudown and respawned with their initial state.
	// Bear in mind that restarting an actor will reinitialize the actor to initial state.
	// In case any of the direct child restart fails the given actor will not be started at all.
	ReSpawn(ctx context.Context, name string) (*PID, error)
	// NumActors returns the total number of active actors on a given running node.
	// This does not account for the total number of actors in the cluster
	NumActors() uint64
	// LocalActor returns the reference of a local actor.
	// A local actor is an actor that reside on the same node where the given actor system has started
	LocalActor(actorName string) (*PID, error)
	// RemoteActor returns the address of a remote actor when cluster is enabled
	// When the cluster mode is not enabled an actor not found error will be returned
	// One can always check whether cluster is enabled before calling this method or just use the ActorOf method.
	RemoteActor(ctx context.Context, actorName string) (addr *address.Address, err error)
	// ActorOf retrieves an existing actor within the local system or across the cluster if clustering is enabled.
	//
	// If the actor is found locally, its PID is returned. If the actor resides on a remote host, its address is returned.
	// If the actor is not found, an error of type "actor not found" is returned.
	ActorOf(ctx context.Context, actorName string) (addr *address.Address, pid *PID, err error)
	// InCluster states whether the actor system has started within a cluster of nodes
	InCluster() bool
	// GetPartition returns the partition where a given actor is located
	GetPartition(actorName string) int
	// Subscribe creates an event subscriber to consume events from the actor system.
	// Remember to use the Unsubscribe method to avoid resource leakage.
	Subscribe() (eventstream.Subscriber, error)
	// Unsubscribe unsubscribes a subscriber.
	Unsubscribe(subscriber eventstream.Subscriber) error
	// ScheduleOnce schedules a message that will be delivered to the receiver actor
	// This will send the given message to the actor after the given interval specified.
	// The message will be sent once
	ScheduleOnce(ctx context.Context, message proto.Message, pid *PID, interval time.Duration) error
	// Schedule schedules a message that will be delivered to the receiver actor
	// This will send the given message to the actor after the given interval specified.
	Schedule(ctx context.Context, message proto.Message, pid *PID, interval time.Duration) error
	// RemoteScheduleOnce schedules a message to be sent to a remote actor in the future.
	// This requires remoting to be enabled on the actor system.
	// This will send the given message to the actor after the given interval specified
	// The message will be sent once
	RemoteScheduleOnce(ctx context.Context, message proto.Message, address *address.Address, interval time.Duration) error
	// RemoteSchedule schedules a message to be sent to a remote actor in the future.
	// This requires remoting to be enabled on the actor system.
	// This will send the given message to the actor after the given interval specified
	RemoteSchedule(ctx context.Context, message proto.Message, address *address.Address, interval time.Duration) error
	// ScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
	ScheduleWithCron(ctx context.Context, message proto.Message, pid *PID, cronExpression string) error
	// RemoteScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
	RemoteScheduleWithCron(ctx context.Context, message proto.Message, address *address.Address, cronExpression string) error
	// PeerAddress returns the actor system address known in the cluster. That address is used by other nodes to communicate with the actor system.
	// This address is empty when cluster mode is not activated
	PeerAddress() string
	// Register registers an actor for future use. This is necessary when creating an actor remotely
	Register(ctx context.Context, actor Actor) error
	// Deregister removes a registered actor from the registry
	Deregister(ctx context.Context, actor Actor) error
	// Logger returns the logger sets when creating the actor system
	Logger() log.Logger
	// Host returns the actor system node host address
	// This is the bind address for remote communication
	Host() string
	// Port returns the actor system node port.
	// This is the bind port for remote communication
	Port() int
	// Uptime returns the number of seconds since the actor system started
	Uptime() int64
	// Running returns true when the actor system is running
	Running() bool
	// Run starts the actor system, blocks on the signals channel, and then
	// gracefully shuts the application down.
	// It's designed to make typical applications simple to run.
	// All of Run's functionality is implemented in terms of the exported
	// Start and Stop methods. Applications with more specialized needs
	// can use those methods directly instead of relying on Run.
	Run(ctx context.Context, startHook func(ctx context.Context) error, stopHook func(ctx context.Context) error)
	// handleRemoteAsk handles a synchronous message to another actor and expect a response.
	// This block until a response is received or timed out.
	handleRemoteAsk(ctx context.Context, to *PID, message proto.Message, timeout time.Duration) (response proto.Message, err error)
	// handleRemoteTell handles an asynchronous message to an actor
	handleRemoteTell(ctx context.Context, to *PID, message proto.Message) error
	// broadcastActor sets actor in the actor system actors registry
	broadcastActor(actor *PID)
	// getPeerStateFromStore returns the peer state from the cluster store
	getPeerStateFromStore(address string) (*internalpb.PeerState, error)
	// removePeerStateFromStore removes the peer state from the cluster store
	removePeerStateFromStore(address string) error
	// reservedName returns reserved actor's name
	reservedName(nameType nameType) string
	// getCluster returns the cluster engine
	getCluster() cluster.Interface
	// tree returns the actors tree
	tree() *pidTree

	completeRebalancing()
	getRootGuardian() *PID
	getSystemGuardian() *PID
	getUserGuardian() *PID
	getDeathWatch() *PID
	getDeadletter() *PID
	getSingletonManager() *PID
}

// ActorSystem represent a collection of actors on a given node
// Only a single instance of the ActorSystem can be created on a given node
type actorSystem struct {
	// hold the actors tree in the system
	actors *pidTree

	// states whether the actor system has started or not
	started atomic.Bool

	// Specifies the actor system name
	name string
	// Specifies the logger to use in the system
	logger log.Logger
	// Specifies at what point in time to passivate the actor.
	// when the actor is passivated it is stopped which means it does not consume
	// any further resources like memory and cpu. The default value is 5s
	passivationAfter time.Duration
	// Specifies how long the sender of a message should wait to receive a reply
	// when using SendReply. The default value is 5s
	askTimeout time.Duration
	// Specifies the shutdown timeout. The default value is 30s
	shutdownTimeout time.Duration
	// Specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	actorInitMaxRetries int
	// Specifies the actors initialization timeout
	// The default value is 1s
	actorInitTimeout time.Duration

	// Specifies whether remoting is enabled.
	// This allows to handle remote messaging
	remotingEnabled atomic.Bool
	remoting        *Remoting
	// Specifies the remoting server
	server       *nethttp.Server
	listener     net.Listener
	remoteConfig *remote.Config

	// cluster settings
	clusterEnabled atomic.Bool
	// cluster mode
	cluster            cluster.Interface
	wireActorsQueue    chan *internalpb.ActorRef
	eventsQueue        <-chan *cluster.Event
	clusterSyncStopSig chan types.Unit
	partitionHasher    hash.Hasher
	clusterNode        *discovery.Node

	// help protect some the fields to set
	locker sync.Mutex
	// specifies the stash capacity
	stashEnabled bool

	stopGC          chan types.Unit
	janitorInterval time.Duration

	// specifies the events stream
	eventsStream eventstream.Stream

	// specifies the message scheduler
	scheduler *scheduler

	registry   types.Registry
	reflection *reflection

	peersStateLoopInterval time.Duration
	clusterStore           *cluster.Store
	clusterConfig          *ClusterConfig
	rebalancingQueue       chan *internalpb.PeerState
	rebalancedNodes        goset.Set[string]

	rebalancer       *PID
	rootGuardian     *PID
	userGuardian     *PID
	systemGuardian   *PID
	deathWatch       *PID
	deadletter       *PID
	singletonManager *PID

	startedAt       *atomic.Int64
	rebalancing     *atomic.Bool
	rebalanceLocker *sync.Mutex
	shutdownHooks   []ShutdownHook

	actorsCounter      *atomic.Uint64
	deadlettersCounter *atomic.Uint64

	clientTLS *tls.Config
	serverTLS *tls.Config
}

var (
	// enforce compilation error when all methods of the ActorSystem interface are not implemented
	// by the struct actorSystem
	_ ActorSystem                              = (*actorSystem)(nil)
	_ internalpbconnect.RemotingServiceHandler = (*actorSystem)(nil)
	_ internalpbconnect.ClusterServiceHandler  = (*actorSystem)(nil)
)

// NewActorSystem creates an instance of ActorSystem
func NewActorSystem(name string, opts ...Option) (ActorSystem, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	if match, _ := regexp.MatchString("^[a-zA-Z0-9][a-zA-Z0-9-_]*$", name); !match {
		return nil, ErrInvalidActorSystemName
	}

	system := &actorSystem{
		wireActorsQueue:        make(chan *internalpb.ActorRef, 10),
		name:                   name,
		logger:                 log.New(log.ErrorLevel, os.Stderr),
		passivationAfter:       DefaultPassivationTimeout,
		actorInitMaxRetries:    DefaultInitMaxRetries,
		locker:                 sync.Mutex{},
		shutdownTimeout:        DefaultShutdownTimeout,
		stashEnabled:           false,
		stopGC:                 make(chan types.Unit, 1),
		janitorInterval:        DefaultJanitorInterval,
		eventsStream:           eventstream.New(),
		partitionHasher:        hash.DefaultHasher(),
		actorInitTimeout:       DefaultInitTimeout,
		eventsQueue:            make(chan *cluster.Event, 1),
		registry:               types.NewRegistry(),
		clusterSyncStopSig:     make(chan types.Unit, 1),
		peersStateLoopInterval: DefaultPeerStateLoopInterval,
		remoteConfig:           remote.DefaultConfig(),
		actors:                 newTree(),
		startedAt:              atomic.NewInt64(0),
		rebalancing:            atomic.NewBool(false),
		shutdownHooks:          make([]ShutdownHook, 0),
		rebalancedNodes:        goset.NewSet[string](),
		rebalanceLocker:        &sync.Mutex{},
		actorsCounter:          atomic.NewUint64(0),
		deadlettersCounter:     atomic.NewUint64(0),
	}

	system.started.Store(false)
	system.remotingEnabled.Store(false)
	system.clusterEnabled.Store(false)

	system.reflection = newReflection(system.registry)

	// apply the various options
	for _, opt := range opts {
		opt.Apply(system)
	}

	if err := system.remoteConfig.Sanitize(); err != nil {
		return nil, err
	}

	// we need to make sure the cluster kinds are defined
	if system.clusterEnabled.Load() {
		if err := system.clusterConfig.Validate(); err != nil {
			return nil, err
		}
	}

	// perform some quick validations on the TLS configurations
	if (system.serverTLS == nil) != (system.clientTLS == nil) {
		return nil, ErrInvalidTLSConfiguration
	}

	// append the right protocols to the TLS settings
	system.ensureTLSProtos()

	return system, nil
}

// Run starts the actor system, blocks on the signals channel, and then
// gracefully shuts the application down and terminate the running processing.
// It's designed to make typical applications simple to run.
// The minimal GoAkt application looks like this:
//
//	NewActorSystem(name, opts).Run(ctx, startHook, stopHook)
//
// All of Run's functionality is implemented in terms of the exported
// Start and Stop methods. Applications with more specialized needs
// can use those methods directly instead of relying on Run.
func (x *actorSystem) Run(ctx context.Context, startHook func(ctx context.Context) error, stophook func(ctx context.Context) error) {
	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(startHook(ctx)).
		AddError(x.Start(ctx)).
		Error(); err != nil {
		x.logger.Fatal(err)
		os.Exit(1)
		return
	}

	// wait for interruption/termination
	notifier := make(chan os.Signal, 1)
	done := make(chan types.Unit, 1)
	signal.Notify(notifier, syscall.SIGINT, syscall.SIGTERM)
	// wait for a shutdown signal, and then shutdown
	go func() {
		sig := <-notifier
		x.logger.Infof("received an interrupt signal (%s) to shutdown", sig.String())

		if err := errorschain.
			New(errorschain.ReturnFirst()).
			AddError(stophook(ctx)).
			AddError(x.Stop(ctx)).
			Error(); err != nil {
			x.logger.Fatal(err)
		}

		signal.Stop(notifier)
		done <- types.Unit{}
	}()
	<-done
	pid := os.Getpid()
	// make sure if it is unix init process to exit
	if pid == 1 {
		os.Exit(0)
	}

	process, _ := os.FindProcess(pid)
	switch {
	case runtime.GOOS == "windows":
		_ = process.Kill()
	default:
		_ = process.Signal(syscall.SIGTERM)
	}
}

// Start initializes the actor system.
// To guarantee a clean shutdown during unexpected system terminations,
// developers must handle SIGTERM and SIGINT signals appropriately and invoke Stop.
func (x *actorSystem) Start(ctx context.Context) error {
	x.logger.Infof("%s actor system starting on %s/%s..", x.name, runtime.GOOS, runtime.GOARCH)
	x.started.Store(true)
	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(x.enableRemoting(ctx)).
		AddError(x.enableClustering(ctx)).
		AddError(x.spawnRootGuardian(ctx)).
		AddError(x.spawnSystemGuardian(ctx)).
		AddError(x.spawnUserGuardian(ctx)).
		AddError(x.spawnRebalancer(ctx)).
		AddError(x.spawnDeathWatch(ctx)).
		AddError(x.spawnDeadletter(ctx)).
		AddError(x.spawnSingletonManager(ctx)).
		Error(); err != nil {
		return errorschain.
			New(errorschain.ReturnAll()).
			AddError(err).
			AddError(x.shutdown(ctx)).
			Error()
	}

	x.startMessagesScheduler(ctx)
	x.startedAt.Store(time.Now().Unix())
	x.logger.Infof("%s actor system successfully started..:)", x.name)
	return nil
}

// Stop stops the actor system and does not terminate the program.
// One needs to explicitly call os.Exit to terminate the program.
func (x *actorSystem) Stop(ctx context.Context) error {
	return x.shutdown(ctx)
}

// Metric returns the actor system metrics.
// The metrics does not include any cluster data
func (x *actorSystem) Metric(ctx context.Context) *Metric {
	if x.started.Load() {
		x.getSetDeadlettersCount(ctx)
		return &Metric{
			deadlettersCount: int64(x.deadlettersCounter.Load()),
			actorsCount:      int64(x.actorsCounter.Load()),
			uptime:           x.Uptime(),
		}
	}
	return nil
}

// Running returns true when the actor system is running
func (x *actorSystem) Running() bool {
	return x.started.Load()
}

// Uptime returns the number of seconds since the actor system started
func (x *actorSystem) Uptime() int64 {
	if x.started.Load() {
		return time.Now().Unix() - x.startedAt.Load()
	}
	return 0
}

// Host returns the actor system node host address
// This is the bind address for remote communication
func (x *actorSystem) Host() string {
	x.locker.Lock()
	host := x.remoteConfig.BindAddr()
	x.locker.Unlock()
	return host
}

// Port returns the actor system node port.
// This is the bind port for remote communication
func (x *actorSystem) Port() int {
	x.locker.Lock()
	port := x.remoteConfig.BindPort()
	x.locker.Unlock()
	return port
}

// Logger returns the logger sets when creating the actor system
func (x *actorSystem) Logger() log.Logger {
	x.locker.Lock()
	logger := x.logger
	x.locker.Unlock()
	return logger
}

// Deregister removes a registered actor from the registry
func (x *actorSystem) Deregister(_ context.Context, actor Actor) error {
	x.locker.Lock()
	defer x.locker.Unlock()

	if !x.started.Load() {
		return ErrActorSystemNotStarted
	}

	x.registry.Deregister(actor)
	return nil
}

// Register registers an actor for future use. This is necessary when creating an actor remotely
func (x *actorSystem) Register(_ context.Context, actor Actor) error {
	x.locker.Lock()
	defer x.locker.Unlock()

	if !x.started.Load() {
		return ErrActorSystemNotStarted
	}

	x.registry.Register(actor)
	return nil
}

// Schedule schedules a message that will be delivered to the receiver actor
// This will send the given message to the actor at the given interval specified.
func (x *actorSystem) Schedule(ctx context.Context, message proto.Message, pid *PID, interval time.Duration) error {
	return x.scheduler.Schedule(ctx, message, pid, interval)
}

// RemoteSchedule schedules a message to be sent to a remote actor in the future.
// This requires remoting to be enabled on the actor system.
// This will send the given message to the actor at the given interval specified
func (x *actorSystem) RemoteSchedule(ctx context.Context, message proto.Message, address *address.Address, interval time.Duration) error {
	return x.scheduler.RemoteSchedule(ctx, message, address, interval)
}

// ScheduleOnce schedules a message that will be delivered to the receiver actor
// This will send the given message to the actor after the given interval specified.
// The message will be sent once
func (x *actorSystem) ScheduleOnce(ctx context.Context, message proto.Message, pid *PID, interval time.Duration) error {
	return x.scheduler.ScheduleOnce(ctx, message, pid, interval)
}

// RemoteScheduleOnce schedules a message to be sent to a remote actor in the future.
// This requires remoting to be enabled on the actor system.
// This will send the given message to the actor after the given interval specified
// The message will be sent once
func (x *actorSystem) RemoteScheduleOnce(ctx context.Context, message proto.Message, address *address.Address, interval time.Duration) error {
	return x.scheduler.RemoteScheduleOnce(ctx, message, address, interval)
}

// ScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (x *actorSystem) ScheduleWithCron(ctx context.Context, message proto.Message, pid *PID, cronExpression string) error {
	return x.scheduler.ScheduleWithCron(ctx, message, pid, cronExpression)
}

// RemoteScheduleWithCron schedules a message to be sent to an actor in the future using a cron expression.
func (x *actorSystem) RemoteScheduleWithCron(ctx context.Context, message proto.Message, address *address.Address, cronExpression string) error {
	return x.scheduler.RemoteScheduleWithCron(ctx, message, address, cronExpression)
}

// Subscribe creates an event subscriber to consume events from the actor system.
// Remember to use the Unsubscribe method to avoid resource leakage.
func (x *actorSystem) Subscribe() (eventstream.Subscriber, error) {
	if !x.started.Load() {
		return nil, ErrActorSystemNotStarted
	}
	x.locker.Lock()
	subscriber := x.eventsStream.AddSubscriber()
	x.eventsStream.Subscribe(subscriber, eventsTopic)
	x.locker.Unlock()
	return subscriber, nil
}

// Unsubscribe unsubscribes a subscriber.
func (x *actorSystem) Unsubscribe(subscriber eventstream.Subscriber) error {
	if !x.started.Load() {
		return ErrActorSystemNotStarted
	}
	x.locker.Lock()
	x.eventsStream.Unsubscribe(subscriber, eventsTopic)
	x.eventsStream.RemoveSubscriber(subscriber)
	x.locker.Unlock()
	return nil
}

// GetPartition returns the partition where a given actor is located
func (x *actorSystem) GetPartition(actorName string) int {
	if !x.InCluster() {
		// TODO: maybe add a partitioner function
		return 0
	}
	return x.cluster.GetPartition(actorName)
}

// InCluster states whether the actor system is started within a cluster of nodes
func (x *actorSystem) InCluster() bool {
	return x.clusterEnabled.Load() && x.cluster != nil
}

// NumActors returns the total number of active actors on a given running node.
// This does not account for the total number of actors in the cluster
func (x *actorSystem) NumActors() uint64 {
	return x.actorsCounter.Load()
}

// Spawn creates or returns the instance of a given actor in the system
func (x *actorSystem) Spawn(ctx context.Context, name string, actor Actor, opts ...SpawnOption) (*PID, error) {
	if !x.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	// check some preconditions
	if err := x.checkSpawnPreconditions(ctx, name, actor, false); err != nil {
		return nil, err
	}

	actorAddress := x.actorAddress(name)
	pidNode, exist := x.actors.GetNode(actorAddress.String())
	if exist {
		pid := pidNode.GetValue()
		if pid.IsRunning() {
			return pid, nil
		}
	}

	pid, err := x.configPID(ctx, name, actor, opts...)
	if err != nil {
		return nil, err
	}

	x.actorsCounter.Inc()
	// add the given actor to the tree and supervise it
	guardian := x.getUserGuardian()
	_ = x.actors.AddNode(guardian, pid)
	x.actors.AddWatcher(pid, x.deathWatch)
	x.broadcastActor(pid)
	return pid, nil
}

// SpawnNamedFromFunc creates an actor with the given receive function and provided name. One can set the PreStart and PostStop lifecycle hooks
// in the given optional options
func (x *actorSystem) SpawnNamedFromFunc(ctx context.Context, name string, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error) {
	if !x.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	config := newFuncConfig(opts...)
	actor := newFuncActor(name, receiveFunc, config)

	// check some preconditions
	if err := x.checkSpawnPreconditions(ctx, name, actor, false); err != nil {
		return nil, err
	}

	actorAddress := x.actorAddress(name)
	pidNode, exist := x.actors.GetNode(actorAddress.String())
	if exist {
		pid := pidNode.GetValue()
		if pid.IsRunning() {
			return pid, nil
		}
	}

	pid, err := x.configPID(ctx, name, actor, WithMailbox(config.mailbox))
	if err != nil {
		return nil, err
	}

	x.actorsCounter.Inc()
	_ = x.actors.AddNode(x.userGuardian, pid)
	x.actors.AddWatcher(pid, x.deathWatch)
	x.broadcastActor(pid)
	return pid, nil
}

// SpawnFromFunc creates an actor with the given receive function.
func (x *actorSystem) SpawnFromFunc(ctx context.Context, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error) {
	return x.SpawnNamedFromFunc(ctx, uuid.NewString(), receiveFunc, opts...)
}

// SpawnRouter creates a new router. One can additionally set the router options.
// A router is a special type of actor that helps distribute messages of the same type over a set of actors, so that messages can be processed in parallel.
// A single actor will only process one message at a time.
func (x *actorSystem) SpawnRouter(ctx context.Context, poolSize int, routeesKind Actor, opts ...RouterOption) (*PID, error) {
	router := newRouter(poolSize, routeesKind, x.logger, opts...)
	routerName := x.reservedName(routerType)
	return x.Spawn(ctx, routerName, router)
}

// SpawnSingleton creates a singleton actor in the system.
//
// A singleton actor is instantiated when cluster mode is enabled.
// A singleton actor like any other actor is created only once within the system and in the cluster.
// A singleton actor is created with the default supervisor strategy and directive.
// A singleton actor once created lives throughout the lifetime of the given actor system.
// One cannot create a child actor for a singleton actor.
//
// The cluster singleton is automatically started on the oldest node in the cluster.
// When the oldest node leaves the cluster unexpectedly, the singleton is restarted on the new oldest node.
// This is useful for managing shared resources or coordinating tasks that should be handled by a single actor.
func (x *actorSystem) SpawnSingleton(ctx context.Context, name string, actor Actor) error {
	if !x.started.Load() {
		return ErrActorSystemNotStarted
	}

	if !x.InCluster() {
		return ErrClusterDisabled
	}

	cl := x.getCluster()

	// only create the singleton actor on the oldest node in the cluster
	if !cl.IsLeader(ctx) {
		return x.spawnSingletonOnLeader(ctx, cl, name, actor)
	}

	// check some preconditions
	if err := x.checkSpawnPreconditions(ctx, name, actor, true); err != nil {
		return err
	}

	pid, err := x.configPID(ctx, name, actor,
		WithLongLived(),
		withSingleton(),
		WithSupervisor(
			NewSupervisor(
				WithStrategy(OneForOneStrategy),
				WithDirective(PanicError{}, StopDirective),
				WithDirective(InternalError{}, StopDirective),
				WithDirective(&runtime.PanicNilError{}, StopDirective),
			),
		))
	if err != nil {
		return err
	}

	x.actorsCounter.Inc()
	// add the given actor to the tree and supervise it
	_ = x.actors.AddNode(x.singletonManager, pid)
	x.actors.AddWatcher(pid, x.deathWatch)
	x.broadcastActor(pid)
	return nil
}

// Kill stops a given actor in the system
func (x *actorSystem) Kill(ctx context.Context, name string) error {
	if !x.started.Load() {
		return ErrActorSystemNotStarted
	}

	actorAddress := x.actorAddress(name)
	pidNode, exist := x.actors.GetNode(actorAddress.String())
	if exist {
		pid := pidNode.GetValue()
		// decrement the actors count since we are stopping the actor
		x.actorsCounter.Dec()
		return pid.Shutdown(ctx)
	}

	return ErrActorNotFound(actorAddress.String())
}

// ReSpawn recreates a given actor in the system
// During restart all messages that are in the mailbox and not yet processed will be ignored.
// Only the direct alive children of the given actor will be shudown and respawned with their initial state.
// Bear in mind that restarting an actor will reinitialize the actor to initial state.
// In case any of the direct child restart fails the given actor will not be started at all.
func (x *actorSystem) ReSpawn(ctx context.Context, name string) (*PID, error) {
	if !x.started.Load() {
		return nil, ErrActorSystemNotStarted
	}

	actorAddress := x.actorAddress(name)
	pidNode, exist := x.actors.GetNode(actorAddress.String())
	if exist {
		pid := pidNode.GetValue()

		parent := NoSender
		if parentNode, ok := x.actors.ParentAt(pid, 0); ok {
			parent = parentNode.GetValue()
		}

		if err := pid.Restart(ctx); err != nil {
			return nil, fmt.Errorf("failed to restart actor=%s: %w", actorAddress.String(), err)
		}

		// no need to handle the error here because the only time this method
		// returns an error if when the parent does not exist which was taken care of in the
		// lines above
		_ = x.actors.AddNode(parent, pid)
		x.actors.AddWatcher(pid, x.deathWatch)
		return pid, nil
	}

	return nil, ErrActorNotFound(actorAddress.String())
}

// Name returns the actor system name
func (x *actorSystem) Name() string {
	x.locker.Lock()
	name := x.name
	x.locker.Unlock()
	return name
}

// Actors returns the list of Actors that are alive on a given running node.
// This does not account for the total number of actors in the cluster
func (x *actorSystem) Actors() []*PID {
	x.locker.Lock()
	pidNodes := x.actors.Nodes()
	x.locker.Unlock()
	actors := make([]*PID, 0, len(pidNodes))
	for _, pidNode := range pidNodes {
		pid := pidNode.GetValue()
		if !isReservedName(pid.Name()) {
			actors = append(actors, pid)
		}
	}
	return actors
}

// ActorRefs retrieves a list of active actors, including both local actors
// and, when cluster mode is enabled, actors across the cluster. Use this
// method cautiously, as the scanning process may impact system performance.
// If the cluster request fails, only locally active actors will be returned.
// The timeout parameter defines the maximum duration for cluster-based requests
// before they are terminated.
func (x *actorSystem) ActorRefs(ctx context.Context, timeout time.Duration) []ActorRef {
	pids := x.Actors()
	actorRefs := make([]ActorRef, len(pids))
	uniques := make(map[string]types.Unit)
	for index, pid := range pids {
		actorRefs[index] = fromPID(pid)
		uniques[pid.Address().String()] = types.Unit{}
	}

	if x.InCluster() {
		if actors, err := x.getCluster().Actors(ctx, timeout); err == nil {
			for _, actor := range actors {
				actorRef := fromActorRef(actor)
				if _, ok := uniques[actorRef.Address().String()]; !ok {
					actorRefs = append(actorRefs, actorRef)
				}
			}
		}
	}

	return actorRefs
}

// PeerAddress returns the actor system address known in the cluster. That address is used by other nodes to communicate with the actor system.
// This address is empty when cluster mode is not activated
func (x *actorSystem) PeerAddress() string {
	x.locker.Lock()
	defer x.locker.Unlock()
	if x.clusterEnabled.Load() {
		return x.clusterNode.PeersAddress()
	}
	return ""
}

// ActorOf retrieves an existing actor within the local system or across the cluster if clustering is enabled.
//
// If the actor is found locally, its PID is returned. If the actor resides on a remote host, its address is returned.
// If the actor is not found, an error of type "actor not found" is returned.
func (x *actorSystem) ActorOf(ctx context.Context, actorName string) (addr *address.Address, pid *PID, err error) {
	x.locker.Lock()

	if !x.started.Load() {
		x.locker.Unlock()
		return nil, nil, ErrActorSystemNotStarted
	}

	// first check whether the actor exist locally
	actorAddress := x.actorAddress(actorName)
	if lpidNode, ok := x.actors.GetNode(actorAddress.String()); ok {
		x.locker.Unlock()
		lpid := lpidNode.GetValue()
		return lpid.Address(), lpid, nil
	}

	// check in the cluster
	if x.clusterEnabled.Load() {
		actor, err := x.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				x.logger.Infof("actor=%s not found", actorName)
				x.locker.Unlock()
				return nil, nil, ErrActorNotFound(actorName)
			}

			x.locker.Unlock()
			return nil, nil, fmt.Errorf("failed to fetch remote actor=%s: %w", actorName, err)
		}

		x.locker.Unlock()
		return address.From(actor.GetActorAddress()), nil, nil
	}

	if x.remotingEnabled.Load() {
		x.locker.Unlock()
		return nil, nil, ErrMethodCallNotAllowed
	}

	x.logger.Infof("actor=%s not found", actorName)
	x.locker.Unlock()
	return nil, nil, ErrActorNotFound(actorName)
}

// LocalActor returns the reference of a local actor.
// A local actor is an actor that reside on the same node where the given actor system has started
func (x *actorSystem) LocalActor(actorName string) (*PID, error) {
	x.locker.Lock()

	if !x.started.Load() {
		x.locker.Unlock()
		return nil, ErrActorSystemNotStarted
	}

	actorAddress := x.actorAddress(actorName)
	if lpidNode, ok := x.actors.GetNode(actorAddress.String()); ok {
		x.locker.Unlock()
		lpid := lpidNode.GetValue()
		return lpid, nil
	}

	x.logger.Infof("actor=%s not found", actorName)
	x.locker.Unlock()
	return nil, ErrActorNotFound(actorName)
}

// RemoteActor returns the address of a remote actor when cluster is enabled
// When the cluster mode is not enabled an actor not found error will be returned
// One can always check whether cluster is enabled before calling this method or just use the ActorOf method.
func (x *actorSystem) RemoteActor(ctx context.Context, actorName string) (addr *address.Address, err error) {
	x.locker.Lock()

	if !x.started.Load() {
		e := ErrActorSystemNotStarted
		x.locker.Unlock()
		return nil, e
	}

	if x.cluster == nil {
		x.locker.Unlock()
		return nil, ErrClusterDisabled
	}

	actor, err := x.cluster.GetActor(ctx, actorName)
	if err != nil {
		if errors.Is(err, cluster.ErrActorNotFound) {
			x.logger.Infof("actor=%s not found", actorName)
			x.locker.Unlock()
			return nil, ErrActorNotFound(actorName)
		}

		x.locker.Unlock()
		return nil, fmt.Errorf("failed to fetch remote actor=%s: %w", actorName, err)
	}

	x.locker.Unlock()
	return address.From(actor.GetActorAddress()), nil
}

// RemoteLookup for an actor on a remote host.
func (x *actorSystem) RemoteLookup(ctx context.Context, request *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error) {
	logger := x.logger
	msg := request.Msg

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	if x.clusterEnabled.Load() {
		actorName := msg.GetName()
		actor, err := x.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				logger.Error(ErrAddressNotFound(actorName).Error())
				return nil, ErrAddressNotFound(actorName)
			}

			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: actor.GetActorAddress()}), nil
	}

	addr := address.New(msg.GetName(), x.Name(), msg.GetHost(), int(msg.GetPort()))
	pidNode, exist := x.actors.GetNode(addr.String())
	if !exist {
		logger.Error(ErrAddressNotFound(addr.String()).Error())
		return nil, ErrAddressNotFound(addr.String())
	}

	pid := pidNode.GetValue()
	return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: pid.Address().Address}), nil
}

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately. With this type of message the receiver cannot communicate back to Sender
// except reply the message with a response. This one-way communication
func (x *actorSystem) RemoteAsk(ctx context.Context, stream *connect.BidiStream[internalpb.RemoteAskRequest, internalpb.RemoteAskResponse]) error {
	logger := x.logger

	if !x.remotingEnabled.Load() {
		return connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	for {
		switch err := ctx.Err(); {
		case errors.Is(err, context.Canceled):
			return connect.NewError(connect.CodeCanceled, err)
		case errors.Is(err, context.DeadlineExceeded):
			return connect.NewError(connect.CodeDeadlineExceeded, err)
		}

		request, err := stream.Receive()
		if eof(err) {
			return nil
		}

		if err != nil {
			logger.Error(err)
			return connect.NewError(connect.CodeUnknown, err)
		}

		message := request.GetRemoteMessage()
		receiver := message.GetReceiver()
		name := receiver.GetName()

		remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
		if remoteAddr != net.JoinHostPort(receiver.GetHost(), strconv.Itoa(int(receiver.GetPort()))) {
			return connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
		}

		addr := x.actorAddress(name)
		pidNode, exist := x.actors.GetNode(addr.String())
		if !exist {
			logger.Error(ErrAddressNotFound(addr.String()).Error())
			return ErrAddressNotFound(addr.String())
		}

		timeout := x.askTimeout
		if request.GetTimeout() != nil {
			timeout = request.GetTimeout().AsDuration()
		}

		pid := pidNode.GetValue()
		reply, err := x.handleRemoteAsk(ctx, pid, message, timeout)
		if err != nil {
			logger.Error(ErrRemoteSendFailure(err).Error())
			return ErrRemoteSendFailure(err)
		}

		marshaled, _ := anypb.New(reply)
		response := &internalpb.RemoteAskResponse{Message: marshaled}
		if err := stream.Send(response); err != nil {
			return connect.NewError(connect.CodeUnknown, err)
		}
	}
}

// RemoteTell is used to send a message to an actor remotely by another actor
func (x *actorSystem) RemoteTell(ctx context.Context, stream *connect.ClientStream[internalpb.RemoteTellRequest]) (*connect.Response[internalpb.RemoteTellResponse], error) {
	logger := x.logger

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	requestc := make(chan *internalpb.RemoteTellRequest, 1)
	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(2)

	eg.Go(
		func() error {
			defer close(requestc)
			for stream.Receive() {
				select {
				case requestc <- stream.Msg():
				case <-ctx.Done():
					logger.Error(ctx.Err())
					return connect.NewError(connect.CodeCanceled, ctx.Err())
				}
			}

			if err := stream.Err(); err != nil {
				logger.Error(err)
				return connect.NewError(connect.CodeUnknown, err)
			}

			return nil
		},
	)

	eg.Go(
		func() error {
			for request := range requestc {
				receiver := request.GetRemoteMessage().GetReceiver()
				addr := address.New(receiver.GetName(), x.Name(), receiver.GetHost(), int(receiver.GetPort()))
				pidNode, exist := x.actors.GetNode(addr.String())
				if !exist {
					logger.Error(ErrAddressNotFound(addr.String()).Error())
					return ErrAddressNotFound(addr.String())
				}

				pid := pidNode.GetValue()
				if err := x.handleRemoteTell(ctx, pid, request.GetRemoteMessage()); err != nil {
					logger.Error(ErrRemoteSendFailure(err))
					return ErrRemoteSendFailure(err)
				}
			}
			return nil
		},
	)

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return connect.NewResponse(new(internalpb.RemoteTellResponse)), nil
}

// RemoteReSpawn is used the handle the re-creation of an actor from a remote host or from an api call
func (x *actorSystem) RemoteReSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteReSpawnRequest]) (*connect.Response[internalpb.RemoteReSpawnResponse], error) {
	logger := x.logger

	msg := request.Msg

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorAddress := address.New(msg.GetName(), x.Name(), msg.GetHost(), int(msg.GetPort()))
	pidNode, exist := x.actors.GetNode(actorAddress.String())
	if !exist {
		logger.Error(ErrAddressNotFound(actorAddress.String()).Error())
		return nil, ErrAddressNotFound(actorAddress.String())
	}

	pid := pidNode.GetValue()
	parent := NoSender
	if parentNode, ok := x.actors.ParentAt(pid, 0); ok {
		parent = parentNode.GetValue()
	}

	if err := pid.Restart(ctx); err != nil {
		return nil, fmt.Errorf("failed to restart actor=%s: %w", actorAddress.String(), err)
	}

	if err := x.actors.AddNode(parent, pid); err != nil {
		return nil, err
	}

	return connect.NewResponse(new(internalpb.RemoteReSpawnResponse)), nil
}

// RemoteStop stops an actor on a remote machine
func (x *actorSystem) RemoteStop(ctx context.Context, request *connect.Request[internalpb.RemoteStopRequest]) (*connect.Response[internalpb.RemoteStopResponse], error) {
	logger := x.logger

	msg := request.Msg

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorAddress := address.New(msg.GetName(), x.Name(), msg.GetHost(), int(msg.GetPort()))
	pidNode, exist := x.actors.GetNode(actorAddress.String())
	if !exist {
		logger.Error(ErrAddressNotFound(actorAddress.String()).Error())
		return nil, ErrAddressNotFound(actorAddress.String())
	}

	pid := pidNode.GetValue()
	if err := pid.Shutdown(ctx); err != nil {
		return nil, fmt.Errorf("failed to stop actor=%s: %w", actorAddress.String(), err)
	}

	return connect.NewResponse(new(internalpb.RemoteStopResponse)), nil
}

// RemoteSpawn handles the remoteSpawn call
func (x *actorSystem) RemoteSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteSpawnRequest]) (*connect.Response[internalpb.RemoteSpawnResponse], error) {
	logger := x.logger

	msg := request.Msg
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actor, err := x.reflection.ActorFrom(msg.GetActorType())
	if err != nil {
		logger.Errorf(
			"failed to create actor=[(%s) of type (%s)] on [host=%s, port=%d]: reason: (%v)",
			msg.GetActorName(), msg.GetActorType(), msg.GetHost(), msg.GetPort(), err,
		)

		if errors.Is(err, ErrTypeNotRegistered) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, ErrTypeNotRegistered)
		}

		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if msg.GetIsSingleton() {
		if err := x.SpawnSingleton(ctx, msg.GetActorName(), actor); err != nil {
			logger.Errorf("failed to create actor=(%s) on [host=%s, port=%d]: reason: (%v)", msg.GetActorName(), msg.GetHost(), msg.GetPort(), err)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}

	var opts []SpawnOption
	if !msg.GetRelocatable() {
		opts = append(opts, WithRelocationDisabled())
	}

	if _, err = x.Spawn(ctx, msg.GetActorName(), actor, opts...); err != nil {
		logger.Errorf("failed to create actor=(%s) on [host=%s, port=%d]: reason: (%v)", msg.GetActorName(), msg.GetHost(), msg.GetPort(), err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	logger.Infof("actor=(%s) successfully created on [host=%s, port=%d]", msg.GetActorName(), msg.GetHost(), msg.GetPort())
	return connect.NewResponse(new(internalpb.RemoteSpawnResponse)), nil
}

// GetNodeMetric handles the GetNodeMetric request send the given node
func (x *actorSystem) GetNodeMetric(_ context.Context, request *connect.Request[internalpb.GetNodeMetricRequest]) (*connect.Response[internalpb.GetNodeMetricResponse], error) {
	if !x.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrClusterDisabled)
	}

	req := request.Msg

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != req.GetNodeAddress() {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	actorCount := x.actors.Size()
	return connect.NewResponse(
		&internalpb.GetNodeMetricResponse{
			NodeRemoteAddress: remoteAddr,
			ActorsCount:       uint64(actorCount),
		},
	), nil
}

// GetKinds returns the cluster kinds
func (x *actorSystem) GetKinds(_ context.Context, request *connect.Request[internalpb.GetKindsRequest]) (*connect.Response[internalpb.GetKindsResponse], error) {
	if !x.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrClusterDisabled)
	}

	req := request.Msg
	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())

	// routine check
	if remoteAddr != req.GetNodeAddress() {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrInvalidHost)
	}

	kinds := make([]string, len(x.clusterConfig.Kinds()))
	for i, kind := range x.clusterConfig.Kinds() {
		kinds[i] = types.Name(kind)
	}

	return connect.NewResponse(&internalpb.GetKindsResponse{Kinds: kinds}), nil
}

// handleRemoteAsk handles a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (x *actorSystem) handleRemoteAsk(ctx context.Context, to *PID, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	return Ask(ctx, to, message, timeout)
}

// handleRemoteTell handles an asynchronous message to an actor
func (x *actorSystem) handleRemoteTell(ctx context.Context, to *PID, message proto.Message) error {
	return Tell(ctx, to, message)
}

// getRootGuardian returns the system rootGuardian guardian
func (x *actorSystem) getRootGuardian() *PID {
	x.locker.Lock()
	rootGuardian := x.rootGuardian
	x.locker.Unlock()
	return rootGuardian
}

// getUserGuardian returns the user guardian
func (x *actorSystem) getUserGuardian() *PID {
	x.locker.Lock()
	userGuardian := x.userGuardian
	x.locker.Unlock()
	return userGuardian
}

// getSystemGuardian returns the system guardian
func (x *actorSystem) getSystemGuardian() *PID {
	x.locker.Lock()
	systemGuardian := x.systemGuardian
	x.locker.Unlock()
	return systemGuardian
}

// getJanitor returns the system deathWatch
func (x *actorSystem) getDeathWatch() *PID {
	x.locker.Lock()
	janitor := x.deathWatch
	x.locker.Unlock()
	return janitor
}

// getDeadletters returns the system deadletter actor
func (x *actorSystem) getDeadletter() *PID {
	x.locker.Lock()
	deadletters := x.deadletter
	x.locker.Unlock()
	return deadletters
}

// getSingletonManager returns the system singleton manager
func (x *actorSystem) getSingletonManager() *PID {
	x.locker.Lock()
	singletonManager := x.singletonManager
	x.locker.Unlock()
	return singletonManager
}

func (x *actorSystem) completeRebalancing() {
	x.rebalancing.Store(false)
}

// removePeerStateFromStore removes the peer state from the cluster store
func (x *actorSystem) removePeerStateFromStore(address string) error {
	x.locker.Lock()
	if err := x.clusterStore.DeletePeerState(address); err != nil {
		x.locker.Unlock()
		return err
	}
	x.rebalancedNodes.Remove(address)
	x.locker.Unlock()
	return nil
}

// getPeerStateFromStore returns the peer state from the cluster store
func (x *actorSystem) getPeerStateFromStore(address string) (*internalpb.PeerState, error) {
	x.locker.Lock()
	peerState, ok := x.clusterStore.GetPeerState(address)
	x.locker.Unlock()
	if !ok {
		return nil, ErrPeerNotFound
	}
	return peerState, nil
}

// broadcastActor broadcast the newly (re)spawned actor into the cluster
func (x *actorSystem) broadcastActor(actor *PID) {
	if x.clusterEnabled.Load() {
		x.wireActorsQueue <- &internalpb.ActorRef{
			ActorAddress: actor.Address().Address,
			ActorType:    types.Name(actor.Actor()),
			IsSingleton:  actor.IsSingleton(),
			Relocatable:  actor.IsRelocatable(),
		}
	}
}

// enableClustering enables clustering. When clustering is enabled remoting is also enabled to facilitate remote
// communication
func (x *actorSystem) enableClustering(ctx context.Context) error {
	if !x.clusterEnabled.Load() {
		return nil
	}

	x.logger.Info("enabling clustering...")

	x.locker.Lock()

	if !x.remotingEnabled.Load() {
		x.logger.Error("clustering needs remoting to be enabled")
		x.locker.Unlock()
		return errors.New("clustering needs remoting to be enabled")
	}

	clusterStore, err := cluster.NewStore(x.clusterConfig.WAL(), x.logger)
	if err != nil {
		x.logger.Errorf("failed to initialize peers cache: %v", err)
		x.locker.Unlock()
		return err
	}

	x.clusterStore = clusterStore
	x.clusterNode = &discovery.Node{
		Name:          x.name,
		Host:          x.remoteConfig.BindAddr(),
		DiscoveryPort: x.clusterConfig.DiscoveryPort(),
		PeersPort:     x.clusterConfig.PeersPort(),
		RemotingPort:  x.remoteConfig.BindPort(),
	}

	clusterEngine, err := cluster.NewEngine(
		x.name,
		x.clusterConfig.Discovery(),
		x.clusterNode,
		cluster.WithLogger(x.logger),
		cluster.WithPartitionsCount(x.clusterConfig.PartitionCount()),
		cluster.WithHasher(x.partitionHasher),
		cluster.WithMinimumPeersQuorum(x.clusterConfig.MinimumPeersQuorum()),
		cluster.WithWriteQuorum(x.clusterConfig.WriteQuorum()),
		cluster.WithReadQuorum(x.clusterConfig.ReadQuorum()),
		cluster.WithReplicaCount(x.clusterConfig.ReplicaCount()),
		cluster.WithTLS(x.serverTLS, x.clientTLS),
		cluster.WithTableSize(x.clusterConfig.TableSize()),
	)
	if err != nil {
		x.logger.Errorf("failed to initialize cluster engine: %v", err)
		x.locker.Unlock()
		return err
	}

	x.logger.Info("starting cluster engine...")
	if err := clusterEngine.Start(ctx); err != nil {
		x.logger.Errorf("failed to start cluster engine: %v", err)
		x.locker.Unlock()
		return err
	}

	bootstrapChan := make(chan struct{}, 1)
	timer := time.AfterFunc(
		time.Second, func() {
			bootstrapChan <- struct{}{}
		},
	)
	<-bootstrapChan
	timer.Stop()

	x.logger.Info("cluster engine successfully started...")

	x.cluster = clusterEngine
	x.eventsQueue = clusterEngine.Events()
	x.rebalancingQueue = make(chan *internalpb.PeerState, 1)
	for _, kind := range x.clusterConfig.Kinds() {
		x.registry.Register(kind)
		x.logger.Infof("cluster kind=(%s) registered", types.Name(kind))
	}
	x.locker.Unlock()

	go x.clusterEventsLoop()
	go x.replicationLoop()
	go x.peersStateLoop()
	go x.rebalancingLoop()

	x.logger.Info("clustering enabled...:)")
	return nil
}

// enableRemoting enables the remoting service to handle remote messaging
func (x *actorSystem) enableRemoting(ctx context.Context) error {
	if !x.remotingEnabled.Load() {
		return nil
	}

	x.logger.Info("enabling remoting...")
	remotingServicePath, remotingServiceHandler := internalpbconnect.NewRemotingServiceHandler(x)
	clusterServicePath, clusterServiceHandler := internalpbconnect.NewClusterServiceHandler(x)

	mux := nethttp.NewServeMux()
	mux.Handle(remotingServicePath, remotingServiceHandler)
	mux.Handle(clusterServicePath, clusterServiceHandler)

	x.locker.Lock()
	defer x.locker.Unlock()

	// configure the appropriate server
	if err := x.configureServer(ctx, mux); err != nil {
		x.locker.Unlock()
		x.logger.Error(fmt.Errorf("failed enable remoting: %w", err))
		return err
	}

	go func() {
		if err := x.startHTTPServer(); err != nil {
			if !errors.Is(err, nethttp.ErrServerClosed) {
				x.logger.Panic(fmt.Errorf("failed to start remoting service: %w", err))
			}
		}
	}()

	// configure remoting
	x.setRemoting()
	x.logger.Info("remoting enabled...:)")
	return nil
}

// setRemoting sets the remoting service
func (x *actorSystem) setRemoting() {
	if x.clientTLS != nil {
		x.remoting = NewRemoting(
			WithRemotingTLS(x.clientTLS),
			WithRemotingMaxReadFameSize(int(x.remoteConfig.MaxFrameSize())), // nolint
		)
		return
	}
	x.remoting = NewRemoting(WithRemotingMaxReadFameSize(int(x.remoteConfig.MaxFrameSize())))
}

// startMessagesScheduler starts the messages scheduler
func (x *actorSystem) startMessagesScheduler(ctx context.Context) {
	// set the scheduler
	x.scheduler = newScheduler(x.logger,
		x.shutdownTimeout,
		withSchedulerCluster(x.cluster),
		withSchedulerRemoting(x.remoting))
	// start the scheduler
	x.scheduler.Start(ctx)
}

func (x *actorSystem) ensureTLSProtos() {
	if x.serverTLS != nil && x.clientTLS != nil {
		// ensure that the required protocols are set for the TLS
		toAdd := []string{"h2", "http/1.1"}

		// server application protocols setting
		protos := goset.NewSet[string](x.serverTLS.NextProtos...)
		protos.Append(toAdd...)
		x.serverTLS.NextProtos = protos.ToSlice()

		// client application protocols setting
		protos = goset.NewSet[string](x.clientTLS.NextProtos...)
		protos.Append(toAdd...)
		x.clientTLS.NextProtos = protos.ToSlice()
	}
}

// reset the actor system
func (x *actorSystem) reset() {
	x.actors.Reset()
}

// shutdown stops the actor system
func (x *actorSystem) shutdown(ctx context.Context) error {
	// make sure the actor system has started
	if !x.started.Load() {
		return ErrActorSystemNotStarted
	}

	x.stopGC <- types.Unit{}
	x.logger.Infof("%s is shutting down..:)", x.name)

	x.started.Store(false)
	if x.scheduler != nil {
		x.scheduler.Stop(ctx)
	}

	// run the various shutdown hooks
	for _, hook := range x.shutdownHooks {
		if err := hook(ctx); err != nil {
			x.reset()
			return err
		}
	}

	ctx, cancel := context.WithTimeout(ctx, x.shutdownTimeout)
	defer cancel()

	var actorRefs []ActorRef
	for _, actor := range x.Actors() {
		actorRefs = append(actorRefs, fromPID(actor))
	}

	if err := x.getRootGuardian().Shutdown(ctx); err != nil {
		x.reset()
		x.logger.Errorf("%s failed to shutdown cleanly: %w", x.name, err)
		return err
	}
	x.actors.DeleteNode(x.getRootGuardian())

	if x.eventsStream != nil {
		x.eventsStream.Close()
	}

	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(x.shutdownCluster(ctx, actorRefs)).
		AddError(x.shutdownRemoting(ctx)).
		Error(); err != nil {
		x.logger.Errorf("%s failed to shutdown: %w", x.name, err)
		return err
	}

	x.reset()
	x.logger.Infof("%s shuts down successfully", x.name)
	return nil
}

// replicationLoop publishes newly created actor into the cluster when cluster is enabled
func (x *actorSystem) replicationLoop() {
	for actor := range x.wireActorsQueue {
		// never replicate system actors because there are specific to the
		// started node
		if isReservedName(actor.GetActorAddress().GetName()) {
			continue
		}
		if x.InCluster() {
			ctx := context.Background()
			if err := x.cluster.PutActor(ctx, actor); err != nil {
				x.logger.Panic(err.Error())
			}
		}
	}
}

// clusterEventsLoop listens to cluster events and send them to the event streams
func (x *actorSystem) clusterEventsLoop() {
	for event := range x.eventsQueue {
		if x.InCluster() {
			if event != nil && event.Payload != nil {
				// push the event to the event stream
				message, _ := event.Payload.UnmarshalNew()
				if x.eventsStream != nil {
					x.logger.Debugf("node=(%s) publishing cluster event=(%s)....", x.name, event.Type)
					x.eventsStream.Publish(eventsTopic, message)
					x.logger.Debugf("cluster event=(%s) successfully published by node=(%s)", event.Type, x.name)
				}

				if event.Type == cluster.NodeLeft {
					nodeLeft := new(goaktpb.NodeLeft)
					_ = event.Payload.UnmarshalTo(nodeLeft)

					ctx := context.Background()

					// First check whether this node is the leader
					// only leader can start rebalancing. Just remove from the peer state from your cluster state
					// to free up resources
					if !x.cluster.IsLeader(ctx) {
						if err := x.clusterStore.DeletePeerState(nodeLeft.GetAddress()); err != nil {
							x.logger.Errorf("%s failed to remove left node=(%s) from cluster store: %w", x.name, nodeLeft.GetAddress(), err)
						}
						continue
					}

					if x.rebalancedNodes.Contains(nodeLeft.GetAddress()) {
						continue
					}

					x.rebalancedNodes.Add(nodeLeft.GetAddress())
					if peerState, ok := x.clusterStore.GetPeerState(nodeLeft.GetAddress()); ok {
						x.rebalanceLocker.Lock()
						x.rebalancingQueue <- peerState
						x.rebalanceLocker.Unlock()
					}
				}
			}
		}
	}
}

// peersStateLoop fetches the cluster peers' PeerState and update the node Store
func (x *actorSystem) peersStateLoop() {
	x.logger.Info("peers state synchronization has started...")
	ticker := ticker.New(x.peersStateLoopInterval)
	ticker.Start()
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.Ticks:
				// stop ticking and close
				if !x.started.Load() {
					tickerStopSig <- types.Unit{}
					return
				}

				eg, ctx := errgroup.WithContext(context.Background())
				peersChan := make(chan *cluster.Peer)
				eg.Go(func() error {
					defer close(peersChan)
					peers, err := x.cluster.Peers(ctx)
					if err != nil {
						return err
					}

					for _, peer := range peers {
						select {
						case peersChan <- peer:
						case <-ctx.Done():
							return ctx.Err()
						}
					}
					return nil
				})
				eg.Go(func() error {
					for peer := range peersChan {
						if err := x.processPeerState(ctx, peer); err != nil {
							return err
						}
						select {
						case <-ctx.Done():
							return ctx.Err()
						default:
							// pass
						}
					}
					return nil
				})
				if err := eg.Wait(); err != nil {
					x.logger.Error(err)
					// TODO: stop or panic
				}

			case <-x.clusterSyncStopSig:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	ticker.Stop()
	x.logger.Info("peers state synchronization has stopped...")
}

// rebalancingLoop help perform cluster rebalancing
func (x *actorSystem) rebalancingLoop() {
	for peerState := range x.rebalancingQueue {
		ctx := context.Background()
		if !x.shouldRebalance(peerState) {
			continue
		}

		if x.rebalancing.Load() {
			x.rebalanceLocker.Lock()
			x.rebalancingQueue <- peerState
			x.rebalanceLocker.Unlock()
			continue
		}

		x.rebalancing.Store(true)
		message := &internalpb.Rebalance{PeerState: peerState}
		if err := x.systemGuardian.Tell(ctx, x.rebalancer, message); err != nil {
			x.logger.Error(err)
		}
	}
}

// shouldRebalance returns true when the current node can perform the cluster rebalancing
func (x *actorSystem) shouldRebalance(peerState *internalpb.PeerState) bool {
	return !(peerState == nil ||
		!x.InCluster() ||
		proto.Equal(peerState, new(internalpb.PeerState)) ||
		len(peerState.GetActors()) == 0)
}

// processPeerState processes a given peer synchronization record.
func (x *actorSystem) processPeerState(ctx context.Context, peer *cluster.Peer) error {
	peerAddress := net.JoinHostPort(peer.Host, strconv.Itoa(peer.PeersPort))
	x.logger.Infof("processing peer sync:(%s)", peerAddress)
	peerState, err := x.cluster.GetState(ctx, peerAddress)
	if err != nil {
		if errors.Is(err, cluster.ErrPeerSyncNotFound) {
			return nil
		}
		x.logger.Error(err)
		return err
	}

	x.logger.Debugf("peer (%s) actors count (%d)", peerAddress, len(peerState.GetActors()))
	if err := x.clusterStore.PersistPeerState(peerState); err != nil {
		x.logger.Error(err)
		return err
	}
	x.logger.Infof("peer sync(%s) successfully processed", peerAddress)
	return nil
}

// configPID constructs a PID provided the actor name and the actor kind
// this is a utility function used when spawning actors
func (x *actorSystem) configPID(ctx context.Context, name string, actor Actor, opts ...SpawnOption) (*PID, error) {
	addr := x.actorAddress(name)
	if err := addr.Validate(); err != nil {
		return nil, err
	}

	// define the pid options
	// pid inherit the actor system settings defined during instantiation
	pidOpts := []pidOption{
		withInitMaxRetries(x.actorInitMaxRetries),
		withCustomLogger(x.logger),
		withActorSystem(x),
		withEventsStream(x.eventsStream),
		withInitTimeout(x.actorInitTimeout),
		withRemoting(x.remoting),
	}

	spawnConfig := newSpawnConfig(opts...)
	// set the mailbox option
	if spawnConfig.mailbox != nil {
		pidOpts = append(pidOpts, withMailbox(spawnConfig.mailbox))
	}

	// set the supervisor strategies when defined
	if spawnConfig.supervisor != nil {
		pidOpts = append(pidOpts, withSupervisor(spawnConfig.supervisor))
	}

	// define the actor as singleton when necessary
	if spawnConfig.asSingleton {
		pidOpts = append(pidOpts, asSingleton())
	}

	if !spawnConfig.relocatable {
		pidOpts = append(pidOpts, withRelocationDisabled())
	}

	// enable stash
	if x.stashEnabled {
		pidOpts = append(pidOpts, withStash())
	}

	switch {
	case isReservedName(name):
		// disable passivation for system actor
		pidOpts = append(pidOpts, withPassivationDisabled())
	default:
		switch {
		case spawnConfig.passivateAfter == nil:
			// use system-wide passivation settings
			pidOpts = append(pidOpts, withPassivationAfter(x.passivationAfter))
		case *spawnConfig.passivateAfter < longLived:
			// use custom passivation setting
			pidOpts = append(pidOpts, withPassivationAfter(*spawnConfig.passivateAfter))
		default:
			// live forever :)
			pidOpts = append(pidOpts, withPassivationDisabled())
		}
	}

	pid, err := newPID(
		ctx,
		addr,
		actor,
		pidOpts...,
	)

	if err != nil {
		return nil, err
	}
	return pid, nil
}

// tree returns the actors tree
func (x *actorSystem) tree() *pidTree {
	return x.actors
}

// getCluster returns the cluster engine
func (x *actorSystem) getCluster() cluster.Interface {
	return x.cluster
}

// reservedName returns reserved actor's name
func (x *actorSystem) reservedName(nameType nameType) string {
	return systemNames[nameType]
}

// actorAddress returns the actor path provided the actor name
func (x *actorSystem) actorAddress(name string) *address.Address {
	return address.New(name, x.name, x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
}

// startHTTPServer starts the appropriate http server
func (x *actorSystem) startHTTPServer() error {
	return x.server.Serve(x.listener)
}

// shutdownHTTPServer stops the appropriate http server
func (x *actorSystem) shutdownHTTPServer(ctx context.Context) error {
	return x.server.Shutdown(ctx)
}

// configureServer configure the various http server and listeners based upon the various settings
func (x *actorSystem) configureServer(ctx context.Context, mux *nethttp.ServeMux) error {
	hostPort := net.JoinHostPort(x.remoteConfig.BindAddr(), strconv.Itoa(x.remoteConfig.BindPort()))
	httpServer := getServer(ctx, hostPort)
	listener, err := tcp.NewKeepAliveListener(httpServer.Addr)
	if err != nil {
		return err
	}

	// Configure HTTP/2 with performance tuning
	http2Server := &http2.Server{
		MaxConcurrentStreams: 1000, // Allow up to 1000 concurrent streams
		MaxReadFrameSize:     x.remoteConfig.MaxFrameSize(),
		IdleTimeout:          x.remoteConfig.IdleTimeout(),
		WriteByteTimeout:     x.remoteConfig.WriteTimeout(),
		ReadIdleTimeout:      x.remoteConfig.ReadIdleTimeout(),
	}

	// set the http TLS server
	if x.serverTLS != nil {
		x.server = httpServer
		x.server.TLSConfig = x.serverTLS
		x.server.Handler = mux
		x.listener = tls.NewListener(listener, x.serverTLS)
		return http2.ConfigureServer(x.server, http2Server)
	}

	// http/2 server with h2c (HTTP/2 Cleartext).
	x.server = httpServer
	x.server.Handler = h2c.NewHandler(mux, http2Server)
	x.listener = listener
	return nil
}

// spawnRootGuardian creates the rootGuardian guardian
func (x *actorSystem) spawnRootGuardian(ctx context.Context) error {
	var err error
	actorName := x.reservedName(rootGuardianType)
	x.rootGuardian, err = x.configPID(ctx, actorName, newRootGuardian())
	if err != nil {
		return fmt.Errorf("actor=%s failed to start rootGuardian guardian: %w", actorName, err)
	}

	// rootGuardian is the rootGuardian node of the actors tree
	_ = x.actors.AddNode(NoSender, x.rootGuardian)
	return nil
}

// spawnSystemGuardian creates the system guardian
func (x *actorSystem) spawnSystemGuardian(ctx context.Context) error {
	var err error
	actorName := x.reservedName(systemGuardianType)

	x.systemGuardian, err = x.configPID(ctx,
		actorName,
		newSystemGuardian(),
		WithSupervisor(NewSupervisor()))
	if err != nil {
		return fmt.Errorf("actor=%s failed to start system guardian: %w", actorName, err)
	}

	// systemGuardian is a child actor of the rootGuardian actor
	_ = x.actors.AddNode(x.rootGuardian, x.systemGuardian)
	return nil
}

// spawnUserGuardian creates the user guardian
func (x *actorSystem) spawnUserGuardian(ctx context.Context) error {
	var err error
	actorName := x.reservedName(userGuardianType)
	x.userGuardian, err = x.configPID(ctx,
		actorName,
		newUserGuardian(),
		WithSupervisor(NewSupervisor()))
	if err != nil {
		return fmt.Errorf("actor=%s failed to start user guardian: %w", actorName, err)
	}

	// userGuardian is a child actor of the rootGuardian actor
	_ = x.actors.AddNode(x.rootGuardian, x.userGuardian)
	return nil
}

// spawnDeathWatch creates the deathWatch actor
func (x *actorSystem) spawnDeathWatch(ctx context.Context) error {
	var err error
	actorName := x.reservedName(deathWatchType)

	// define the supervisor strategy to use
	supervisor := NewSupervisor(
		WithStrategy(OneForOneStrategy),
		WithDirective(PanicError{}, StopDirective),
		WithDirective(InternalError{}, StopDirective),
	)

	x.deathWatch, err = x.configPID(ctx,
		actorName,
		newDeathWatch(),
		WithSupervisor(supervisor),
	)
	if err != nil {
		return fmt.Errorf("actor=%s failed to start the deathWatch: %w", actorName, err)
	}

	// the deathWatch is a child actor of the system guardian
	_ = x.actors.AddNode(x.systemGuardian, x.deathWatch)
	return nil
}

// spawnRebalancer creates the cluster rebalancer
func (x *actorSystem) spawnRebalancer(ctx context.Context) error {
	if x.clusterEnabled.Load() {
		var err error
		actorName := x.reservedName(rebalancerType)

		// define the supervisor strategy to use
		supervisor := NewSupervisor(
			WithStrategy(OneForOneStrategy),
			WithDirective(PanicError{}, RestartDirective),
			WithDirective(&runtime.PanicNilError{}, RestartDirective),
			WithDirective(rebalancingError{}, RestartDirective),
			WithDirective(InternalError{}, ResumeDirective),
			WithDirective(SpawnError{}, ResumeDirective),
		)

		x.rebalancer, err = x.configPID(ctx,
			actorName,
			newRebalancer(x.reflection, x.remoting),
			WithSupervisor(supervisor),
		)
		if err != nil {
			return fmt.Errorf("actor=%s failed to start cluster rebalancer: %w", actorName, err)
		}

		// the rebalancer is a child actor of the system guardian
		_ = x.actors.AddNode(x.systemGuardian, x.rebalancer)
	}
	return nil
}

// spawnDeadletter creates the deadletter synthetic actor
func (x *actorSystem) spawnDeadletter(ctx context.Context) error {
	var err error
	actorName := x.reservedName(deadletterType)
	x.deadletter, err = x.configPID(ctx,
		actorName,
		newDeadLetter(),
		WithSupervisor(
			NewSupervisor(
				WithStrategy(OneForOneStrategy),
				WithDirective(PanicError{}, ResumeDirective),
				WithDirective(&runtime.PanicNilError{}, ResumeDirective),
			),
		),
	)
	if err != nil {
		return fmt.Errorf("actor=%s failed to start deadletter: %w", actorName, err)
	}

	// the deadletter is a child actor of the system guardian
	_ = x.actors.AddNode(x.systemGuardian, x.deadletter)
	return nil
}

// checkSpawnPreconditions make sure before an actor is created some pre-conditions are checks
func (x *actorSystem) checkSpawnPreconditions(ctx context.Context, actorName string, kind Actor, singleton bool) error {
	// check the existence of the actor given the kind prior to creating it
	if x.clusterEnabled.Load() {
		// a singleton actor must only have one instance at a given time of its kind
		// in the whole cluster
		if singleton {
			id, err := x.cluster.LookupKind(ctx, types.Name(kind))
			if err != nil {
				return err
			}

			if id != "" {
				return ErrSingletonAlreadyExists
			}

			return nil
		}

		// here we make sure in cluster mode that the given actor is uniquely created
		// by checking both its kind and identifier
		existed, err := x.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				return nil
			}
			return err
		}

		if existed.GetActorType() == types.Name(kind) {
			return ErrActorAlreadyExists(actorName)
		}
	}

	return nil
}

// cleanupCluster cleans up the cluster
func (x *actorSystem) cleanupCluster(ctx context.Context, actorRefs []ActorRef) error {
	eg, ctx := errgroup.WithContext(ctx)

	// Remove singleton actors from the cluster
	if x.cluster.IsLeader(ctx) {
		for _, actorRef := range actorRefs {
			if actorRef.IsSingleton() {
				actorRef := actorRef
				eg.Go(func() error {
					kind := actorRef.Kind()
					if err := x.cluster.RemoveKind(ctx, kind); err != nil {
						x.logger.Errorf("failed to remove [actor kind=%s] from cluster: %v", kind, err)
						return err
					}
					x.logger.Infof("[actor kind=%s] removed from cluster", kind)
					return nil
				})
			}
		}
	}

	// Remove all actors from the cluster
	for _, actorRef := range actorRefs {
		actorRef := actorRef
		eg.Go(func() error {
			actorName := actorRef.Name()
			if err := x.cluster.RemoveActor(ctx, actorName); err != nil {
				x.logger.Errorf("failed to remove [actor=%s] from cluster: %v", actorName, err)
				return err
			}
			x.logger.Infof("[actor=%s] removed from cluster", actorName)
			return nil
		})
	}

	return eg.Wait()
}

// getSetDeadlettersCount gets and sets the deadletter count
func (x *actorSystem) getSetDeadlettersCount(ctx context.Context) {
	var (
		to      = x.getDeadletter()
		from    = x.getSystemGuardian()
		message = new(internalpb.GetDeadlettersCount)
	)
	if to.IsRunning() {
		// ask the deadletter actor for the count
		// using the default ask timeout
		// note: no need to check for error because this call is internal
		message, _ := from.Ask(ctx, to, message, DefaultAskTimeout)
		// cast the response received from the deadletter
		deadlettersCount := message.(*internalpb.DeadlettersCount)
		// set the counter
		x.deadlettersCounter.Store(uint64(deadlettersCount.GetTotalCount()))
	}
}

func (x *actorSystem) shutdownCluster(ctx context.Context, actorRefs []ActorRef) error {
	if x.clusterEnabled.Load() {
		if x.cluster != nil {
			if err := errorschain.
				New(errorschain.ReturnFirst()).
				AddError(x.cleanupCluster(ctx, actorRefs)).
				AddError(x.cluster.Stop(ctx)).
				Error(); err != nil {
				x.reset()
				x.logger.Errorf("%s failed to shutdown cleanly: %w", x.name, err)
				return err
			}
		}

		if x.wireActorsQueue != nil {
			close(x.wireActorsQueue)
		}

		x.clusterSyncStopSig <- types.Unit{}
		x.clusterEnabled.Store(false)
		x.rebalancing.Store(false)
		x.rebalanceLocker.Lock()

		if x.rebalancingQueue != nil {
			close(x.rebalancingQueue)
		}

		x.rebalanceLocker.Unlock()
		if x.clusterStore != nil {
			x.clusterStore.Close()
		}
	}
	return nil
}

func (x *actorSystem) shutdownRemoting(ctx context.Context) error {
	if x.remotingEnabled.Load() {
		if x.remoting != nil {
			x.remoting.Close()
		}

		if x.server != nil {
			if err := x.shutdownHTTPServer(ctx); err != nil {
				x.reset()
				x.logger.Errorf("%s failed to shutdown: %w", x.name, err)
				return err
			}
			x.remotingEnabled.Store(false)
			x.server = nil
			x.listener = nil
		}
	}
	return nil
}

func isReservedName(name string) bool {
	return strings.HasPrefix(name, systemNamePrefix)
}

// getServer creates an instance of http server
func getServer(ctx context.Context, address string) *nethttp.Server {
	return &nethttp.Server{
		Addr: address,
		// The maximum duration for reading the entire request, including the body.
		// It’s implemented in net/http by calling SetReadDeadline immediately after Accept
		// ReadTimeout := handler_timeout + ReadHeaderTimeout + wiggle_room
		ReadTimeout: 3 * time.Second,
		// ReadHeaderTimeout is the amount of time allowed to read request headers
		ReadHeaderTimeout: time.Second,
		// WriteTimeout is the maximum duration before timing out writes of the response.
		// It is reset whenever a new request’s header is read.
		// This effectively covers the lifetime of the ServeHTTP handler stack
		WriteTimeout: time.Second,
		// IdleTimeout is the maximum amount of time to wait for the next request when keep-alive are enabled.
		// If IdleTimeout is zero, the value of ReadTimeout is used. Not relevant to request timeouts
		IdleTimeout: 1200 * time.Second,
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}
}
