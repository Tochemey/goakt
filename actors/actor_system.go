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
	"connectrpc.com/otelconnect"
	cmp "github.com/orcaman/concurrent-map/v2"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/cluster"
	"github.com/tochemey/goakt/discovery"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/tochemey/goakt/internal/goakt/v1/goaktv1connect"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/pb/v1"
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
	// Spawn creates an actor in the system and starts it
	Spawn(ctx context.Context, name string, actor Actor) PID
	// Kill stops a given actor in the system
	Kill(ctx context.Context, name string) error
	// ReSpawn recreates a given actor in the system
	ReSpawn(ctx context.Context, name string) (PID, error)
	// NumActors returns the total number of active actors in the system
	NumActors() uint64
	// LocalActor returns the reference of a local actor.
	// A local actor is an actor that reside on the same node where the given actor system is running
	LocalActor(ctx context.Context, actorName string) (PID, error)
	// RemoteActor returns the address of a remote actor when cluster is enabled
	// When the cluster mode is not enabled an actor not found error will be returned
	// One can always check whether cluster is enabled before calling this method or just use the ActorOf method.
	RemoteActor(ctx context.Context, actorName string) (addr *pb.Address, err error)
	// ActorOf returns an existing actor in the local system or in the cluster when clustering is enabled
	// When cluster mode is activated, the PID will be nil.
	// When remoting is enabled this method will return and error
	// An actor not found error is return when the actor is not found.
	ActorOf(ctx context.Context, actorName string) (addr *pb.Address, pid PID, err error)
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
func (x *actorSystem) GetPartition(ctx context.Context, actorName string) uint64 {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "GetPartition")
	defer span.End()

	// return zero when the actor system is not in cluster mode
	if !x.InCluster() {
		// TODO: maybe add a partitioner function
		return 0
	}

	// fetch the actor name partition from the cluster
	return uint64(x.cluster.GetPartition(actorName))
}

// InCluster states whether the actor system is running within a cluster of nodes
func (x *actorSystem) InCluster() bool {
	return x.clusterEnabled.Load() && x.cluster != nil
}

// NumActors returns the total number of active actors in the system
func (x *actorSystem) NumActors() uint64 {
	return uint64(x.actors.Count())
}

// Spawn creates or returns the instance of a given actor in the system
func (x *actorSystem) Spawn(ctx context.Context, name string, actor Actor) PID {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Spawn")
	defer span.End()
	// first check whether the actor system has started
	if !x.hasStarted.Load() {
		return nil
	}
	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(protocol, x.name, "", -1))
	// set the actor path when the remoting is enabled
	if x.remotingEnabled.Load() {
		// get the path of the given actor
		actorPath = NewPath(name, NewAddress(protocol, x.name, x.remotingHost, int(x.remotingPort)))
	}
	// check whether the given actor already exist in the system or not
	pid, exist := x.actors.Get(actorPath.String())
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
		withInitMaxRetries(x.actorInitMaxRetries),
		withPassivationAfter(x.expireActorAfter),
		withSendReplyTimeout(x.replyTimeout),
		withCustomLogger(x.logger),
		withActorSystem(x),
		withSupervisorStrategy(x.supervisorStrategy),
		withTelemetry(x.telemetry))

	// add the given actor to the actor map
	x.actors.Set(actorPath.String(), pid)

	// let us register the actor
	x.typesLoader.Register(name, actor)

	// when cluster is enabled replicate the actor metadata across the cluster
	if x.clusterEnabled.Load() {
		// send it to the cluster channel a wire actor
		x.clusterChan <- &goaktpb.WireActor{
			ActorName:    name,
			ActorAddress: actorPath.RemoteAddress(),
			ActorPath:    actorPath.String(),
		}
	}

	// return the actor ref
	return pid
}

// Kill stops a given actor in the system
func (x *actorSystem) Kill(ctx context.Context, name string) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Kill")
	defer span.End()
	// first check whether the actor system has started
	if !x.hasStarted.Load() {
		return errors.New("actor system has not started yet")
	}
	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(protocol, x.name, "", -1))
	// set the actor path with the remoting is enabled
	if x.remotingEnabled.Load() {
		// get the path of the given actor
		actorPath = NewPath(name, NewAddress(protocol, x.name, x.remotingHost, int(x.remotingPort)))
	}
	// check whether the given actor already exist in the system or not
	pid, exist := x.actors.Get(actorPath.String())
	// actor is found.
	if exist {
		// stop the given actor
		return pid.Shutdown(ctx)
	}
	return fmt.Errorf("actor=%s not found in the system", actorPath.String())
}

// ReSpawn recreates a given actor in the system
func (x *actorSystem) ReSpawn(ctx context.Context, name string) (PID, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "ReSpawn")
	defer span.End()
	// first check whether the actor system has started
	if !x.hasStarted.Load() {
		return nil, errors.New("actor system has not started yet")
	}
	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(protocol, x.name, "", -1))
	// set the actor path with the remoting is enabled
	if x.remotingEnabled.Load() {
		// get the path of the given actor
		actorPath = NewPath(name, NewAddress(protocol, x.name, x.remotingHost, int(x.remotingPort)))
	}
	// check whether the given actor already exist in the system or not
	pid, exist := x.actors.Get(actorPath.String())
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
func (x *actorSystem) Name() string {
	// acquire the lock
	x.mu.Lock()
	// release the lock
	defer x.mu.Unlock()
	return x.name
}

// Actors returns the list of Actors that are alive in the actor system
func (x *actorSystem) Actors() []PID {
	// acquire the lock
	x.mu.Lock()
	// release the lock
	defer x.mu.Unlock()

	// get the actors from the actor map
	items := x.actors.Items()
	var refs []PID
	for _, actorRef := range items {
		refs = append(refs, actorRef)
	}

	return refs
}

// ActorOf returns an existing actor in the local system or in the cluster when clustering is enabled
// When cluster mode is activated, the PID will be nil.
// When remoting is enabled this method will return and error
// An actor not found error is return when the actor is not found.
func (x *actorSystem) ActorOf(ctx context.Context, actorName string) (addr *pb.Address, pid PID, err error) {
	// acquire the lock
	x.mu.Lock()
	// release the lock
	defer x.mu.Unlock()

	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "ActorOf")
	defer span.End()

	// try to locate the actor in the cluster when cluster is enabled
	if x.cluster != nil || x.clusterEnabled.Load() {
		// let us locate the actor in the cluster
		wireActor, err := x.cluster.GetActor(ctx, actorName)
		// handle the eventual error
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				x.logger.Infof("actor=%s not found", actorName)
				return nil, nil, ErrActorNotFound
			}
			return nil, nil, errors.Wrapf(err, "failed to fetch remote actor=%s", actorName)
		}

		// return the address of the remote actor
		return wireActor.GetActorAddress(), nil, nil
	}

	// method call is not allowed
	if x.remotingEnabled.Load() {
		return nil, nil, ErrMethodCallNotAllowed
	}

	// try to locate the actor when cluster is not enabled
	// first let us do a local lookup
	items := x.actors.Items()
	// iterate the local actors storage
	for _, actorRef := range items {
		if actorRef.ActorPath().Name() == actorName {
			return actorRef.ActorPath().RemoteAddress(), actorRef, nil
		}
	}
	// add a logger
	x.logger.Infof("actor=%s not found", actorName)
	return nil, nil, ErrActorNotFound
}

// LocalActor returns the reference of a local actor.
// A local actor is an actor that reside on the same node where the given actor system is running
func (x *actorSystem) LocalActor(ctx context.Context, actorName string) (PID, error) {
	// acquire the lock
	x.mu.Lock()
	// release the lock
	defer x.mu.Unlock()

	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "LocalActor")
	defer span.End()
	// first let us do a local lookup
	items := x.actors.Items()
	// iterate the local actors storage
	for _, actorRef := range items {
		if actorRef.ActorPath().Name() == actorName {
			return actorRef, nil
		}
	}
	// add a logger
	x.logger.Infof("actor=%s not found", actorName)
	return nil, ErrActorNotFound
}

// RemoteActor returns the address of a remote actor when cluster is enabled
// When the cluster mode is not enabled an actor not found error will be returned
// One can always check whether cluster is enabled before calling this method or just use the ActorOf method.
func (x *actorSystem) RemoteActor(ctx context.Context, actorName string) (addr *pb.Address, err error) {
	// acquire the lock
	x.mu.Lock()
	// release the lock
	defer x.mu.Unlock()

	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteActor")
	defer span.End()
	// check whether cluster is enabled or not
	if x.cluster == nil {
		return nil, errors.New("cluster is not enabled")
	}

	// let us locate the actor in the cluster
	wireActor, err := x.cluster.GetActor(ctx, actorName)
	// handle the eventual error
	if err != nil {
		if errors.Is(err, cluster.ErrActorNotFound) {
			x.logger.Infof("actor=%s not found", actorName)
			return nil, ErrActorNotFound
		}
		return nil, errors.Wrapf(err, "failed to fetch remote actor=%s", actorName)
	}

	// return the address of the remote actor
	return wireActor.GetActorAddress(), nil
}

// Start starts the actor system
func (x *actorSystem) Start(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Start")
	defer span.End()
	// set the has started to true
	x.hasStarted.Store(true)

	// enable clustering when it is enabled
	if x.clusterEnabled.Load() {
		x.enableClustering(ctx)
	}

	// start remoting when remoting is enabled
	if x.remotingEnabled.Load() {
		x.enableRemoting(ctx)
	}

	// start the metrics service
	// register metrics. However, we don't panic when we fail to register
	// we just log it for now
	// TODO decide what to do when we fail to register the metrics or export the metrics registration as public
	if err := x.registerMetrics(); err != nil {
		x.logger.Error(errors.Wrapf(err, "failed to register actorSystem=%s metrics", x.name))
	}
	x.logger.Infof("%s started..:)", x.name)
	return nil
}

// Stop stops the actor system
func (x *actorSystem) Stop(ctx context.Context) error {
	// create a cancellation context to gracefully shutdown
	ctx, cancel := context.WithTimeout(ctx, x.shutdownTimeout)
	defer cancel()

	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Stop")
	defer span.End()
	x.logger.Infof("%s is shutting down..:)", x.name)

	// set started to false
	x.hasStarted.Store(false)

	// stop the remoting server
	if x.remotingEnabled.Load() {
		// stop the server
		if err := x.remotingServer.Shutdown(ctx); err != nil {
			return err
		}

		// unset the remoting settings
		x.remotingEnabled.Store(false)
		x.remotingServer = nil
	}

	// stop the cluster service
	if x.clusterEnabled.Load() {
		// stop the cluster service
		if err := x.cluster.Stop(ctx); err != nil {
			return err
		}
		// stop broadcasting cluster messages
		close(x.clusterChan)

		// unset the remoting settings
		x.clusterEnabled.Store(false)
		x.cluster = nil
	}

	// short-circuit the shutdown process when there are no online actors
	if len(x.Actors()) == 0 {
		x.logger.Info("No online actors to shutdown. Shutting down successfully done")
		return nil
	}

	// stop all the actors
	for _, actor := range x.Actors() {
		// remove the actor the map and shut it down
		x.actors.Remove(actor.ActorPath().String())
		// only shutdown live actors
		if err := actor.Shutdown(ctx); err != nil {
			// return the error
			return err
		}
	}

	// reset the actor system
	x.reset()

	return nil
}

// RemoteLookup for an actor on a remote host.
func (x *actorSystem) RemoteLookup(ctx context.Context, request *connect.Request[goaktpb.RemoteLookupRequest]) (*connect.Response[goaktpb.RemoteLookupResponse], error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteLookup")
	defer span.End()

	// get a context log
	logger := x.logger

	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	// set the actor path with the remoting is enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingNotEnabled)
	}

	// get the remoting server address
	nodeAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)

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
	actorPath := NewPath(name, NewAddress(protocol, x.Name(), reqCopy.GetHost(), int(reqCopy.GetPort())))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := x.actors.Get(actorPath.String())
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
func (x *actorSystem) RemoteAsk(ctx context.Context, request *connect.Request[goaktpb.RemoteAskRequest]) (*connect.Response[goaktpb.RemoteAskResponse], error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteAsk")
	defer span.End()

	// get a context log
	logger := x.logger
	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	// set the actor path with the remoting is enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingNotEnabled)
	}

	// get the remoting server address
	nodeAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)

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
	actorPath := NewPath(name, NewAddress(protocol, x.name, x.remotingHost, int(x.remotingPort)))

	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := x.actors.Get(actorPath.String())
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
	reply, err := x.handleRemoteAsk(ctx, pid, reqCopy.GetRemoteMessage())
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
func (x *actorSystem) RemoteTell(ctx context.Context, request *connect.Request[goaktpb.RemoteTellRequest]) (*connect.Response[goaktpb.RemoteTellResponse], error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteTell")
	defer span.End()

	// get a context log
	logger := x.logger
	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	receiver := reqCopy.GetRemoteMessage().GetReceiver()

	// set the actor path with the remoting is enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingNotEnabled)
	}

	// get the remoting server address
	nodeAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)

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
			x.Name(),
			receiver.GetHost(),
			int(receiver.GetPort())))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := x.actors.Get(actorPath.String())
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
	if err := x.handleRemoteTell(ctx, pid, reqCopy.GetRemoteMessage()); err != nil {
		logger.Error(ErrRemoteSendFailure(err))
		return nil, ErrRemoteSendFailure(err)
	}
	return connect.NewResponse(new(goaktpb.RemoteTellResponse)), nil
}

// registerMetrics register the PID metrics with OTel instrumentation.
func (x *actorSystem) registerMetrics() error {
	// grab the OTel meter
	meter := x.telemetry.Meter
	// create an instance of the ActorMetrics
	metrics, err := telemetry.NewSystemMetrics(meter)
	// handle the error
	if err != nil {
		return err
	}

	// define the common labels
	labels := []attribute.KeyValue{
		attribute.String("actor.system", x.Name()),
	}

	// register the metrics
	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		observer.ObserveInt64(metrics.ActorSystemActorsCount, int64(x.NumActors()), metric.WithAttributes(labels...))
		return nil
	}, metrics.ActorSystemActorsCount)

	return err
}

// handleRemoteAsk handles a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (x *actorSystem) handleRemoteAsk(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "handleRemoteAsk")
	defer span.End()
	return Ask(ctx, to, message, x.replyTimeout)
}

// handleRemoteTell handles an asynchronous message to an actor
func (x *actorSystem) handleRemoteTell(ctx context.Context, to PID, message proto.Message) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "handleRemoteTell")
	defer span.End()
	return Tell(ctx, to, message)
}

// enableClustering enables clustering. When clustering is enabled remoting is also enabled to facilitate remote
// communication
func (x *actorSystem) enableClustering(ctx context.Context) {
	// add some logging information
	x.logger.Info("enabling clustering...")

	// create an instance of the cluster service and start it
	cluster, err := cluster.New(x.Name(),
		x.serviceDiscovery,
		cluster.WithLogger(x.logger),
		cluster.WithPartitionsCount(x.partitionsCount),
	)
	// handle the error
	if err != nil {
		x.logger.Panic(errors.Wrap(err, "failed to initialize cluster engine"))
	}

	// add some logging information
	x.logger.Info("starting cluster engine...")
	// start the cluster service
	if err := cluster.Start(ctx); err != nil {
		x.logger.Panic(errors.Wrap(err, "failed to start cluster engine"))
	}

	// create the bootstrap channel
	bootstrapChan := make(chan struct{}, 1)
	// let us wait for some time for the cluster to be properly started
	timer := time.AfterFunc(time.Second, func() {
		bootstrapChan <- struct{}{}
	})
	<-bootstrapChan
	timer.Stop()

	// add some logging information
	x.logger.Info("cluster engine successfully started...")
	// acquire the lock
	x.mu.Lock()
	// set the cluster field
	x.cluster = cluster
	// set the remoting host and port
	x.remotingHost = cluster.NodeHost()
	x.remotingPort = int32(cluster.NodeRemotingPort())
	// release the lock
	x.mu.Unlock()
	// start broadcasting cluster message
	go x.broadcast(ctx)
	// add some logging
	x.logger.Info("clustering enabled...:)")
}

// enableRemoting enables the remoting service to handle remote messaging
func (x *actorSystem) enableRemoting(ctx context.Context) {
	// add some logging information
	x.logger.Info("enabling remoting...")
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
		x,
		connect.WithInterceptors(interceptor(x.telemetry.TracerProvider, x.telemetry.MeterProvider)),
	)
	mux.Handle(path, handler)
	// create the address
	serverAddr := fmt.Sprintf("%s:%d", x.remotingHost, x.remotingPort)

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
				x.logger.Panic(errors.Wrap(err, "failed to start remoting service"))
			}
		}
	}()

	// acquire the lock
	x.mu.Lock()
	// set the server
	x.remotingServer = server
	// release the lock
	x.mu.Unlock()

	// add some logging information
	x.logger.Info("remoting enabled...:)")
}

// reset the actor system
func (x *actorSystem) reset() {
	// set the global nil
	system = nil
	x.telemetry = nil
	x.mu = sync.Mutex{}
	x.actors = cmp.New[PID]()
	x.name = ""
	x.logger = nil
	once.Reset()
}

// broadcast publishes newly created actor into the cluster when cluster is enabled
func (x *actorSystem) broadcast(ctx context.Context) {
	// iterate over the channel
	for wireActor := range x.clusterChan {
		// making sure the cluster is still enabled
		if x.cluster != nil {
			// broadcast the message on the cluster
			if err := x.cluster.PutActor(ctx, wireActor); err != nil {
				x.logger.Error(err.Error())
				// TODO: stop or continue
				return
			}
		}
	}
}
