package actors

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"time"

	"github.com/tochemey/goakt/discovery"

	"github.com/bufbuild/connect-go"
	otelconnect "github.com/bufbuild/connect-opentelemetry-go"
	cmp "github.com/orcaman/concurrent-map/v2"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/cluster"
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
	// Specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	actorInitMaxRetries int
	// Specifies the supervisor strategy
	supervisorStrategy StrategyDirective
	// Specifies the telemetry config
	telemetry *telemetry.Telemetry
	// Specifies whether remoting is enabled.
	// This allows to handle remote messaging
	remotingEnabled bool
	// Specifies the remoting port
	remotingPort int32
	// Specifies the remoting host
	remotingHost string
	// Specifies the remoting server
	remotingServer *http.Server

	// convenient field to check cluster setup
	clusterEnabled bool
	// cluster discovery method
	disco discovery.Discovery
	// cluster mode
	clusterService *cluster.Cluster
	clusterChan    chan *goaktpb.WireActor
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
	// the function only gets called one
	once.Do(func() {
		system = &actorSystem{
			actors:              cmp.New[PID](),
			hasStarted:          atomic.NewBool(false),
			typesLoader:         NewTypesLoader(),
			clusterChan:         make(chan *goaktpb.WireActor, 10),
			name:                name,
			logger:              log.DefaultLogger,
			expireActorAfter:    2 * time.Second,
			replyTimeout:        100 * time.Millisecond,
			actorInitMaxRetries: 5,
			supervisorStrategy:  StopDirective,
			telemetry:           telemetry.New(),
			remotingEnabled:     false,
		}
		// set the reflection
		system.reflection = NewReflection(system.typesLoader)
		// apply the various options
		for _, opt := range opts {
			opt.Apply(system)
		}
	})

	return system, nil
}

// InCluster states whether the actor system is running within a cluster of nodes
func (a *actorSystem) InCluster() bool {
	return a.clusterEnabled && a.clusterService != nil
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
	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(protocol, a.name, "", -1))
	// set the actor path with the remoting is enabled
	if a.remotingEnabled {
		// get the path of the given actor
		actorPath = NewPath(name, NewAddress(protocol, a.name, a.remotingHost, int(a.remotingPort)))
	}
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
		withInitMaxRetries(a.actorInitMaxRetries),
		withPassivationAfter(a.expireActorAfter),
		withSendReplyTimeout(a.replyTimeout),
		withCustomLogger(a.logger),
		withActorSystem(a),
		withSupervisorStrategy(a.supervisorStrategy),
		withTelemetry(a.telemetry))

	// add the given actor to the actor map
	a.actors.Set(actorPath.String(), pid)

	// let us register the actor
	a.typesLoader.Register(name, actor)

	// when cluster is enabled replicate the actor metadata across the cluster
	if a.clusterEnabled {
		// encode the actor
		actorType := reflect.TypeOf(actor)
		var buf bytes.Buffer
		enc := gob.NewEncoder(&buf)
		if err := enc.Encode(actorType); err != nil {
			a.logger.Warnf("failed to encode the underlying actor=%s", name)
			// TODO: at the moment the byte array of the underlying actor is not used but can become handy in the future
		}

		// create a wire actor
		wiredActor := &goaktpb.WireActor{
			ActorName:    name,
			ActorAddress: actorPath.RemoteAddress(),
			ActorPayload: buf.Bytes(),
		}
		// send it to the cluster channel
		a.clusterChan <- wiredActor
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
	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(protocol, a.name, "", -1))
	// set the actor path with the remoting is enabled
	if a.remotingEnabled {
		// get the path of the given actor
		actorPath = NewPath(name, NewAddress(protocol, a.name, a.remotingHost, int(a.remotingPort)))
	}
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
	// set the default actor path assuming we are running locally
	actorPath := NewPath(name, NewAddress(protocol, a.name, "", -1))
	// set the actor path with the remoting is enabled
	if a.remotingEnabled {
		// get the path of the given actor
		actorPath = NewPath(name, NewAddress(protocol, a.name, a.remotingHost, int(a.remotingPort)))
	}
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
	// add a logger
	a.logger.Infof("actor=%s not found", actorName)
	return nil, ErrActorNotFound
}

// GetRemoteActor returns the address of a remote actor when cluster is enabled
func (a *actorSystem) GetRemoteActor(ctx context.Context, actorName string) (addr *pb.Address, err error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "GetRemoteActor")
	defer span.End()
	// check whether cluster is enabled or not
	if a.clusterService == nil {
		return nil, errors.New("cluster is not enabled")
	}

	// let us locate the actor in the cluster
	wireActor, err := a.clusterService.GetActor(ctx, actorName)
	// handle the eventual error
	if err != nil {
		// grab the error code
		code := connect.CodeOf(err)
		// in case we get a not found from the cluster service
		if code == connect.CodeNotFound {
			a.logger.Infof("actor=%s not found", actorName)
			return nil, ErrActorNotFound
		}
		return nil, errors.Wrapf(err, "failed to fetch remote actor=%s", actorName)
	}

	// return the address of the remote actor
	return wireActor.GetActorAddress(), nil
}

// Start starts the actor system
func (a *actorSystem) Start(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Start")
	defer span.End()
	// set the has started to true
	a.hasStarted.Store(true)

	// start remoting when remoting is enabled
	if a.remotingEnabled {
		go a.enableRemoting(ctx)
	}

	// enable clustering when it is enabled
	if a.clusterEnabled {
		go a.enableClustering(ctx)
	}

	// start the metrics service
	// register metrics. However, we don't panic when we fail to register
	// we just log it for now
	// TODO decide what to do when we fail to register the metrics or export the metrics registration as public
	if err := a.registerMetrics(); err != nil {
		a.logger.Error(errors.Wrapf(err, "failed to register actorSystem=%s metrics", a.name))
	}
	a.logger.Infof("%s ActorSystem started..:)", a.name)
	return nil
}

// Stop stops the actor system
func (a *actorSystem) Stop(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Stop")
	defer span.End()
	a.logger.Infof("ActorSystem is shutting down on Cluster=%s..:)", a.name)

	// stop the remoting server
	if a.remotingEnabled {
		// stop the server
		if err := a.remotingServer.Close(); err != nil {
			return err
		}
	}

	// stop the cluster service
	if a.clusterEnabled {
		// stop the cluster service
		if err := a.clusterService.Stop(); err != nil {
			return err
		}
		// stop broadcasting cluster messages
		close(a.clusterChan)
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

	// reset the actor system
	a.reset()

	return nil
}

// RemoteLookup for an actor on a remote host.
func (a *actorSystem) RemoteLookup(ctx context.Context, request *connect.Request[goaktpb.RemoteLookupRequest]) (*connect.Response[goaktpb.RemoteLookupResponse], error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteLookup")
	defer span.End()

	// get a context log
	logger := a.logger

	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	// set the actor path with the remoting is enabled
	if !a.remotingEnabled {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingNotEnabled)
	}

	// get the remoting server address
	nodeAddr := fmt.Sprintf("%s:%d", a.remotingHost, a.remotingPort)

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
	actorPath := NewPath(name, NewAddress(protocol, a.Name(), reqCopy.GetHost(), int(reqCopy.GetPort())))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := a.actors.Get(actorPath.String())
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
func (a *actorSystem) RemoteAsk(ctx context.Context, request *connect.Request[goaktpb.RemoteAskRequest]) (*connect.Response[goaktpb.RemoteAskResponse], error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteAsk")
	defer span.End()

	// get a context log
	logger := a.logger
	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	// set the actor path with the remoting is enabled
	if !a.remotingEnabled {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingNotEnabled)
	}

	// get the remoting server address
	nodeAddr := fmt.Sprintf("%s:%d", a.remotingHost, a.remotingPort)

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
	actorPath := NewPath(name, NewAddress(protocol, a.name, a.remotingHost, int(a.remotingPort)))

	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := a.actors.Get(actorPath.String())
	// return an error when the remote address is not found
	if !exist {
		// log the error
		logger.Error(ErrRemoteActorNotFound(actorPath.String()).Error())
		return nil, ErrRemoteActorNotFound(actorPath.String())
	}
	// restart the actor when it is not live
	if !pid.IsOnline() {
		if err := pid.Restart(ctx); err != nil {
			return nil, err
		}
	}

	// send the message to actor
	reply, err := a.handleRemoteAsk(ctx, pid, reqCopy.GetRemoteMessage())
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
func (a *actorSystem) RemoteTell(ctx context.Context, request *connect.Request[goaktpb.RemoteTellRequest]) (*connect.Response[goaktpb.RemoteTellResponse], error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RemoteTell")
	defer span.End()

	// get a context log
	logger := a.logger
	// first let us make a copy of the incoming request
	reqCopy := request.Msg

	receiver := reqCopy.GetRemoteMessage().GetReceiver()

	// set the actor path with the remoting is enabled
	if !a.remotingEnabled {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrRemotingNotEnabled)
	}

	// get the remoting server address
	nodeAddr := fmt.Sprintf("%s:%d", a.remotingHost, a.remotingPort)

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
			a.Name(),
			receiver.GetHost(),
			int(receiver.GetPort())))
	// start or get the PID of the actor
	// check whether the given actor already exist in the system or not
	pid, exist := a.actors.Get(actorPath.String())
	// return an error when the remote address is not found
	if !exist {
		// log the error
		logger.Error(ErrRemoteActorNotFound(actorPath.String()).Error())
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
	return connect.NewResponse(new(goaktpb.RemoteTellResponse)), nil
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

	// define the common labels
	labels := []attribute.KeyValue{
		attribute.String("actor.system", a.Name()),
	}

	// register the metrics
	_, err = meter.RegisterCallback(func(ctx context.Context, observer metric.Observer) error {
		observer.ObserveInt64(metrics.ActorSystemActorsCount, int64(a.NumActors()), metric.WithAttributes(labels...))
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
	return Ask(ctx, to, message, a.replyTimeout)
}

// handleRemoteTell handles an asynchronous message to an actor
func (a *actorSystem) handleRemoteTell(ctx context.Context, to PID, message proto.Message) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "handleRemoteTell")
	defer span.End()
	return Tell(ctx, to, message)
}

// enableClustering enables clustering. When clustering is enabled remoting is also enabled to facilitate remote
// communication
func (a *actorSystem) enableClustering(ctx context.Context) {
	// create an instance of the cluster service and start it
	cluster := cluster.New(a.disco, a.logger)
	// set the cluster field of the actorSystem
	a.clusterService = cluster
	// start the cluster service
	if err := a.clusterService.Start(ctx); err != nil {
		a.logger.Panic(errors.Wrap(err, "failed to start remoting service"))
	}

	// create the bootstrap channel
	bootstrapChan := make(chan struct{}, 1)
	// let us wait for some time for the cluster to be properly started
	timer := time.AfterFunc(time.Second, func() {
		bootstrapChan <- struct{}{}
	})

	<-bootstrapChan
	timer.Stop()

	// set the remoting host
	a.remotingHost = cluster.NodeHost()
	// let us enable remoting as well if not yet enabled
	if !a.remotingEnabled {
		a.enableRemoting(ctx)
	}
	// start broadcasting cluster message
	go a.broadcast(ctx)
}

// enableRemoting enables the remoting service to handle remote messaging
func (a *actorSystem) enableRemoting(context.Context) {
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
		a,
		connect.WithInterceptors(interceptor(a.telemetry.TracerProvider, a.telemetry.MeterProvider)),
	)
	mux.Handle(path, handler)
	// create the address
	serverAddr := fmt.Sprintf(":%d", a.remotingPort)

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
	}

	// set the server
	a.remotingServer = server

	// listen and service requests
	if err := a.remotingServer.ListenAndServe(); err != nil {
		// check the error type
		if !errors.Is(err, http.ErrServerClosed) {
			a.logger.Panic(errors.Wrap(err, "failed to start remoting service"))
		}
	}
}

// reset the actor system
func (a *actorSystem) reset() {
	// void the settings
	a.hasStarted = atomic.NewBool(false)
	a.telemetry = nil
	a.actors = cmp.New[PID]()
	a.name = ""
	a.logger = nil
	a.remotingServer = nil
	once.Reset()
}

// broadcast publishes newly created actor into the cluster when cluster is enabled
func (a *actorSystem) broadcast(ctx context.Context) {
	// iterate over the channel
	for wireActor := range a.clusterChan {
		// making sure the cluster is still enabled
		if a.clusterService != nil {
			// broadcast the message on the cluster
			if err := a.clusterService.PutActor(ctx, wireActor); err != nil {
				a.logger.Error(err.Error())
				// TODO: stop or continue
				return
			}
		}
	}
}
