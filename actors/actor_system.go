package actors

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"reflect"
	"time"

	cmp "github.com/orcaman/concurrent-map/v2"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/internal/cluster"
	goaktpb "github.com/tochemey/goakt/internal/goaktpb/v1"
	"github.com/tochemey/goakt/internal/grpc"
	"github.com/tochemey/goakt/internal/resync"
	"github.com/tochemey/goakt/internal/telemetry"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/messages/v1"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

var system *actorSystem
var once resync.Once

// ActorSystem defines the contract of an actor system
type ActorSystem interface {
	// Name returns the actor system name
	Name() string
	// NodeAddr returns the node where the actor system is running
	NodeAddr() string
	// Actors returns the list of Actors that are alive in the actor system
	Actors() []PID
	// Host returns the actor system host address
	Host() string
	// Port returns the actor system host port
	Port() int
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
	// GetRemoteActor returns the address of a remote actor when cluster is enabled
	GetRemoteActor(ctx context.Context, actorName string) (addr *pb.Address, err error)
	// GetLocalActor returns the reference of a local actor.
	// A local actor is an actor that reside on the same node where the given actor system is running
	GetLocalActor(ctx context.Context, actorName string) (PID, error)

	// handleRemoteAsk handles a synchronous message to another actor and expect a response.
	// This block until a response is received or timed out.
	handleRemoteAsk(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error)
	// handleRemoteTell handles an asynchronous message to an actor
	handleRemoteTell(ctx context.Context, to PID, message proto.Message) error
}

// ActorSystem represent a collection of actors on a given node
// Only a single instance of the ActorSystem can be created on a given node
type actorSystem struct {
	// Specifies the actor system name
	name string
	// Specifies the node where the actor system is located
	nodeAddr string
	// map of actors in the system
	actors cmp.ConcurrentMap[string, PID]
	//  specifies the logger to use
	logger log.Logger
	// specifies the host address
	host string
	// specifies the port
	port int
	// actor system configuration
	config *Config
	// states whether the actor system has started or not
	hasStarted *atomic.Bool

	// observability settings
	telemetry *telemetry.Telemetry
	// specifies the remoting service
	remotingService grpc.Server
	// specifies the cluster service
	cluster cluster.Cluster

	// close this channel to stop broadcasting wire actor info
	broadcastChan chan *goaktpb.WireActor

	typesLoader TypesLoader
	reflection  Reflection
}

// enforce compilation error when all methods of the ActorSystem interface are not implemented
// by the struct actorSystem
var _ ActorSystem = (*actorSystem)(nil)

// NewActorSystem creates an instance of ActorSystem
func NewActorSystem(config *Config) (ActorSystem, error) {
	// make sure the configuration is set
	if config == nil {
		return nil, ErrMissingConfig
	}

	// the function only gets called one
	once.Do(func() {
		system = &actorSystem{
			name:            config.Name(),
			nodeAddr:        config.NodeHostAndPort(),
			actors:          cmp.New[PID](),
			logger:          config.Logger(),
			host:            "",
			port:            0,
			config:          config,
			hasStarted:      atomic.NewBool(false),
			telemetry:       config.telemetry,
			remotingService: nil,
			cluster:         nil,
			broadcastChan:   make(chan *goaktpb.WireActor, 10),
			typesLoader:     NewTypesLoader(nil),
		}
		// set host and port
		host, port := config.HostAndPort()
		system.host = host
		system.port = port
		// set the reflection
		system.reflection = NewReflection(system.typesLoader)
	})

	return system, nil
}

// NumActors returns the total number of active actors in the system
func (a *actorSystem) NumActors() uint64 {
	return uint64(a.actors.Count())
}

// StartActor creates or returns the instance of a given actor in the system
func (a *actorSystem) StartActor(ctx context.Context, name string, actor Actor) PID {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "StartActor")
	defer span.End()
	// first check whether the actor system has started
	if !a.hasStarted.Load() {
		return nil
	}
	// get the path of the given actor
	actorPath := NewPath(name, NewLocalAddress(protocol, a.name, a.host, a.port))
	// check whether the given actor already exist in the system or not
	pid, exist := a.actors.Get(actorPath.String())
	// actor already exist no need recreate it.
	if exist {
		// check whether the given actor heart beat
		if pid.IsOnline() {
			// return the existing instance
			return pid
		}
	}

	// create an instance of the actor ref
	pid = newPID(ctx,
		actorPath,
		actor,
		withInitMaxRetries(a.config.ActorInitMaxRetries()),
		withPassivationAfter(a.config.ExpireActorAfter()),
		withSendReplyTimeout(a.config.ReplyTimeout()),
		withCustomLogger(a.config.logger),
		withActorSystem(a),
		withSupervisorStrategy(a.config.supervisorStrategy),
		withTelemetry(a.config.telemetry))

	// add the given actor to the actor map
	a.actors.Set(actorPath.String(), pid)

	// let us register the actor
	a.typesLoader.Register(name, actor)
	// inform the cluster of the existence of the given actor
	if a.config.clusterEnabled {
		// encode the actor
		actorType := reflect.TypeOf(actor)
		var buf bytes.Buffer
		enc := gob.NewEncoder(&buf)
		if err := enc.Encode(actorType); err != nil {
			// TODO fatal log
		}

		// create a wire actor
		wiredActor := &goaktpb.WireActor{
			ActorName:    name,
			ActorAddress: actorPath.RemoteAddress(),
			ActorPayload: buf.Bytes(),
		}
		// send it to the broadcaster
		a.broadcastChan <- wiredActor
	}

	// return the actor ref
	return pid
}

// StopActor stops a given actor in the system
func (a *actorSystem) StopActor(ctx context.Context, name string) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "StopActor")
	defer span.End()
	// first check whether the actor system has started
	if !a.hasStarted.Load() {
		return errors.New("actor system has not started yet")
	}
	// get the path of the given actor
	actorPath := NewPath(name, NewLocalAddress(protocol, a.name, a.host, a.port))
	// check whether the given actor already exist in the system or not
	pid, exist := a.actors.Get(actorPath.String())
	// actor is found.
	if exist {
		// stop the given actor
		return pid.Shutdown(ctx)
	}
	return fmt.Errorf("actor=%s not found in the system", actorPath.String())
}

// RestartActor restarts a given actor in the system
func (a *actorSystem) RestartActor(ctx context.Context, name string) (PID, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RestartActor")
	defer span.End()
	// first check whether the actor system has started
	if !a.hasStarted.Load() {
		return nil, errors.New("actor system has not started yet")
	}
	// get the path of the given actor
	actorPath := NewPath(name, NewLocalAddress(protocol, a.name, a.host, a.port))
	// check whether the given actor already exist in the system or not
	pid, exist := a.actors.Get(actorPath.String())
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
func (a *actorSystem) Name() string {
	return a.name
}

// NodeAddr returns the node where the actor system is running
func (a *actorSystem) NodeAddr() string {
	return a.nodeAddr
}

// Host returns the actor system node host address
func (a *actorSystem) Host() string {
	return a.host
}

// Port returns the actor system node port
func (a *actorSystem) Port() int {
	return a.port
}

// Actors returns the list of Actors that are alive in the actor system
func (a *actorSystem) Actors() []PID {
	// get the actors from the actor map
	items := a.actors.Items()
	var refs []PID
	for _, actorRef := range items {
		refs = append(refs, actorRef)
	}

	return refs
}

// GetRemoteActor returns the address of a remote actor when cluster is enabled
func (a *actorSystem) GetRemoteActor(ctx context.Context, actorName string) (addr *pb.Address, err error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "GetRemoteActor")
	defer span.End()
	// check whether cluster is enabled or not
	if a.cluster == nil {
		return nil, errors.New("cluster is not enabled")
	}

	// let us locate the actor in the cluster
	wireActor, err := a.cluster.GetActor(ctx, actorName)
	// handle the eventual error
	if err != nil {
		return nil, errors.Wrapf(err, "failed to fetch remote actor=%s", actorName)
	}

	// return the address of the remote actor
	return wireActor.GetActorAddress(), nil
}

// GetLocalActor returns the reference of a local actor.
// A local actor is an actor that reside on the same node where the given actor system is running
func (a *actorSystem) GetLocalActor(ctx context.Context, actorName string) (PID, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "GetLocalActor")
	defer span.End()
	// first let us do a local lookup
	items := a.actors.Items()
	// iterate the local actors storage
	for _, actorRef := range items {
		if actorRef.ActorPath().Name() == actorName {
			return actorRef, nil
		}
	}
	return nil, fmt.Errorf("actor=%s not found", actorName)
}

// Start starts the actor system
func (a *actorSystem) Start(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Start")
	defer span.End()
	// set the has started to true
	a.hasStarted.Store(true)

	// start remoting when remoting is enabled
	if err := a.enableRemoting(ctx); err != nil {
		return err
	}

	// enable clustering when it is enabled
	if err := a.enableClustering(ctx); err != nil {
		return err
	}

	// start the metrics service
	// register metrics. However, we don't panic when we fail to register
	// we just log it for now
	// TODO decide what to do when we fail to register the metrics or export the metrics registration as public
	if err := a.registerMetrics(); err != nil {
		a.logger.Error(errors.Wrapf(err, "failed to register actorSystem=%s metrics", a.name))
	}
	a.logger.Infof("%s ActorSystem started on Node=%s...", a.name, a.nodeAddr)
	return nil
}

// Stop stops the actor system
func (a *actorSystem) Stop(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Stop")
	defer span.End()
	a.logger.Infof("%s ActorSystem is shutting down on Node=%s...", a.name, a.nodeAddr)

	// stop remoting service when set
	if a.remotingService != nil {
		a.remotingService.Stop(ctx)
	}

	// stop the cluster mode
	if a.cluster != nil {
		if err := a.cluster.Stop(ctx); err != nil {
			a.logger.Fatal(errors.Wrap(err, "failed to stop the cluster service"))
		}
	}

	// short-circuit the shutdown process when there are no online actors
	if len(a.Actors()) == 0 {
		a.logger.Info("No online actors to shutdown. Shutting down successfully done")
		return nil
	}

	// stop all the actors
	for _, actor := range a.Actors() {
		// remove the actor the map and shut it down
		a.actors.Remove(actor.ActorPath().String())
		// only shutdown live actors
		if actor.IsOnline() {
			if err := actor.Shutdown(ctx); err != nil {
				return err
			}
		}
	}

	// stop broadcasting cluster messages
	close(a.broadcastChan)

	// reset the actor system
	a.reset()

	return nil
}

// RegisterService register the remoting service
func (a *actorSystem) RegisterService(srv *ggrpc.Server) {
	goaktpb.RegisterRemoteMessagingServiceServer(srv, a)
}

// RemoteLookup for an actor on a remote host.
func (a *actorSystem) RemoteLookup(ctx context.Context, request *goaktpb.RemoteLookupRequest) (*goaktpb.RemoteLookupResponse, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteLookup")
	defer span.End()

	// get a context logger
	logger := a.logger

	// first let us make a copy of the incoming request
	reqCopy := proto.Clone(request).(*goaktpb.RemoteLookupRequest)

	// let us validate the host and port
	hostAndPort := fmt.Sprintf("%s:%d", reqCopy.GetHost(), reqCopy.GetPort())
	if hostAndPort != a.nodeAddr {
		// log the error
		logger.Error(ErrRemoteSendInvalidNode)
		// here message is sent to the wrong actor system node
		return nil, ErrRemoteSendInvalidNode
	}

	// construct the actor address
	name := reqCopy.GetName()
	actorPath := NewPath(name, NewLocalAddress(protocol, a.Name(), reqCopy.GetHost(), int(reqCopy.GetPort())))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := a.actors.Get(actorPath.String())
	// return an error when the remote address is not found
	if !exist {
		// log the error
		logger.Error(ErrRemoteActorNotFound(actorPath.String()))
		return nil, ErrRemoteActorNotFound(actorPath.String())
	}

	// let us construct the address
	addr := pid.ActorPath().RemoteAddress()

	return &goaktpb.RemoteLookupResponse{Address: addr}, nil
}

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately. With this type of message the receiver cannot communicate back to Sender
// except reply the message with a response. This one-way communication
func (a *actorSystem) RemoteAsk(ctx context.Context, request *goaktpb.RemoteAskRequest) (*goaktpb.RemoteAskResponse, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteAsk")
	defer span.End()

	// get a context logger
	logger := a.logger
	// first let us make a copy of the incoming request
	reqCopy := proto.Clone(request).(*goaktpb.RemoteAskRequest)

	// let us validate the host and port
	hostAndPort := fmt.Sprintf("%s:%d", reqCopy.GetReceiver().GetHost(), reqCopy.GetReceiver().GetPort())
	if hostAndPort != a.nodeAddr {
		// log the error
		logger.Error(ErrRemoteSendInvalidNode)
		// here message is sent to the wrong actor system node
		return nil, ErrRemoteSendInvalidNode
	}

	// construct the actor address
	name := reqCopy.GetReceiver().GetName()
	actorPath := NewPath(name, NewLocalAddress(protocol, a.name, a.host, a.port))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := a.actors.Get(actorPath.String())
	// return an error when the remote address is not found
	if !exist {
		// log the error
		logger.Error(ErrRemoteActorNotFound(actorPath.String()))
		return nil, ErrRemoteActorNotFound(actorPath.String())
	}
	// restart the actor when it is not live
	if !pid.IsOnline() {
		if err := pid.Restart(ctx); err != nil {
			return nil, err
		}
	}

	// send the message to actor
	reply, err := a.handleRemoteAsk(ctx, pid, reqCopy.GetMessage())
	// handle the error
	if err != nil {
		logger.Error(ErrRemoteSendFailure(err))
		return nil, ErrRemoteSendFailure(err)
	}
	// let us marshal the reply
	marshaled, _ := anypb.New(reply)
	return &goaktpb.RemoteAskResponse{Message: marshaled}, nil
}

// RemoteTell is used to send a message to an actor remotely by another actor
// This is the only way remote actors can interact with each other. The actor on the
// other line can reply to the sender by using the Sender in the message
func (a *actorSystem) RemoteTell(ctx context.Context, request *goaktpb.RemoteTellRequest) (*goaktpb.RemoteTellResponse, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteTell")
	defer span.End()

	// get a context logger
	logger := a.logger
	// first let us make a copy of the incoming request
	reqCopy := proto.Clone(request).(*goaktpb.RemoteTellRequest)

	receiver := reqCopy.GetRemoteMessage().GetReceiver()
	// let us validate the host and port
	hostAndPort := fmt.Sprintf("%s:%d", receiver.GetHost(), receiver.GetPort())
	if hostAndPort != a.nodeAddr {
		// log the error
		logger.Error(ErrRemoteSendInvalidNode)
		// here message is sent to the wrong actor system node
		return nil, ErrRemoteSendInvalidNode
	}

	// construct the actor address
	actorPath := NewPath(
		receiver.GetName(),
		NewLocalAddress(
			protocol,
			a.Name(),
			receiver.GetHost(),
			int(receiver.GetPort())))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := a.actors.Get(actorPath.String())
	// return an error when the remote address is not found
	if !exist {
		// log the error
		logger.Error(ErrRemoteActorNotFound(actorPath.String()))
		return nil, ErrRemoteActorNotFound(actorPath.String())
	}
	// restart the actor when it is not live
	if !pid.IsOnline() {
		if err := pid.Restart(ctx); err != nil {
			return nil, err
		}
	}

	// send the message to actor
	if err := a.handleRemoteTell(ctx, pid, reqCopy.GetRemoteMessage()); err != nil {
		logger.Error(ErrRemoteSendFailure(err))
		return nil, ErrRemoteSendFailure(err)
	}
	return &goaktpb.RemoteTellResponse{}, nil
}

// registerMetrics register the PID metrics with OTel instrumentation.
func (a *actorSystem) registerMetrics() error {
	// grab the OTel meter
	meter := a.telemetry.Meter
	// create an instance of the ActorMetrics
	metrics, err := telemetry.NewSystemMetrics(meter)
	// handle the error
	if err != nil {
		return err
	}

	// register the metrics
	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		observer.ObserveInt64(metrics.ActorSystemActorsCount, int64(a.NumActors()))
		return nil
	}, metrics.ActorSystemActorsCount)

	return err
}

// handleRemoteAsk handles a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (a *actorSystem) handleRemoteAsk(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "handleRemoteAsk")
	defer span.End()
	return SendSync(ctx, to, message, a.config.ReplyTimeout())
}

// handleRemoteTell handles an asynchronous message to an actor
func (a *actorSystem) handleRemoteTell(ctx context.Context, to PID, message proto.Message) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "handleRemoteTell")
	defer span.End()
	return SendAsync(ctx, to, message)
}

// enableRemoting enables the remoting service to handle remote messaging
func (a *actorSystem) enableRemoting(ctx context.Context) error {
	// start remoting when remoting is enabled
	if a.config.remotingEnabled {
		// build the grpc server
		config := &grpc.Config{
			ServiceName:      a.Name(),
			GrpcPort:         int32(a.Port()),
			GrpcHost:         a.Host(),
			TraceEnabled:     false, // TODO
			TraceURL:         "",    // TODO
			EnableReflection: false,
			Logger:           a.logger,
		}

		// build the grpc service
		remotingService, err := grpc.
			GetServerBuilder(config).
			WithService(a).
			Build()

		// handle the error
		if err != nil {
			a.logger.Error(errors.Wrap(err, "failed to start remoting service"))
			return err
		}

		// set the remoting service
		a.remotingService = remotingService
		// start the remoting service
		a.remotingService.Start(ctx)
	}
	return nil
}

// enableClustering enables the clustering mode
func (a *actorSystem) enableClustering(ctx context.Context) error {
	// check whether cluster is enabled or not
	if a.config.clusterEnabled {
		// add some logging
		a.logger.Info("bootstrapping clustering...")
		// create an instance of the cluster configuration
		config := &cluster.Config{
			Logger:    a.logger,
			Host:      a.Host(),
			Port:      int32(a.Port()),
			StateDir:  a.config.ClusterStateDir(),
			Name:      a.config.ClusterName(),
			Discovery: a.config.DiscoMethod(),
		}
		// create an instance of the cluster service
		a.cluster = cluster.New(config)
		// let us start the cluster service
		if err := a.cluster.Start(ctx); err != nil {
			a.logger.Error(errors.Wrap(err, "failed to start cluster service"))
			return err
		}

		// create the bootstrap channel
		bootstrapChan := make(chan struct{}, 1)
		// let us wait for some time for the cluster to be properly started
		timer := time.AfterFunc(time.Second, func() {
			bootstrapChan <- struct{}{}
		})

		<-bootstrapChan
		timer.Stop()
		a.logger.Info("clustering successfully bootstrapped")
		// start broadcasting cluster message
		go a.broadcast(ctx)
	}
	return nil
}

// reset the actor system
func (a *actorSystem) reset() {
	// void the settings
	a.config = nil
	a.hasStarted = atomic.NewBool(false)
	a.remotingService = nil
	a.telemetry = nil
	a.actors = cmp.New[PID]()
	a.nodeAddr = ""
	a.name = ""
	a.host = ""
	a.port = -1
	a.logger = nil
	a.cluster = nil
	once.Reset()
}

// broadcast publishes newly created actor into the cluster when cluster is enabled
func (a *actorSystem) broadcast(ctx context.Context) {
	// iterate over the channel
	for wireActor := range a.broadcastChan {
		// making sure the cluster is still enabled
		if a.cluster != nil {
			// broadcast the message on the cluster
			if err := a.cluster.PutActor(ctx, wireActor); err != nil {
				a.logger.Error(err.Error())
				// TODO: stop or continue
				return
			}
		}
	}
}
