package actors

import (
	"context"
	"sync"
	"time"

	"github.com/tochemey/goakt/logging"
	"golang.org/x/sync/errgroup"
)

var cache *actorSystem
var once sync.Once // 👈 defining a new `sync.Once

// ActorSystem represent a collection of actors on a given node
// Only a single instance of the ActorSystem can be created on a given node
type actorSystem struct {
	// Specifies the actor system name
	name string
	// Specifies the node where the actor system is located
	nodeAddr string
	// map of actors in the system
	actorMap *actorMap
	//  specifies the logger to use
	logger logging.Logger

	config *Config

	stopHouseKeeper chan struct{}
}

// enforce compilation error when all methods of the ActorSystem interface are not implemented
// by the struct actorSystem
var _ ActorSystem = (*actorSystem)(nil)

// NewActorSystem creates an instance of ActorSystem
func NewActorSystem(config *Config) ActorSystem {
	// 👇 the function only gets called one
	once.Do(func() {
		cache = &actorSystem{
			name:            config.Name,
			nodeAddr:        config.NodeHostAndPort,
			actorMap:        newActorMap(20),
			logger:          config.Logger,
			config:          config,
			stopHouseKeeper: make(chan struct{}),
		}
	})

	return cache
}

// Spawn creates or returns the instance of a given actor in the system
func (a *actorSystem) Spawn(ctx context.Context, kind string, actor Actor) *ActorRef {
	// create the address of the given actor
	addr := GetAddress(a, kind, actor.ID())
	// check whether the given actor already exist in the system or not
	actorRef, exist := a.actorMap.Get(addr)
	// actor already exist no need recreate it.
	if exist {
		// check whether the given actor heart beat
		if actorRef.IsReady(ctx) {
			// return the existing instance
			return actorRef
		}
	}

	// create an instance of the actor ref
	actorRef = NewActorRef(ctx, actor,
		WithInitMaxRetries(a.config.InitMaxRetries),
		WithPassivationAfter(a.config.PassivateAfter),
		WithSendReplyTimeout(a.config.SendRecvTimeout),
		WithAddress(addr))

	// add the given actor to the actor map
	a.actorMap.Set(addr, actorRef)
	// return the actor ref
	return actorRef
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
func (a *actorSystem) Actors() []*ActorRef {
	// get the actors from the actor map
	return a.actorMap.GetAll()
}

// Start starts the actor system
func (a *actorSystem) Start(ctx context.Context) error {
	// start the housekeeper
	go a.housekeeping()
	a.logger.Infof("%s System started on Node=%s...", a.name, a.nodeAddr)
	return nil
}

// Stop stops the actor system
func (a *actorSystem) Stop(ctx context.Context) error {
	a.logger.Infof("%s System is shutting down on Node=%s...", a.name, a.nodeAddr)
	// tell the housekeeper to stop
	a.stopHouseKeeper <- struct{}{}
	// stop all the actors
	g, ctx := errgroup.WithContext(ctx)
	for _, actorRef := range a.actorMap.GetAll() {
		actorRef := actorRef
		g.Go(func() error {
			actorRef.Shutdown(ctx)
			return nil
		})
	}
	// Wait for all the actors to gracefully shutdown
	if err := g.Wait(); err == nil {
		a.logger.Errorf("failed to shutdown the actors, %v", err)
		return err
	}
	return nil
}

// housekeeping time to time removes dead actors from the system
// that helps free non-utilized resources
func (a *actorSystem) housekeeping() {
	// create the ticker
	ticker := time.NewTicker(time.Second)

	// init ticking
	go func() {
		for range ticker.C {
			// loop over the actors in the system and remove the dead one
			for _, actorRef := range a.actorMap.GetAll() {
				if !actorRef.IsReady(context.Background()) {
					// TODO add a logging info
					a.actorMap.Delete(actorRef.addr)
				}
			}
		}
	}()
	// wait for the stop signal to stop the ticker
	<-a.stopHouseKeeper
	// stop the ticker
	ticker.Stop()
}
