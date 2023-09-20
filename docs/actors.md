## Actors

### Overview

Actors are the building blocks of Go-Akt. An actor is the primitive unit of computation. It’s the thing that receives a message and does some kind of computation based on it.
The characteristics of Actors are:

- PID - the actor reference
- Mailbox - to receive and process messages
- Behavior - help build finite state machines
- Share nothing - automatically thread-safe without having to use locks or any other shared-memory synchronization mechanisms
- Stateful or stateless
- Lifecycle - can live or die
- Messaging - can send messages to other actors both locally and remotely

### ActorSystem

Without an actor system, it is not possible to create actors in Go-Akt. Only a single actor system is recommended to be created per application per node when using Go-Akt.
At the singleton nature of an actor system is not enforced by the library. This is the sole responsibility of the application developer.

#### Start and Stop

```go
// define the actor system name
const actorSystemName = "accountingSystem"

// create a go context
ctx := context.Background()

// create the actor system
actorSystem := actors.NewActorSystem(actorSystemName)

// start the actor system
err := actorSystem.Start(ctx)

// stop the actor system
err = actorSystem.Stop(ctx)
```

#### Define Options

When creating an actor system one can:

- define the [Logger](../log/logger.go) to use.
- define the passivation(idle) timeout of actors created in the system.
- disable passivation
- define the reply timeout when using the Ask messaging pattern.
- define the PreStart exponential backoff max retries.
- define the supervisory strategy to use to handle failures.
- enable remoting.
- enable clustering (more on that in the clustering doc).
- define the shutdown timeout to allow graceful shutdown.
- define the mailbox size. Any number less than zero is disallowed.
- define a custom [mailbox](../actors/mailbox.go).
- enable stashing by defining the stash buffer size.

Kindly check the [options](../actors/option.go)

##### Sample

```go
// define the actor system name
const actorSystemName = "accountingSystem"

// create a go context
ctx := context.Background()

// define a logger using the Go-Akt logger implementation
logger := log.New(log.DebugLevel, os.Stdout)

// define the various options
opts := []actors.Option{
    actors.WithLogger(logger),
    actors.WithPassivationDisabled(),
    actors.WithActorInitMaxRetries(1),
    actors.WithReplyTimeout(5 * time.Second),
    actors.WithTelemetry(telemetry.New()),
    actors.WithSupervisorStrategy(actors.StopDirective),
}

// create the actor system
actorSystem := actors.NewActorSystem(actorSystemName, opts...)
```
### Actors

#### Define the actor receiver


```go
// UserActor is used to test the actor behavior
type UserActor struct{}

// enforce compilation error
var _ Actor = &UserActor{}

// PreStart hook is executed when actor is about to start
func (x *UserActor) PreStart(_ context.Context) error {
   return nil
}

// PostStop hook is executed when the actor has shutdown
func (x *UserActor) PostStop(_ context.Context) error {
   return nil
}

// Receive handles messages received
func (x *UserActor) Receive(ctx ReceiveContext) {
	// handle messages based upon type
    switch ctx.Message().(type) {
    case *testspb.CreateAccount:
		 // reply to an Ask message
        ctx.Response(new(testspb.AccountCreated))
    case *testspb.TestSend:
		 // here we do nothing with a Tell message
    }
}
```

#### Spawn and Stop

   
```go
// define the actor system name
const actorSystemName = "accountingSystem"

// create a go context
ctx := context.Background()

// create the actor system
actorSystem := actors.NewActorSystem(actorSystemName)

// start the actor system
_ := actorSystem.Start(ctx)

// spawn the actor
actorName := "user-1"
receiver := &UserActor{}
actor, _ := actorSystem.Spawn(ctx, actorName, receiver)

// stop the actor
_ := actorSystem.Kill(ctx, actorName)

// stop the actor system
_ = actorSystem.Stop(ctx)
```

#### Tell and Ask

```go
// define the actor system name
const actorSystemName = "accountingSystem"

// create a go context
ctx := context.Background()

// create the actor system
actorSystem := actors.NewActorSystem(actorSystemName)

// start the actor system
_ := actorSystem.Start(ctx)

// create the actor
actor := &UserActor{}

// spawn the user actor 1
user1 := "user-1"
pid1, _ := actorSystem.Spawn(ctx, user1, receiver)

// spawn the user actor 2
user2 := "user-2"
pid2, _ := actorSystem.Spawn(ctx, user2, receiver)

// user1 sends a Tell message to user2
_ = pid1.Tell(ctx, pid2, new(testpb.TestSend))

// user1 sends an Ask message to user2
reply, _ := pid1.Ask(ctx, pid2, new(testpb.CreateAccount))

// stop the actor system
_ = actorSystem.Stop(ctx)
```
