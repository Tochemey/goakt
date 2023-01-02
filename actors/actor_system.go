package actors

import (
	"context"
	"fmt"
	"sync"

	cmp "github.com/orcaman/concurrent-map/v2"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/eventbus"
	"github.com/tochemey/goakt/telemetry"
	"go.uber.org/atomic"
)

var cache *actorSystem
var once sync.Once

// ActorSystem defines the contract of an actor system
type ActorSystem interface {
	// Name returns the actor system name
	Name() string
	// NodeAddr returns the node where the actor system is running
	NodeAddr() string
	// Actors returns the list of Actors that are alive in the actor system
	Actors() []PID
	// Start starts the actor system
	Start(ctx context.Context) error
	// Stop stops the actor system
	Stop(ctx context.Context) error
	// Spawn creates an actor in the system
	Spawn(ctx context.Context, kind, id string, actor Actor) PID
	// StopActor stops a given actor in the system
	StopActor(ctx context.Context, kind, id string) error
	// RestartActor restarts a given actor in the system
	RestartActor(ctx context.Context, kind, id string) (PID, error)
	// EventBus returns the actor system event bus
	EventBus() eventbus.EventBus
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

	// actor system configuration
	config *Config

	hasStarted *atomic.Bool

	eventBus eventbus.EventBus
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
		cache = &actorSystem{
			name:       config.Name(),
			nodeAddr:   config.NodeHostAndPort(),
			actors:     cmp.New[PID](),
			logger:     config.Logger(),
			config:     config,
			hasStarted: atomic.NewBool(false),
			eventBus:   eventbus.New(),
		}
	})

	return cache, nil
}

// EventBus returns the actor system event streams
func (a *actorSystem) EventBus() eventbus.EventBus {
	return a.eventBus
}

// Spawn creates or returns the instance of a given actor in the system
func (a *actorSystem) Spawn(ctx context.Context, kind, id string, actor Actor) PID {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Spawn")
	defer span.End()
	// first check whether the actor system has started
	if !a.hasStarted.Load() {
		return nil
	}
	// create the address of the given actor
	addr := GetAddress(a, kind, id)
	// check whether the given actor already exist in the system or not
	pid, exist := a.actors.Get(string(addr))
	// actor already exist no need recreate it.
	if exist {
		// check whether the given actor heart beat
		if pid.IsOnline() {
			// return the existing instance
			return pid
		}
	}

	// create an instance of the actor ref
	pid = newPID(ctx, actor,
		withInitMaxRetries(a.config.ActorInitMaxRetries()),
		withPassivationAfter(a.config.ExpireActorAfter()),
		withSendReplyTimeout(a.config.ReplyTimeout()),
		withCustomLogger(a.config.logger),
		withActorSystem(a),
		withLocalID(kind, id),
		withSupervisorStrategy(a.config.supervisorStrategy),
		withTelemetry(a.config.telemetry),
		withAddress(addr))

	// add the given actor to the actor map
	a.actors.Set(string(addr), pid)
	// return the actor ref
	return pid
}

// StopActor stops a given actor in the system
func (a *actorSystem) StopActor(ctx context.Context, kind, id string) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "StopActor")
	defer span.End()
	// first check whether the actor system has started
	if !a.hasStarted.Load() {
		return errors.New("actor system has not started yet")
	}
	// create the address of the given actor
	addr := GetAddress(a, kind, id)
	// check whether the given actor already exist in the system or not
	pid, exist := a.actors.Get(string(addr))
	// actor is found.
	if exist {
		// stop the given actor
		return pid.Shutdown(ctx)
	}
	return fmt.Errorf("actor=%s not found in the system", addr)
}

// RestartActor restarts a given actor in the system
func (a *actorSystem) RestartActor(ctx context.Context, kind, id string) (PID, error) {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "RestartActor")
	defer span.End()
	// first check whether the actor system has started
	if !a.hasStarted.Load() {
		return nil, errors.New("actor system has not started yet")
	}
	// create the address of the given actor
	addr := GetAddress(a, kind, id)
	// check whether the given actor already exist in the system or not
	pid, exist := a.actors.Get(string(addr))
	// actor is found.
	if exist {
		// restart the given actor
		if err := pid.Restart(ctx); err != nil {
			// return the error in case the restart failed
			return nil, errors.Wrapf(err, "failed to restart actor=%s", addr)
		}
		return pid, nil
	}
	return nil, fmt.Errorf("actor=%s not found in the system", addr)
}

// Name returns the actor system name
func (a *actorSystem) Name() string {
	return a.name
}

// NodeAddr returns the node where the actor system is running
func (a *actorSystem) NodeAddr() string {
	return a.nodeAddr
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
	// start the housekeeper
	//go a.housekeeping()
	a.logger.Infof("%s System started on Node=%s...", a.name, a.nodeAddr)
	return nil
}

// Stop stops the actor system
func (a *actorSystem) Stop(ctx context.Context) error {
	// add a span context
	ctx, span := telemetry.SpanContext(ctx, "Stop")
	defer span.End()
	a.logger.Infof("%s System is shutting down on Node=%s...", a.name, a.nodeAddr)

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
		a.actors.Remove(string(actor.Address()))
	}

	return nil
}
