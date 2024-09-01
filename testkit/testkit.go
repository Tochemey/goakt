package testkit

import (
	"context"
	"testing"
	"time"

	"github.com/tochemey/goakt/v2/actor"
	"github.com/tochemey/goakt/v2/log"
)

// TestKit defines actor test kit
type TestKit struct {
	actorSystem actor.System
	kt          *testing.T
}

// New creates an instance of TestKit
func New(ctx context.Context, t *testing.T) *TestKit {
	// create an actor system
	system, err := actor.NewSystem(
		"testkit",
		actor.WithPassivationDisabled(),
		actor.WithLogger(log.DefaultLogger),
		actor.WithActorInitTimeout(time.Second),
		actor.WithActorInitMaxRetries(5),
		actor.WithAskTimeout(time.Minute))
	if err != nil {
		t.Fatal(err.Error())
	}

	// start the actor system
	if err := system.Start(ctx); err != nil {
		t.Fatal(err.Error())
	}

	return &TestKit{
		actorSystem: system,
		kt:          t,
	}
}

// Spawn create an actor
func (k *TestKit) Spawn(ctx context.Context, name string, actor actor.Actor) *actor.PID {
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
