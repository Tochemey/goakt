package actors

import (
	"context"
	"fmt"

	cmp "github.com/orcaman/concurrent-map/v2"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/pkg/eventbus"
	"github.com/tochemey/goakt/pkg/grpc"
	"github.com/tochemey/goakt/pkg/resync"
	"github.com/tochemey/goakt/telemetry"
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
	// EventBus returns the actor system event bus
	EventBus() eventbus.EventBus
	// NumActors returns the total number of active actors in the system
	NumActors() uint64

	// handleSendSync handles a synchronous message to another actor and expect a response.
	// This block until a response is received or timed out.
	handleSendSync(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error)
	// handleSendAsync handles an asynchronous message to an actor
	handleSendAsync(ctx context.Context, to PID, message proto.Message) error
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

	// TODO: remove this. May not be needed
	eventBus eventbus.EventBus
	// observability settings
	telemetry *telemetry.Telemetry
	// specifies the remoting service
	remotingService grpc.Server
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
			eventBus:        eventbus.New(),
			telemetry:       config.telemetry,
			remotingService: nil,
		}
		// set host and port
		host, port := config.HostAndPort()
		system.host = host
		system.port = port
	})

	return system, nil
}

// EventBus returns the actor system event streams
func (a *actorSystem) EventBus() eventbus.EventBus {
	return a.eventBus
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
	actorPath := NewPath(name, NewAddress(protocol, a.name, a.host, a.port))
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
	actorPath := NewPath(name, NewAddress(protocol, a.name, a.host, a.port))
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
	actorPath := NewPath(name, NewAddress(protocol, a.name, a.host, a.port))
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

// Start starts the actor system
func (a *actorSystem) Start(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Start")
	defer span.End()
	// set the has started to true
	a.hasStarted.Store(true)

	// start remoting when remoting is enabled
	if err := a.startRemoting(ctx); err != nil {
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

	// short-circuit the shutdown process when there are no online actors
	if len(a.Actors()) == 0 {
		a.logger.Info("No online actors to shutdown. Shutting down successfully done")
		return nil
	}

	// stop all the actors
	for _, actor := range a.Actors() {
		if err := actor.Shutdown(ctx); err != nil {
			return err
		}
		a.actors.Remove(actor.ActorPath().String())
	}

	// reset the actor system
	a.reset()

	return nil
}

// RegisterService register the remoting service
func (a *actorSystem) RegisterService(srv *ggrpc.Server) {
	pb.RegisterRemotingServiceServer(srv, a)
}

// RemoteLookup for an actor on a remote host.
func (a *actorSystem) RemoteLookup(ctx context.Context, request *pb.RemoteLookupRequest) (*pb.RemoteLookupResponse, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteLookup")
	defer span.End()

	// get a context logger
	logger := a.logger.WithContext(ctx)

	// first let us make a copy of the incoming request
	reqCopy := proto.Clone(request).(*pb.RemoteLookupRequest)

	// check whether the actor system is set and validate it
	// if the actor system is not set we just use the current actor system name to perform the lookup
	sys := reqCopy.GetAddress().GetActorSystem()
	if sys != a.name {
		// log the error
		logger.Error(ErrRemoteSendInvalidActorSystem)
		// here message is sent to the wrong actor system
		return nil, ErrRemoteSendInvalidActorSystem
	}

	// let us validate the host and port
	hostAndPort := fmt.Sprintf("%s:%d", reqCopy.GetAddress().GetHost(), reqCopy.GetAddress().GetPort())
	if hostAndPort != a.nodeAddr {
		// log the error
		logger.Error(ErrRemoteSendInvalidNode)
		// here message is sent to the wrong actor system node
		return nil, ErrRemoteSendInvalidNode
	}

	// construct the actor address
	name := reqCopy.GetAddress().GetName()
	actorPath := NewPath(name, NewAddress(protocol, a.name, a.host, a.port))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	_, exist := a.actors.Get(actorPath.String())
	// return an error when the remote address is not found
	if !exist {
		// log the error
		logger.Error(ErrRemoteActorNotFound(actorPath.String()))
		return nil, ErrRemoteActorNotFound(actorPath.String())
	}

	return &pb.RemoteLookupResponse{}, nil
}

// RemoteSendSync handles a message to an actor remotely with a reply expected from the receiving actor
func (a *actorSystem) RemoteSendSync(ctx context.Context, request *pb.RemoteSendSyncRequest) (*pb.RemoteSendSyncResponse, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteSendSync")
	defer span.End()

	// get a context logger
	logger := a.logger.WithContext(ctx)
	// first let us make a copy of the incoming request
	reqCopy := proto.Clone(request).(*pb.RemoteSendSyncRequest)

	// check whether the actor system is set and validate it
	// if the actor system is not set we just use the current actor system name to perform the lookup
	sys := reqCopy.GetReceiver().GetActorSystem()
	if sys != a.name {
		// log the error
		logger.Error(ErrRemoteSendInvalidActorSystem)
		// here message is sent to the wrong actor system
		return nil, ErrRemoteSendInvalidActorSystem
	}

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
	actorPath := NewPath(name, NewAddress(protocol, a.name, a.host, a.port))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := a.actors.Get(string(actorPath.String()))
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
	reply, err := a.handleSendSync(ctx, pid, reqCopy.GetMessage())
	// handle the error
	if err != nil {
		logger.Error(ErrRemoteSendFailure(err))
		return nil, ErrRemoteSendFailure(err)
	}
	// let us marshal the reply
	marshaled, _ := anypb.New(reply)
	return &pb.RemoteSendSyncResponse{Message: marshaled}, nil
}

// RemoteSendAsync handles a message to an actor remotely without expecting any reply
func (a *actorSystem) RemoteSendAsync(ctx context.Context, request *pb.RemoteSendAsyncRequest) (*pb.RemoteSendAsyncResponse, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteSendAsync")
	defer span.End()

	// get a context logger
	logger := a.logger.WithContext(ctx)
	// first let us make a copy of the incoming request
	reqCopy := proto.Clone(request).(*pb.RemoteSendAsyncRequest)

	// check whether the actor system is set and validate it
	// if the actor system is not set we just use the current actor system name to perform the lookup
	sys := reqCopy.GetAddress().GetActorSystem()
	if sys != a.name {
		// log the error
		logger.Error(ErrRemoteSendInvalidActorSystem)
		// here message is sent to the wrong actor system
		return nil, ErrRemoteSendInvalidActorSystem
	}

	// let us validate the host and port
	hostAndPort := fmt.Sprintf("%s:%d", reqCopy.GetAddress().GetHost(), reqCopy.GetAddress().GetPort())
	if hostAndPort != a.nodeAddr {
		// log the error
		logger.Error(ErrRemoteSendInvalidNode)
		// here message is sent to the wrong actor system node
		return nil, ErrRemoteSendInvalidNode
	}

	// construct the actor address
	actorPath := NewPath(
		reqCopy.GetAddress().GetName(),
		NewAddress(
			protocol,
			reqCopy.GetAddress().GetActorSystem(),
			reqCopy.GetAddress().GetHost(),
			int(reqCopy.GetAddress().GetPort())))
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
	if err := a.handleSendAsync(ctx, pid, reqCopy.GetMessage()); err != nil {
		logger.Error(ErrRemoteSendFailure(err))
		return nil, ErrRemoteSendFailure(err)
	}
	return &pb.RemoteSendAsyncResponse{}, nil
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

// handleSendSync handles a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (a *actorSystem) handleSendSync(ctx context.Context, to PID, message proto.Message) (response proto.Message, err error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "handleSendSync")
	defer span.End()
	return SendSync(ctx, to, message, a.config.ReplyTimeout())
}

// handleSendAsync handles an asynchronous message to an actor
func (a *actorSystem) handleSendAsync(ctx context.Context, to PID, message proto.Message) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "handleSendSync")
	defer span.End()
	return SendAsync(ctx, to, message)
}

// startRemoting starts the remoting service to handle remote messages
func (a *actorSystem) startRemoting(ctx context.Context) error {
	// start remoting when remoting is enabled
	if a.config.remotingEnabled {
		// build the grpc server
		config := &grpc.Config{
			ServiceName:      a.Name(),
			GrpcPort:         a.Port(),
			GrpcHost:         a.Host(),
			TraceEnabled:     false, // TODO
			TraceURL:         "",    // TODO
			EnableReflection: false,
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

// reset the actor system
func (a *actorSystem) reset() {
	// void the settings
	a.config = nil
	a.hasStarted = atomic.NewBool(false)
	a.remotingService = nil
	a.telemetry = nil
	a.eventBus = nil
	a.actors = cmp.New[PID]()
	a.nodeAddr = ""
	a.name = ""
	a.host = ""
	a.port = -1
	a.logger = nil
	once.Reset()
}
