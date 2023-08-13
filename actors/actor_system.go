package actors

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sync"
	"time"

	"connectrpc.com/connect"
	otelconnect "connectrpc.com/otelconnect"
	cmp "github.com/orcaman/concurrent-map/v2"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/cluster"
	"github.com/tochemey/goakt/discovery"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/tochemey/goakt/internal/goakt/v1/goaktv1connect"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/messages/v1"
	"github.com/tochemey/goakt/pkg/resync"
	"github.com/tochemey/goakt/pkg/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/atomic"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

var system *actorSystem
var once resync.Once

// ActorSystem defines the contract of an actor system
type ActorSystem interface {
	// Name returns the actor system name
	Name() string
	// Actors returns the list of Actors that are alive in the actor system
	Actors() []PID
	// Start starts the actor system
	Start(ctx context.Context) error
	// Stop stops the actor system
	Stop(ctx context.Context) error
	// StartActor creates an actor in the system and starts it
	StartActor(ctx context.Context, name string, actor Actor) PID
	// StopActor stops a given actor in the system
	StopActor(ctx context.Context, name string) error
	// RestartActor restarts a given actor in the system
	RestartActor(ctx context.Context, name string) (PID, error)
	// NumActors returns the total number of active actors in the system
	NumActors() uint64
	// GetLocalActor returns the reference of a local actor.
	// A local actor is an actor that reside on the same node where the given actor system is running
	GetLocalActor(ctx context.Context, actorName string) (PID, error)
	// GetRemoteActor returns the address of a remote actor when cluster is enabled
	GetRemoteActor(ctx context.Context, actorName string) (addr *pb.Address, err error)
	// InCluster states whether the actor system is running within a cluster of nodes
	InCluster() bool
	// GetPartition returns the partition where a given actor is located
	GetPartition(ctx context.Context, actorName string) uint64
	// handleRemoteAsk handles a synchronous message to another actor and expect a response.
	// This block until a response is received or timed out.
	handleRemoteAsk(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error)
	// handleRemoteTell handles an asynchronous message to an actor
	handleRemoteTell(ctx context.Context, to PID, message proto.Message) error
}

// ActorSystem represent a collection of actors on a given node
// Only a single instance of the ActorSystem can be created on a given node
type actorSystem struct {
	goaktv1connect.UnimplementedRemoteMessagingServiceHandler

	// map of actors in the system
	actors cmp.ConcurrentMap[string, PID]

	// states whether the actor system has started or not
	hasStarted *atomic.Bool

	typesLoader TypesLoader
	reflection  Reflection

	// Specifies the actor system name
	name string
	// Specifies the logger to use in the system
	logger log.Logger
	// Specifies at what point in time to passivate the actor.
	// when the actor is passivated it is stopped which means it does not consume
	// any further resources like memory and cpu. The default value is 5s
	expireActorAfter time.Duration
	// Specifies how long the sender of a receiveContext should wait to receive a reply
	// when using SendReply. The default value is 5s
	replyTimeout time.Duration
	// Specifies the shutdown timeout. The default value is 30s
	shutdownTimeout time.Duration
	// Specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	actorInitMaxRetries int
	// Specifies the supervisor strategy
	supervisorStrategy StrategyDirective
	// Specifies the telemetry config
	telemetry *telemetry.Telemetry
	// Specifies whether remoting is enabled.
	// This allows to handle remote messaging
	remotingEnabled *atomic.Bool
	// Specifies the remoting port
	remotingPort int32
	// Specifies the remoting host
	remotingHost string
	// Specifies the remoting server
	remotingServer *http.Server

	// convenient field to check cluster setup
	clusterEnabled *atomic.Bool
	// cluster discovery method
	serviceDiscovery *discovery.ServiceDiscovery
	// define the number of partitions to shard the actors in the cluster
	partitionsCount uint64
	// cluster mode
	cluster     *cluster.Cluster
	clusterChan chan *goaktpb.WireActor

	// help protect some the fields to set
	mu sync.Mutex
}

// enforce compilation error when all methods of the ActorSystem interface are not implemented
// by the struct actorSystem
var _ ActorSystem = (*actorSystem)(nil)

// NewActorSystem creates an instance of ActorSystem
func NewActorSystem(name string, opts ...Option) (ActorSystem, error) {
	// check whether the name is set or not
	if name == "" {
		return nil, ErrNameRequired
	}
	// make sure the actor system name is valid
	if match, _ := regexp.MatchString("^[a-zA-Z0-9][a-zA-Z0-9-_]*$", name); !match {
		return nil, ErrInvalidActorSystemName
	}

	if system == nil {
		// the function only gets called one
		once.Do(func() {
			system = &actorSystem{
				actors:              cmp.New[PID](),
				hasStarted:          atomic.NewBool(false),
				typesLoader:         NewTypesLoader(),
				clusterChan:         make(chan *goaktpb.WireActor, 10),
				name:                name,
				logger:              log.DefaultLogger,
				expireActorAfter:    DefaultPassivationTimeout,
				replyTimeout:        DefaultReplyTimeout,
				actorInitMaxRetries: DefaultInitMaxRetries,
				supervisorStrategy:  DefaultSupervisoryStrategy,
				telemetry:           telemetry.New(),
				remotingEnabled:     atomic.NewBool(false),
				clusterEnabled:      atomic.NewBool(false),
				mu:                  sync.Mutex{},
				shutdownTimeout:     DefaultShutdownTimeout,
			}
			// set the reflection
			system.reflection = NewReflection(system.typesLoader)
			// apply the various options
			for _, opt := range opts {
				opt.Apply(system)
			}
		})
	}

	return system, nil
}

// GetPartition returns the partition where a given actor is located
func (asys *actorSystem) GetPartition(ctx context.Context, actorName string) uint64 {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "GetPartition")
	defer span.End()

	// return zero when the actor system is not in cluster mode
	if !asys.InCluster() {
		// TODO: maybe add a partitioner function
		return 0
	}

	// fetch the actor name partition from the cluster
	return uint64(asys.cluster.GetPartition(actorName))
}

// InCluster states whether the actor system is running within a cluster of nodes
func (asys *actorSystem) InCluster() bool {
	return asys.clusterEnabled.Load() && asys.cluster != nil
}

// NumActors returns the total number of active actors in the system
func (asys *actorSystem) NumActors() uint64 {
	return uint64(asys.actors.Count())
}

// StartActor creates or returns the instance of a given actor in the system
func (asys *actorSystem) StartActor(ctx context.Context, name string, actor Actor) PID {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "StartActor")
	defer span.End()
	// first check whether the actor system has started
	if !asys.hasStarted.Load() {
		return nil
	}
	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(protocol, asys.name, "", -1))
	// set the actor path when the remoting is enabled
	if asys.remotingEnabled.Load() {
		// get the path of the given actor
		actorPath = NewPath(name, NewAddress(protocol, asys.name, asys.remotingHost, int(asys.remotingPort)))
	}
	// check whether the given actor already exist in the system or not
	pid, exist := asys.actors.Get(actorPath.String())
	// actor already exist no need recreate it.
	if exist {
		// check whether the given actor heart beat
		if pid.IsRunning() {
			// return the existing instance
			return pid
		}
	}

	// create an instance of the actor ref
	pid = newPID(ctx,
		actorPath,
		actor,
		withInitMaxRetries(asys.actorInitMaxRetries),
		withPassivationAfter(asys.expireActorAfter),
		withSendReplyTimeout(asys.replyTimeout),
		withCustomLogger(asys.logger),
		withActorSystem(asys),
		withSupervisorStrategy(asys.supervisorStrategy),
		withTelemetry(asys.telemetry))

	// add the given actor to the actor map
	asys.actors.Set(actorPath.String(), pid)

	// let us register the actor
	asys.typesLoader.Register(name, actor)

	// when cluster is enabled replicate the actor metadata across the cluster
	if asys.clusterEnabled.Load() {
		// TODO: revisit the encoding the actor
		// encode the actor
		//actorType := reflect.TypeOf(actor)
		//var buf bytes.Buffer
		//enc := gob.NewEncoder(&buf)
		//if err := enc.Encode(actorType); err != nil {
		//	a.logger.Warn(errors.Wrapf(err, "failed to encode the underlying actor=%s", name).Error())
		//	// TODO: at the moment the byte array of the underlying actor is not used but can become handy in the future
		//}

		// create a wire actor
		wiredActor := &goaktpb.WireActor{
			ActorName:    name,
			ActorAddress: actorPath.RemoteAddress(),
			ActorPath:    actorPath.String(),
		}
		// send it to the cluster channel
		asys.clusterChan <- wiredActor
	}

	// return the actor ref
	return pid
}

// StopActor stops a given actor in the system
func (asys *actorSystem) StopActor(ctx context.Context, name string) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "StopActor")
	defer span.End()
	// first check whether the actor system has started
	if !asys.hasStarted.Load() {
		return errors.New("actor system has not started yet")
	}
	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(protocol, asys.name, "", -1))
	// set the actor path with the remoting is enabled
	if asys.remotingEnabled.Load() {
		// get the path of the given actor
		actorPath = NewPath(name, NewAddress(protocol, asys.name, asys.remotingHost, int(asys.remotingPort)))
	}
	// check whether the given actor already exist in the system or not
	pid, exist := asys.actors.Get(actorPath.String())
	// actor is found.
	if exist {
		// stop the given actor
		return pid.Shutdown(ctx)
	}
	return fmt.Errorf("actor=%s not found in the system", actorPath.String())
}

// RestartActor restarts a given actor in the system
func (asys *actorSystem) RestartActor(ctx context.Context, name string) (PID, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RestartActor")
	defer span.End()
	// first check whether the actor system has started
	if !asys.hasStarted.Load() {
		return nil, errors.New("actor system has not started yet")
	}
	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(protocol, asys.name, "", -1))
	// set the actor path with the remoting is enabled
	if asys.remotingEnabled.Load() {
		// get the path of the given actor
		actorPath = NewPath(name, NewAddress(protocol, asys.name, asys.remotingHost, int(asys.remotingPort)))
	}
	// check whether the given actor already exist in the system or not
	pid, exist := asys.actors.Get(actorPath.String())
	// actor is found.
	if exist {
		// restart the given actor
		if err := pid.Restart(ctx); err != nil {
			// return the error in case the restart failed
			return nil, errors.Wrapf(err, "failed to restart actor=%s", actorPath.String())
		}
		return pid, nil
	}
	return nil, fmt.Errorf("actor=%s not found in the system", actorPath.String())
}

// Name returns the actor system name
func (asys *actorSystem) Name() string {
	// acquire the lock
	asys.mu.Lock()
	// release the lock
	defer asys.mu.Unlock()
	return asys.name
}

// Actors returns the list of Actors that are alive in the actor system
func (asys *actorSystem) Actors() []PID {
	// acquire the lock
	asys.mu.Lock()
	// release the lock
	defer asys.mu.Unlock()

	// get the actors from the actor map
	items := asys.actors.Items()
	var refs []PID
	for _, actorRef := range items {
		refs = append(refs, actorRef)
	}

	return refs
}

// GetLocalActor returns the reference of a local actor.
// A local actor is an actor that reside on the same node where the given actor system is running
func (asys *actorSystem) GetLocalActor(ctx context.Context, actorName string) (PID, error) {
	// acquire the lock
	asys.mu.Lock()
	// release the lock
	defer asys.mu.Unlock()

	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "GetLocalActor")
	defer span.End()
	// first let us do a local lookup
	items := asys.actors.Items()
	// iterate the local actors storage
	for _, actorRef := range items {
		if actorRef.ActorPath().Name() == actorName {
			return actorRef, nil
		}
	}
	// add a logger
	asys.logger.Infof("actor=%s not found", actorName)
	return nil, ErrActorNotFound
}

// GetRemoteActor returns the address of a remote actor when cluster is enabled
func (asys *actorSystem) GetRemoteActor(ctx context.Context, actorName string) (addr *pb.Address, err error) {
	// acquire the lock
	asys.mu.Lock()
	// release the lock
	defer asys.mu.Unlock()

	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "GetRemoteActor")
	defer span.End()
	// check whether cluster is enabled or not
	if asys.cluster == nil {
		return nil, errors.New("cluster is not enabled")
	}

	// let us locate the actor in the cluster
	wireActor, err := asys.cluster.GetActor(ctx, actorName)
	// handle the eventual error
	if err != nil {
		if errors.Is(err, cluster.ErrActorNotFound) {
			asys.logger.Infof("actor=%s not found", actorName)
			return nil, ErrActorNotFound
		}
		return nil, errors.Wrapf(err, "failed to fetch remote actor=%s", actorName)
	}

	// return the address of the remote actor
	return wireActor.GetActorAddress(), nil
}

// Start starts the actor system
func (asys *actorSystem) Start(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Start")
	defer span.End()
	// set the has started to true
	asys.hasStarted.Store(true)

	// start remoting when remoting is enabled
	if asys.remotingEnabled.Load() {
		asys.enableRemoting(ctx)
	}

	// enable clustering when it is enabled
	if asys.clusterEnabled.Load() {
		asys.enableClustering(ctx)
	}

	// start the metrics service
	// register metrics. However, we don't panic when we fail to register
	// we just log it for now
	// TODO decide what to do when we fail to register the metrics or export the metrics registration as public
	if err := asys.registerMetrics(); err != nil {
		asys.logger.Error(errors.Wrapf(err, "failed to register actorSystem=%s metrics", asys.name))
	}
	asys.logger.Infof("%s started..:)", asys.name)
	return nil
}

// Stop stops the actor system
func (asys *actorSystem) Stop(ctx context.Context) error {
	// create a cancellation context to gracefully shutdown
	ctx, cancel := context.WithTimeout(ctx, asys.shutdownTimeout)
	defer cancel()

	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Stop")
	defer span.End()
	asys.logger.Infof("%s is shutting down..:)", asys.name)

	// stop the remoting server
	if asys.remotingEnabled.Load() {
		// stop the server
		if err := asys.remotingServer.Shutdown(ctx); err != nil {
			return err
		}
	}

	// stop the cluster service
	if asys.clusterEnabled.Load() {
		// stop the cluster service
		if err := asys.cluster.Stop(ctx); err != nil {
			return err
		}
		// stop broadcasting cluster messages
		close(asys.clusterChan)
	}

	// short-circuit the shutdown process when there are no online actors
	if len(asys.Actors()) == 0 {
		asys.logger.Info("No online actors to shutdown. Shutting down successfully done")
		return nil
	}

	// stop all the actors
	for _, actor := range asys.Actors() {
		// remove the actor the map and shut it down
		asys.actors.Remove(actor.ActorPath().String())
		// only shutdown live actors
		if err := actor.Shutdown(ctx); err != nil {
			// return the error
			return err
		}
	}

	// reset the actor system
	asys.reset()

	return nil
}

// RemoteLookup for an actor on a remote host.
func (asys *actorSystem) RemoteLookup(ctx context.Context, request *connect.Request[goaktpb.RemoteLookupRequest]) (*connect.Response[goaktpb.RemoteLookupResponse], error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteLookup")
	defer span.End()

	// get a context log
	logger := asys.logger

	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	// set the actor path with the remoting is enabled
	if !asys.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingNotEnabled)
	}

	// get the remoting server address
	nodeAddr := fmt.Sprintf("%s:%d", asys.remotingHost, asys.remotingPort)

	// let us validate the host and port
	hostAndPort := fmt.Sprintf("%s:%d", reqCopy.GetHost(), reqCopy.GetPort())
	if hostAndPort != nodeAddr {
		// log the error
		logger.Error(ErrRemoteSendInvalidNode.Message())
		// here message is sent to the wrong actor system node
		return nil, ErrRemoteSendInvalidNode
	}

	// construct the actor address
	name := reqCopy.GetName()
	actorPath := NewPath(name, NewAddress(protocol, asys.Name(), reqCopy.GetHost(), int(reqCopy.GetPort())))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := asys.actors.Get(actorPath.String())
	// return an error when the remote address is not found
	if !exist {
		// log the error
		logger.Error(ErrRemoteActorNotFound(actorPath.String()).Error())
		return nil, ErrRemoteActorNotFound(actorPath.String())
	}

	// let us construct the address
	addr := pid.ActorPath().RemoteAddress()

	return connect.NewResponse(&goaktpb.RemoteLookupResponse{Address: addr}), nil
}

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately. With this type of message the receiver cannot communicate back to Sender
// except reply the message with a response. This one-way communication
func (asys *actorSystem) RemoteAsk(ctx context.Context, request *connect.Request[goaktpb.RemoteAskRequest]) (*connect.Response[goaktpb.RemoteAskResponse], error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteAsk")
	defer span.End()

	// get a context log
	logger := asys.logger
	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	// set the actor path with the remoting is enabled
	if !asys.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingNotEnabled)
	}

	// get the remoting server address
	nodeAddr := fmt.Sprintf("%s:%d", asys.remotingHost, asys.remotingPort)

	// let us validate the host and port
	hostAndPort := fmt.Sprintf("%s:%d", reqCopy.GetRemoteMessage().GetReceiver().GetHost(), reqCopy.GetRemoteMessage().GetReceiver().GetPort())
	if hostAndPort != nodeAddr {
		// log the error
		logger.Error(ErrRemoteSendInvalidNode.Message())
		// here message is sent to the wrong actor system node
		return nil, ErrRemoteSendInvalidNode
	}

	// construct the actor address
	name := reqCopy.GetRemoteMessage().GetReceiver().GetName()
	actorPath := NewPath(name, NewAddress(protocol, asys.name, asys.remotingHost, int(asys.remotingPort)))

	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := asys.actors.Get(actorPath.String())
	// return an error when the remote address is not found
	if !exist {
		// log the error
		logger.Error(ErrRemoteActorNotFound(actorPath.String()).Error())
		return nil, ErrRemoteActorNotFound(actorPath.String())
	}
	// restart the actor when it is not live
	if !pid.IsRunning() {
		if err := pid.Restart(ctx); err != nil {
			return nil, err
		}
	}

	// send the message to actor
	reply, err := asys.handleRemoteAsk(ctx, pid, reqCopy.GetRemoteMessage())
	// handle the error
	if err != nil {
		logger.Error(ErrRemoteSendFailure(err).Error())
		return nil, ErrRemoteSendFailure(err)
	}
	// let us marshal the reply
	marshaled, _ := anypb.New(reply)
	return connect.NewResponse(&goaktpb.RemoteAskResponse{Message: marshaled}), nil
}

// RemoteTell is used to send a message to an actor remotely by another actor
// This is the only way remote actors can interact with each other. The actor on the
// other line can reply to the sender by using the Sender in the message
func (asys *actorSystem) RemoteTell(ctx context.Context, request *connect.Request[goaktpb.RemoteTellRequest]) (*connect.Response[goaktpb.RemoteTellResponse], error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteTell")
	defer span.End()

	// get a context log
	logger := asys.logger
	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	receiver := reqCopy.GetRemoteMessage().GetReceiver()

	// add some debug logger
	logger.Debugf("received a remote tell call for=(%s)", receiver.String())

	// set the actor path with the remoting is enabled
	if !asys.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingNotEnabled)
	}

	// get the remoting server address
	nodeAddr := fmt.Sprintf("%s:%d", asys.remotingHost, asys.remotingPort)

	// let us validate the host and port
	hostAndPort := fmt.Sprintf("%s:%d", receiver.GetHost(), receiver.GetPort())
	if hostAndPort != nodeAddr {
		// log the error
		logger.Error(ErrRemoteSendInvalidNode.Message())
		// here message is sent to the wrong actor system node
		return nil, ErrRemoteSendInvalidNode
	}

	// construct the actor address
	actorPath := NewPath(
		receiver.GetName(),
		NewAddress(
			protocol,
			asys.Name(),
			receiver.GetHost(),
			int(receiver.GetPort())))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := asys.actors.Get(actorPath.String())
	// return an error when the remote address is not found
	if !exist {
		// log the error
		logger.Error(ErrRemoteActorNotFound(actorPath.String()).Error())
		return nil, ErrRemoteActorNotFound(actorPath.String())
	}
	// restart the actor when it is not live
	if !pid.IsRunning() {
		if err := pid.Restart(ctx); err != nil {
			return nil, err
		}
	}

	// send the message to actor
	if err := asys.handleRemoteTell(ctx, pid, reqCopy.GetRemoteMessage()); err != nil {
		logger.Error(ErrRemoteSendFailure(err))
		return nil, ErrRemoteSendFailure(err)
	}
	return connect.NewResponse(new(goaktpb.RemoteTellResponse)), nil
}

// registerMetrics register the PID metrics with OTel instrumentation.
func (asys *actorSystem) registerMetrics() error {
	// grab the OTel meter
	meter := asys.telemetry.Meter
	// create an instance of the ActorMetrics
	metrics, err := telemetry.NewSystemMetrics(meter)
	// handle the error
	if err != nil {
		return err
	}

	// define the common labels
	labels := []attribute.KeyValue{
		attribute.String("actor.system", asys.Name()),
	}

	// register the metrics
	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		observer.ObserveInt64(metrics.ActorSystemActorsCount, int64(asys.NumActors()), metric.WithAttributes(labels...))
		return nil
	}, metrics.ActorSystemActorsCount)

	return err
}

// handleRemoteAsk handles a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (asys *actorSystem) handleRemoteAsk(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "handleRemoteAsk")
	defer span.End()
	return Ask(ctx, to, message, asys.replyTimeout)
}

// handleRemoteTell handles an asynchronous message to an actor
func (asys *actorSystem) handleRemoteTell(ctx context.Context, to PID, message proto.Message) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "handleRemoteTell")
	defer span.End()
	return Tell(ctx, to, message)
}

// enableClustering enables clustering. When clustering is enabled remoting is also enabled to facilitate remote
// communication
func (asys *actorSystem) enableClustering(ctx context.Context) {
	// add some logging information
	asys.logger.Info("enabling clustering...")

	// create an instance of the cluster service and start it
	cluster, err := cluster.New(asys.Name(),
		asys.serviceDiscovery,
		cluster.WithLogger(asys.logger),
		cluster.WithPartitionsCount(asys.partitionsCount),
	)
	// handle the error
	if err != nil {
		asys.logger.Panic(errors.Wrap(err, "failed to initialize cluster engine"))
	}

	// add some logging information
	asys.logger.Info("starting cluster engine...")
	// start the cluster service
	if err := cluster.Start(ctx); err != nil {
		asys.logger.Panic(errors.Wrap(err, "failed to start cluster engine"))
	}

	// create the bootstrap channel
	bootstrapChan := make(chan struct{}, 1)
	// let us wait for some time for the cluster to be properly started
	timer := time.AfterFunc(time.Second, func() {
		bootstrapChan <- struct{}{}
	})

	<-bootstrapChan
	timer.Stop()

	asys.logger.Info("cluster engine successfully started...")

	// acquire the lock
	asys.mu.Lock()
	// set the cluster field
	asys.cluster = cluster
	// release the lock
	// set the remoting host and port
	asys.remotingHost = cluster.NodeHost()
	asys.remotingPort = int32(cluster.NodeRemotingPort())
	// release the lock
	asys.mu.Unlock()

	// let us enable remoting as well if not yet enabled
	if !asys.remotingEnabled.Load() {
		asys.enableRemoting(ctx)
	}
	// start broadcasting cluster message
	go asys.broadcast(ctx)
	// add some logging
	asys.logger.Info("clustering enabled...:)")
}

// enableRemoting enables the remoting service to handle remote messaging
func (asys *actorSystem) enableRemoting(ctx context.Context) {
	// add some logging information
	asys.logger.Info("enabling remoting...")
	// create a function to handle the observability
	interceptor := func(tp trace.TracerProvider, mp metric.MeterProvider) connect.Interceptor {
		return otelconnect.NewInterceptor(
			otelconnect.WithTracerProvider(tp),
			otelconnect.WithMeterProvider(mp),
		)
	}

	// create a http service mux
	mux := http.NewServeMux()
	// create the resource and handler
	path, handler := goaktv1connect.NewRemoteMessagingServiceHandler(
		asys,
		connect.WithInterceptors(interceptor(asys.telemetry.TracerProvider, asys.telemetry.MeterProvider)),
	)
	mux.Handle(path, handler)
	// create the address
	serverAddr := fmt.Sprintf("%s:%d", asys.remotingHost, asys.remotingPort)

	// create a http service instance
	// TODO revisit the timeouts
	// reference: https://adam-p.ca/blog/2022/01/golang-http-server-timeouts/
	server := &http.Server{
		Addr: serverAddr,
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
		// For gRPC clients, it's convenient to support HTTP/2 without TLS. You can
		// avoid x/net/http2 by using http.ListenAndServeTLS.
		Handler: h2c.NewHandler(mux, &http2.Server{
			IdleTimeout: 1200 * time.Second,
		}),
		// Set the base context to incoming context of the system
		BaseContext: func(listener net.Listener) context.Context {
			return ctx
		},
	}

	// listen and service requests
	go func() {
		if err := server.ListenAndServe(); err != nil {
			// check the error type
			if !errors.Is(err, http.ErrServerClosed) {
				asys.logger.Panic(errors.Wrap(err, "failed to start remoting service"))
			}
		}
	}()

	// acquire the lock
	asys.mu.Lock()
	// set the server
	asys.remotingServer = server
	// release the lock
	asys.mu.Unlock()

	// add some logging information
	asys.logger.Info("remoting enabled...:)")
}

// reset the actor system
func (asys *actorSystem) reset() {
	asys.hasStarted = atomic.NewBool(false)
	asys.remotingEnabled = atomic.NewBool(false)
	asys.clusterEnabled = atomic.NewBool(false)
	asys.telemetry = nil
	asys.actors = cmp.New[PID]()
	asys.name = ""
	asys.logger = nil
	asys.remotingServer = nil
	// set the global nil
	system = nil
	once.Reset()
}

// broadcast publishes newly created actor into the cluster when cluster is enabled
func (asys *actorSystem) broadcast(ctx context.Context) {
	// iterate over the channel
	for wireActor := range asys.clusterChan {
		// making sure the cluster is still enabled
		if asys.cluster != nil {
			// broadcast the message on the cluster
			if err := asys.cluster.PutActor(ctx, wireActor); err != nil {
				asys.logger.Error(err.Error())
				// TODO: stop or continue
				return
			}
		}
	}
}
