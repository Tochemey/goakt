package actors

import (
	"context"
	"sync"

	cmp "github.com/orcaman/concurrent-map/v2"
	"github.com/tochemey/goakt/log"
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
		}
	})

	return cache, nil
}

// Spawn creates or returns the instance of a given actor in the system
func (a *actorSystem) Spawn(ctx context.Context, kind, id string, actor Actor) PID {
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
		withAddress(addr))

	// add the given actor to the actor map
	a.actors.Set(string(addr), pid)
	// return the actor ref
	return pid
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
	// set the has started to true
	a.hasStarted.Store(true)
	// start the housekeeper
	//go a.housekeeping()
	a.logger.Infof("%s System started on Node=%s...", a.name, a.nodeAddr)
	return nil
}

// Stop stops the actor system
func (a *actorSystem) Stop(ctx context.Context) error {
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
