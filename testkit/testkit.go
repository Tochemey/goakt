package testkit

import (
	"context"
	"testing"
	"time"

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/log"
)

// TestKit defines actor test kit
type TestKit struct {
	actorSystem actors.ActorSystem
	kt          *testing.T
	logger      log.Logger
}

// New creates an instance of TestKit
func New(ctx context.Context, t *testing.T, opts ...Option) *TestKit {
	// create the testkit instance
	testkit := &TestKit{
		kt:     t,
		logger: log.DiscardLogger,
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(testkit)
	}
	// create an actor system
	system, err := actors.NewActorSystem(
		"testkit",
		actors.WithPassivationDisabled(),
		actors.WithLogger(testkit.logger),
		actors.WithActorInitTimeout(time.Second),
		actors.WithActorInitMaxRetries(5),
		actors.WithReplyTimeout(time.Minute))
	if err != nil {
		t.Fatal(err.Error())
	}

	// start the actor system
	if err := system.Start(ctx); err != nil {
		t.Fatal(err.Error())
	}

	testkit.actorSystem = system
	return testkit
}

// Spawn create an actor
func (k *TestKit) Spawn(ctx context.Context, name string, actor actors.Actor) *actors.PID {
	// create and instance of actor
	pid, err := k.actorSystem.Spawn(ctx, name, actor)
	// handle the error
	if err != nil {
		k.kt.Fatal(err.Error())
	}
	// return the created actor id
	return pid
}

// NewProbe create a test probe
func (k *TestKit) NewProbe(ctx context.Context) Probe {
	// create an instance of TestProbe
	testProbe, err := newProbe(ctx, k.actorSystem, k.kt)
	// handle the error
	if err != nil {
		k.kt.Fatal(err.Error())
	}

	// return the created test probe
	return testProbe
}

// Shutdown stops the test kit
func (k *TestKit) Shutdown(ctx context.Context) {
	if err := k.actorSystem.Stop(ctx); err != nil {
		k.kt.Fatal(err.Error())
	}
}
