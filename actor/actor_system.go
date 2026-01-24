// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package actor

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	stdhttp "net/http"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	goset "github.com/deckarep/golang-set/v2"
	"github.com/flowchartsman/retry"
	"go.akshayshah.org/connectproto"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"
	"go.uber.org/multierr"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/discovery"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/eventstream"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/hash"
	"github.com/tochemey/goakt/v3/internal/chain"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/codec"
	"github.com/tochemey/goakt/v3/internal/compression/brotli"
	"github.com/tochemey/goakt/v3/internal/compression/zstd"
	"github.com/tochemey/goakt/v3/internal/datacentercontroller"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v3/internal/locker"
	"github.com/tochemey/goakt/v3/internal/metric"
	"github.com/tochemey/goakt/v3/internal/network"
	"github.com/tochemey/goakt/v3/internal/pointer"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/internal/ticker"
	"github.com/tochemey/goakt/v3/internal/types"
	"github.com/tochemey/goakt/v3/internal/validation"
	"github.com/tochemey/goakt/v3/internal/xsync"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/memory"
	"github.com/tochemey/goakt/v3/passivation"
	"github.com/tochemey/goakt/v3/remote"
	sup "github.com/tochemey/goakt/v3/supervisor"
	gtls "github.com/tochemey/goakt/v3/tls"
)

const (
	defaultReplicationFactor = 3
	defaultReplicationQuorum = 2 // Wait for 2-of-3 acks
)

// ActorSystem defines the contract of an actor system
//
//nolint:revive
type ActorSystem interface {
	// Metric retrieves the current set of runtime metrics for the actor system.
	//
	// This includes local actor system metrics such as the number of actors,
	// mailbox sizes, and message throughput. It does not include metrics
	// from other nodes in a distributed or clustered environment.
	//
	// Use this method for monitoring and debugging purposes within a single node.
	//
	// The provided context can be used to control timeouts or cancellations
	// of any background operations involved in collecting the metrics.
	Metric(ctx context.Context) *Metric
	// Name returns the actor system name
	Name() string
	// Actors returns the list of Actors that are alive on a given running node.
	// This does not account for the total number of actors in the cluster
	Actors() []*PID
	// ActorRefs retrieves a list of active actors, including both local actors
	// and, when cluster mode is enabled, actors across the cluster. Use this
	// method cautiously, as the scanning process may impact system performance.
	// If the cluster request fails, an error will be returned.
	// The timeout parameter defines the maximum duration for cluster-based requests
	// before they are terminated.
	ActorRefs(ctx context.Context, timeout time.Duration) ([]ActorRef, error)
	// Start initializes the actor system.
	// To guarantee a clean shutdown during unexpected system terminations,
	// developers must handle SIGTERM and SIGINT signals appropriately and invoke Stop.
	Start(ctx context.Context) error
	// Stop stops the actor system and does not terminate the program.
	// One needs to explicitly call os.Exit to terminate the program.
	Stop(ctx context.Context) error
	// Spawn creates and starts a new actor in the local actor system.
	//
	// The actor will be registered under the given `name`, allowing other actors
	// or components to send messages to it using the returned *PID. If an actor
	// with the same name already exists in the local system, an error will be returned.
	//
	// Parameters:
	//   - ctx: A context used to control cancellation and timeouts during the spawn process.
	//   - name: A unique identifier for the actor within the local actor system.
	//   - actor: An instance implementing the Actor interface, representing the behavior and lifecycle of the actor.
	//   - opts: Optional SpawnOptions to customize the actor's behavior (e.g., dependency, mailbox, supervisor strategy).
	//
	// Returns:
	//   - *PID: A pointer to the Process ID of the spawned actor, used for message passing.
	//   - error: An error if the actor could not be spawned (e.g., name conflict, invalid configuration).
	//
	// Example:
	//
	//   pid, err := system.Spawn(ctx, "user-service", NewUserActor())
	//   if err != nil {
	//       log.Fatalf("Failed to spawn actor: %v", err)
	//   }
	//
	// Note: Actors spawned using this method are confined to the local actor system.
	// For distributed scenarios, use a SpawnOn mechanism if available.
	Spawn(ctx context.Context, name string, actor Actor, opts ...SpawnOption) (*PID, error)
	// SpawnOn creates and starts an actor, either locally or on a remote node,
	// depending on the configuration of the actor system.
	//
	// In cluster mode, the actor may be spawned on any node in the cluster
	// based on the specified placement strategy. Supported strategies include:
	//   - RoundRobin: Distributes actors evenly across available nodes.
	//   - Random: Choose a node at random.
	//   - Local: Ensures that the actor is created on the local node.
	//   - LeastLoad: Places the actor on the node with the fewest active actors.
	//
	// In non-cluster mode, the actor is created on the local actor system
	// just like with the standard `Spawn` function.
	//
	// Unlike `Spawn`, `SpawnOn` does not return a PID immediately. To interact with
	// the spawned actor, use the `ActorOf` method to resolve its PID after it has been
	// successfully created.
	//
	// Parameters:
	//   - ctx: A context used to control cancellation and timeouts during the spawn process.
	//   - name: A globally unique name for the actor in the cluster.
	//   - actor: An instance implementing the Actor interface.
	//   - opts: Optional SpawnOptions, such as placement strategy or dependencies, mailbox, supervisor strategy.
	//
	// Returns:
	//   - error: An error if the actor could not be spawned (e.g., name conflict,
	//     network failure, or misconfiguration).
	//
	// Example:
	//
	//	err := system.SpawnOn(ctx, "cart-service", NewCartActor(),
	//	    WithPlacementStrategy(ConsistentHash))
	//	if err != nil {
	//	    log.Fatalf("Failed to spawn actor: %v", err)
	//	}
	//
	// Note: The created actor used the default mailbox set during the creation of the actor system.
	SpawnOn(ctx context.Context, name string, actor Actor, opts ...SpawnOption) error
	// SpawnFromFunc creates an actor with the given receive function. One can set the PreStart and PostStop lifecycle hooks
	// in the given optional options
	SpawnFromFunc(ctx context.Context, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error)
	// SpawnNamedFromFunc creates an actor with the given receive function and provided name. One can set the PreStart and PostStop lifecycle hooks
	// in the given optional options
	SpawnNamedFromFunc(ctx context.Context, name string, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error)
	// SpawnRouter creates and initializes a new router actor with the specified options.
	//
	// A router is a special type of actor designed to distribute messages of the same type
	// across a group of routee actors. This enables concurrent message processing and improves
	// throughput by leveraging multiple actors in parallel.
	//
	// Each individual actor in the group (a "routee") processes only one message at a time,
	// preserving the actor model’s single-threaded execution semantics.
	//
	// Note: Routers are **not** redeployable. If the host node of a router leaves the cluster
	// or crashes, the router and its routees will not be automatically re-spawned elsewhere.
	//
	// Use routers when you need to fan out work across multiple workers while preserving
	// the isolation and safety guarantees of the actor model.
	SpawnRouter(ctx context.Context, name string, poolSize int, routeesKind Actor, opts ...RouterOption) (*PID, error)
	// SpawnSingleton creates a singleton actor in the system.
	//
	// A singleton actor is instantiated when cluster mode is enabled.
	// A singleton actor like any other actor is created only once within the system and in the cluster.
	// A singleton actor is created with the default supervisor strategy and directive.
	// A singleton actor once created lives throughout the lifetime of the given actor system.
	//
	// The cluster singleton is automatically started on the oldest node in the cluster.
	// If the oldest node leaves the cluster, the singleton is restarted on the new oldest node.
	// This is useful for managing shared resources or coordinating tasks that should be handled by a single actor.
	SpawnSingleton(ctx context.Context, name string, actor Actor, opts ...ClusterSingletonOption) error
	// Kill stops a given actor in the system either locally or on a remote node(when clustering is enabled)
	Kill(ctx context.Context, name string) error
	// ReSpawn recreates a given actor in the system
	// During restart all messages that are in the mailbox and not yet processed will be ignored.
	// Only the direct alive children of the given actor will be shudown and respawned with their initial state.
	// Bear in mind that restarting an actor will reinitialize the actor to initial state.
	// In case any of the direct child restart fails the given actor will not be started at all.
	ReSpawn(ctx context.Context, name string) (*PID, error)
	// NumActors returns the total number of active actors on a given running node.
	// This does not account for the total number of actors in the cluster
	NumActors() uint64
	// LocalActor returns the reference of a local actor.
	// A local actor is an actor that reside on the same node where the given actor system has started
	LocalActor(actorName string) (*PID, error)
	// RemoteActor returns the address of a remote actor when cluster is enabled
	// When the cluster mode is not enabled an actor not found error will be returned
	// One can always check whether cluster is enabled before calling this method or just use the ActorOf method.
	RemoteActor(ctx context.Context, actorName string) (addr *address.Address, err error)
	// ActorOf retrieves an existing actor within the local system or across the cluster if clustering is enabled.
	//
	// If the actor is found locally, its PID is returned. If the actor resides on a remote host, its address is returned.
	// If the actor is not found, an error of type "actor not found" is returned.
	ActorOf(ctx context.Context, actorName string) (addr *address.Address, pid *PID, err error)
	// ActorExists checks whether an actor with the given name exists in the system,
	// either locally, or on another node in the cluster if clustering is enabled.
	ActorExists(ctx context.Context, actorName string) (exists bool, err error)
	// InCluster states whether the actor system has started within a cluster of nodes
	InCluster() bool
	// GetPartition returns the partition where a given actor is located
	GetPartition(actorName string) uint64
	// Subscribe creates an event subscriber to consume events from the actor system.
	// Remember to use the Unsubscribe method to avoid resource leakage.
	Subscribe() (eventstream.Subscriber, error)
	// Unsubscribe unsubscribes a subscriber.
	Unsubscribe(subscriber eventstream.Subscriber) error
	// ScheduleOnce schedules a one-time delivery of a message to the specified actor (PID) after a given delay.
	//
	// The message will be sent exactly once to the target actor after the specified duration has elapsed.
	// This is a fire-and-forget scheduling mechanism — once delivered, the message will not be retried or repeated.
	//
	// Parameters:
	//	  - ctx: The context for managing cancellation and deadlines.
	//   - message: The proto.Message to be sent.
	//   - pid: The PID of the actor that will receive the message.
	//   - delay: The duration to wait before delivering the message.
	//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
	//
	// Returns:
	//   - error: An error is returned if scheduling fails due to invalid input or internal errors.
	//
	// Note:
	//   - It's strongly recommended to set a unique reference ID using WithReference if you intend to cancel, pause, or resume the message later.
	//   - If no reference is set, an automatic one will be generated, which may not be easily retrievable.
	ScheduleOnce(ctx context.Context, message proto.Message, pid *PID, delay time.Duration, opts ...ScheduleOption) error
	// Schedule schedules a recurring message to be delivered to the specified actor (PID) at a fixed interval.
	//
	// This function sets up a message to be sent repeatedly to the target actor, with each delivery occurring
	// after the specified interval. The scheduling continues until explicitly canceled or if the actor is no longer available.
	//
	// Parameters:
	//	  - ctx: The context for managing cancellation and deadlines.
	//   - message: The proto.Message to be delivered at regular intervals.
	//   - pid: The PID of the actor that will receive the message.
	//   - interval: The time duration between each delivery of the message.
	//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
	//
	// Returns:
	//   - error: An error is returned if the message could not be scheduled due to invalid input or internal issues.
	//
	// Note:
	//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the scheduled message.
	//   - If no reference is set, an automatic one will be generated internally, which may not be easily retrievable for later operations.
	//   - This function does not provide built-in delivery guarantees such as at-least-once or exactly-once semantics; ensure idempotency where needed.
	Schedule(ctx context.Context, message proto.Message, pid *PID, interval time.Duration, opts ...ScheduleOption) error
	// RemoteScheduleOnce schedules a one-time delivery of a message to a remote actor after a specified delay.
	//
	// This method schedules a message to be sent to an actor located at a remote address once the given interval has elapsed.
	// It requires that remoting is enabled in the actor system configuration.
	//
	// Parameters:
	//	  - ctx: The context for managing cancellation and deadlines.
	//   - message: The proto.Message to be delivered.
	//   - receiver: The address.Address of the remote actor that will receive the message.
	//   - delay: The time duration to wait before delivering the message.
	//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
	//
	// Returns:
	//   - error: An error is returned if remoting is not enabled, the target address is invalid, or scheduling fails.
	//
	// Note:
	//   - Remoting must be enabled in the actor system for this function to work.
	//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the message later.
	//   - If no reference is set, an automatic one will be generated internally, which may not be retrievable.
	RemoteScheduleOnce(ctx context.Context, message proto.Message, receiver *address.Address, delay time.Duration, opts ...ScheduleOption) error
	// RemoteSchedule schedules a recurring message to be sent to a remote actor at a specified interval.
	//
	// This method sends the given message repeatedly to the remote actor located at the specified address,
	// with each delivery occurring after the configured time interval.
	// Remoting must be enabled in the actor system for this functionality to work.
	//
	// Parameters:
	//	  - ctx: The context for managing cancellation and deadlines.
	//   - message: The proto.Message to be delivered periodically.
	//   - receiver: The address.Address of the remote actor that will receive the message.
	//   - interval: The time duration between each message delivery.
	//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
	//
	// Returns:
	//   - error: An error is returned if remoting is not enabled, the target address is invalid, or scheduling fails.
	//
	// Note:
	//   - Remoting must be enabled in the actor system for this method to function correctly.
	//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the scheduled message.
	//   - If no reference is set, an automatic one will be generated internally, which may not be easily retrievable for later operations.
	RemoteSchedule(ctx context.Context, message proto.Message, receiver *address.Address, interval time.Duration, opts ...ScheduleOption) error
	// ScheduleWithCron schedules a message to be delivered to the specified actor (PID) using a cron expression.
	//
	// This method enables flexible time-based scheduling using standard cron syntax, allowing you to specify complex recurring schedules.
	// The message will be sent to the target actor according to the schedule defined by the cron expression.
	//
	// Parameters:
	//	  - ctx: The context for managing cancellation and deadlines.
	//   - message: The proto.Message to be delivered.
	//   - pid: The PID of the actor that will receive the message.
	//   - cronExpression: A standard cron-formatted string (e.g., "0 */5 * * * *") representing the schedule.
	//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
	//
	// Returns:
	//   - error: An error is returned if the cron expression is invalid or if scheduling fails due to internal errors.
	//
	// Note:
	//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the scheduled message.
	//   - If no reference is set, an automatic one will be generated internally, which may not be easily retrievable for future operations.
	//   - The cron expression must follow the format supported by the scheduler (typically 6 or 5 fields depending on implementation).
	ScheduleWithCron(ctx context.Context, message proto.Message, pid *PID, cronExpression string, opts ...ScheduleOption) error
	// RemoteScheduleWithCron schedules a message to be sent to a remote actor according to a cron expression.
	//
	// This method allows scheduling messages to remote actors using flexible cron-based timing,
	// enabling complex recurring schedules for message delivery.
	// Remoting must be enabled in the actor system for this method to work.
	//
	// Parameters:
	//	  - ctx: The context for managing cancellation and deadlines.
	//   - message: The proto.Message to be delivered according to the cron schedule.
	//   - receiver: The address.Address of the remote actor that will receive the message.
	//   - cronExpression: A standard cron-formatted string defining the schedule (e.g., "0 0 * * *").
	//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
	//
	// Returns:
	//   - error: An error is returned if the cron expression is invalid, remoting is disabled, or scheduling fails.
	//
	// Note:
	//   - Remoting must be enabled in the actor system for this functionality.
	//   - It's strongly recommended to set a unique reference ID using WithReference if you intend to cancel, pause, or resume the scheduled message.
	//   - If no reference is set, an automatic one will be generated internally and may not be easily retrievable.
	//   - The cron expression must conform to the scheduler’s supported format (usually 5 or 6 fields).
	RemoteScheduleWithCron(ctx context.Context, message proto.Message, receiver *address.Address, cronExpression string, opts ...ScheduleOption) error
	// CancelSchedule cancels a previously scheduled message intended for delivery to a target actor (PID).
	//
	// It attempts to locate and cancel the scheduled task associated with the specified message reference.
	// If the scheduled message cannot be found, has already been delivered, or was previously canceled, an error is returned.
	//
	// Parameters:
	//   - reference: The message reference previously used when scheduling the message
	//
	// Returns:
	//   - error: An error is returned if the scheduled message could not be found or canceled.
	CancelSchedule(reference string) error
	// PauseSchedule pauses a previously scheduled message that was set to be delivered to a target actor (PID).
	//
	// This function temporarily halts the delivery of the scheduled message. It can be resumed later using a corresponding resume mechanism,
	// depending on the scheduler's capabilities. If the message has already been delivered or cannot be found, an error is returned.
	//
	// Parameters:
	//   - reference: The message reference previously used when scheduling the message
	//
	// Returns:
	//   - error: An error is returned if the scheduled message cannot be found, has already been delivered, or cannot be paused.
	PauseSchedule(reference string) error
	// ResumeSchedule resumes a previously paused scheduled message intended for delivery to a target actor (PID).
	//
	// This function reactivates a scheduled message that was previously paused, allowing it to continue toward delivery.
	// If the message has already been delivered, canceled, or cannot be found, an error is returned.
	//
	// Parameters:
	//   - reference: The message reference previously used when scheduling the message
	//
	// Returns:
	//   - error: An error is returned if the scheduled message cannot be found, was never paused, has already been delivered, or cannot be resumed.
	ResumeSchedule(reference string) error
	// PeersAddress returns the actor system address known in the cluster. That address is used by other nodes to communicate with the actor system.
	// This address is empty when cluster mode is not activated
	PeersAddress() string
	// Register registers an actor for future use. This is necessary when creating an actor remotely
	Register(ctx context.Context, actor Actor) error
	// Deregister removes a registered actor from the registry
	Deregister(ctx context.Context, actor Actor) error
	// Logger returns the logger sets when creating the actor system
	Logger() log.Logger
	// Host returns the actor system node host address
	// This is the bind address for remote communication
	Host() string
	// Port returns the actor system node port.
	// This is the bind port for remote communication
	Port() int
	// Uptime is the number of seconds since the actor system started
	Uptime() int64
	// Running returns true when the actor system is running
	Running() bool
	// Run starts the actor system, blocks on the signals channel, and then
	// gracefully shuts the application down.
	// It's designed to make typical applications simple to run.
	// All of Run's functionality is implemented in terms of the exported
	// Start and Stop methods. Applications with more specialized needs
	// can use those methods directly instead of relying on Run.
	Run(ctx context.Context, startHook func(ctx context.Context) error, stopHook func(ctx context.Context) error)
	// TopicActor returns the topic actor, a system-managed actor responsible for handling
	// publish-subscribe (pub-sub) functionality within the actor system.
	//
	// The topic actor maintains a registry of subscribers (actors) per topic and ensures
	// that messages published to a topic are delivered to all registered subscribers.
	//
	// Requirements:
	//   - PubSub mode must be enabled via the WithPubSub() option when initializing the actor system.
	//
	// Cluster Behavior:
	//   - In cluster mode, messages published to a topic on one node are forwarded to other nodes,
	//     but only once per topic actor with active local subscribers. This ensures efficient message
	//     propagation without redundant network traffic.
	//
	// Usage:
	//   system := NewActorSystem(WithPubSub())
	//
	// Returns the actor reference for the topic actor.
	TopicActor() *PID
	// Extensions returns a slice of all registered extensions in the ActorSystem.
	//
	// This allows system-level introspection or iteration over all available extensions.
	// It can be useful for diagnostics, monitoring, or applying configuration across all extensions.
	//
	// Returns:
	//   - []extension.Extension: A list of all registered extensions.
	Extensions() []extension.Extension
	// Extension retrieves a specific registered extension by its unique ID.
	//
	// This method allows actors or system components to access a specific Extension
	// instance that was previously registered using WithExtensions.
	//
	// Parameters:
	//   - extensionID: The unique identifier of the extension to retrieve.
	//
	// Returns:
	//   - extension.Extension: The registered extension with the given ID, or nil if not found.
	Extension(extensionID string) extension.Extension
	// Inject provides a way to register shared dependencies with the ActorSystem.
	//
	// These dependencies — such as clients, services, or repositories — will be injected
	// into actors that declare them as required when using SpawnOptions.
	//
	// This mechanism ensures actors are provisioned consistently with the resources they
	// depend on, even when they're relocated (e.g., during failover or rescheduling in a
	// distributed cluster environment).
	//
	// Actors can retrieve these dependencies via their context, enabling decoupled,
	// testable, and runtime-injected configurations.
	//
	// Returns an error if the actor system has not started.
	Inject(dependencies ...extension.Dependency) error
	// GrainIdentity retrieves or activates a Grain (virtual actor) identified by the given name.
	//
	// This method ensures that a Grain with the specified name exists in the system. If the Grain is not already active,
	// it will be created using the provided factory function. Grains are virtual actors that are automatically managed
	// and can be transparently activated or deactivated based on usage.
	//
	// Parameters:
	//   - ctx: Context for cancellation and timeout control.
	//   - name: The unique name identifying the Grain.
	//   - factory: A function that creates a new Grain instance if activation is required.
	//	 - opts: Optional GrainOptions to customize the Grain's activation behavior (e.g., activation timeout, retries).
	//
	// Returns:
	//   - *GrainIdentity: The identity object representing the located or newly activated Grain.
	//   - error: An error if the Grain could not be found, created, or activated.
	//
	// Note:
	//   - This method abstracts away the details of Grain lifecycle management.
	//   - Use this to obtain a reference to a Grain for message passing or further operations.
	GrainIdentity(ctx context.Context, name string, factory GrainFactory, opts ...GrainOption) (*GrainIdentity, error)
	// AskGrain sends a synchronous request message to a Grain (virtual actor) identified by the given identity.
	//
	// This method locates or activates the target Grain (locally or in the cluster), sends the provided
	// protobuf message, and waits for a response or error. The request will block until a response is received,
	// the context is canceled, or the timeout elapses.
	//
	// Parameters:
	//   - ctx: Context for cancellation and timeout control.
	//   - identity: The unique identity of the Grain.
	//   - message: The protobuf message to send to the Grain.
	//   - timeout: The maximum duration to wait for a response.
	//
	// Returns:
	//   - response: The response message from the Grain, if successful.
	//   - error: An error if the request fails, times out, or the system is not started.
	AskGrain(ctx context.Context, identity *GrainIdentity, message proto.Message, timeout time.Duration) (response proto.Message, err error)
	// TellGrain sends an asynchronous message to a Grain (virtual actor) identified by the given identity.
	//
	// This method locates or activates the target Grain (locally or in the cluster) and delivers the provided
	// protobuf message without waiting for a response. Use this for fire-and-forget scenarios where no reply is expected.
	//
	// Parameters:
	//   - ctx: Context for cancellation and timeout control.
	//   - identity: The unique identity of the Grain.
	//   - message: The protobuf message to send to the Grain.
	//
	// Returns:
	//   - error: An error if the message could not be delivered or the system is not started.
	TellGrain(ctx context.Context, identity *GrainIdentity, message proto.Message) error
	// Grains retrieves a list of all active Grains (virtual actors) in the system.
	//
	// Grains are virtual actors that are automatically managed by the actor system. This method returns a slice of
	// GrainIdentity objects representing the currently active Grains. In cluster mode, it attempts to aggregate Grains
	// across all nodes in the cluster; if the cluster request fails, only locally active Grains will be returned.
	//
	// Use this method with caution, as scanning for all Grains (especially in a large cluster) may impact system performance.
	// The timeout parameter defines the maximum duration for cluster-based requests before they are terminated.
	//
	// Parameters:
	//   - ctx: Context for cancellation and timeout control.
	//   - timeout: The maximum duration to wait for cluster-based queries.
	//
	// Returns:
	//   - []*GrainIdentity: A slice of GrainIdentity objects for all active Grains.
	//
	// Note:
	//   - This method abstracts away the details of Grain lifecycle management.
	//   - Use this to obtain references to all active Grains for monitoring, diagnostics, or administrative purposes.
	Grains(ctx context.Context, timeout time.Duration) []*GrainIdentity
	// NoSender returns a special PID that represents an anonymous / absent sender.
	//
	// Use this PID when sending or scheduling messages for which no sender is expected. The PID
	// is meaningful only for local messaging and is not routable across the network.
	//
	// In remote scenarios use address.NoSender(), which encodes the appropriate
	// network address semantics for a no-sender value.
	//
	// Notes:
	//  - The returned PID should be used as the Sender in envelopes, not as a target
	//    destination for Send operations.
	//  - The value is stable for local use and intended to explicitly indicate the
	//    absence of a sender (as opposed to nil).
	NoSender() *PID
	// PeersPort returns the port known in the cluster.
	// That port is used by other nodes to communicate with this actor system.
	// This port is zero when cluster mode is not activated
	PeersPort() int
	// DiscoveryPort returns the port used for service discovery.
	// This port is zero when cluster mode is not activated
	DiscoveryPort() int
	// Peers returns a best-effort snapshot of currently known peer nodes in the cluster.
	//
	// Behavior:
	//   - Requires cluster mode. If clustering is disabled, an empty slice is returned
	//     and ErrClusterDisabled may be reported depending on the implementation.
	//   - Returns an eventually consistent view of membership as observed by this node.
	//     The result can be stale or incomplete while membership information propagates.
	//   - If the lookup exceeds the provided timeout or fails, a partial snapshot may
	//     be returned (possibly empty) along with a non-nil error.
	//
	// Concurrency and Ordering:
	//   - The returned slice is a point-in-time snapshot. Cluster membership can change
	//     immediately after the call returns.
	//   - The order of peers is unspecified and should not be relied upon.
	//
	// Parameters:
	//   - ctx: Context for cancellation and deadline control.
	//   - timeout: Maximum duration allowed for cluster membership lookup.
	//
	// Returns:
	//   - []*remote.Peer: Zero or more peer descriptors known to this node at call time.
	//   - error: May be non-nil if the lookup timed out, was canceled, or another error occurred.
	//
	// Possible Errors:
	//   - ErrActorSystemNotStarted: The actor system has not been started.
	//   - ErrClusterDisabled: Clustering is not enabled for this node.
	//   - context.DeadlineExceeded: The operation exceeded the supplied timeout.
	//   - context.Canceled: The context was canceled.
	//   - Other transient network/storage errors from the underlying cluster engine.
	//
	// Example:
	//   ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	//   defer cancel()
	//
	//   peers, err := system.Peers(ctx, 2*time.Second)
	//   if err != nil {
	//       system.Logger().Warnf("peer lookup returned an error: %v", err)
	//   }
	//   for _, p := range peers {
	//       // Use p (e.g., p.Address(), p.Host(), p.Port(), etc.)
	//   }
	Peers(ctx context.Context, timeout time.Duration) ([]*remote.Peer, error)
	// IsLeader returns true if the current node is the cluster leader.
	//
	// Behavior:
	//   - Requires cluster mode. If clustering is disabled, false is returned
	//     along with ErrClusterDisabled.
	//   - The leader is determined by the underlying cluster engine and may change
	//     over time as nodes join/leave or failures occur.
	//
	// Parameters:
	//   - ctx: Context for cancellation and deadline control.
	//
	// Returns:
	//   - bool: True if this node is the current cluster leader; false otherwise.
	//   - error: May be non-nil if clustering is disabled or another error occurred.
	//
	// Possible Errors:
	//   - ErrActorSystemNotStarted: The actor system has not been started.
	//   - ErrClusterDisabled: Clustering is not enabled for this node.
	//   - context.Canceled: The context was canceled.
	//   - Other transient errors from the underlying cluster engine.
	//
	// Example:
	//   ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	//   defer cancel()
	//
	//   isLeader, err := system.IsLeader(ctx)
	//   if err != nil {
	//       system.Logger().Errorf("failed to determine cluster leader status: %v", err)
	//   } else if isLeader {
	//       system.Logger().Info("this node is the cluster leader")
	//   } else {
	//       system.Logger().Info("this node is not the cluster leader")
	//   }
	IsLeader(ctx context.Context) (bool, error)
	// Leader returns the current cluster leader peer information.
	//
	// Behavior:
	//   - Requires cluster mode. If clustering is disabled, nil is returned
	//     along with ErrClusterDisabled.
	//   - The leader is determined by the underlying cluster engine and may change
	//     over time as nodes join/leave or failures occur.
	//
	// Parameters:
	//   - ctx: Context for cancellation and deadline control.
	//
	// Returns:
	//   - *remote.Peer: The current cluster leader peer, or nil if not available.
	//   - error: May be non-nil if clustering is disabled or another error occurred.
	//
	// Possible Errors:
	//   - ErrActorSystemNotStarted: The actor system has not been started.
	//   - ErrClusterDisabled: Clustering is not enabled for this node.
	//   - context.Canceled: The context was canceled.
	//   - Other transient errors from the underlying cluster engine.
	// Example:
	//   ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	//   defer cancel()
	//
	//   leader, err := system.Leader(ctx)
	//   if err != nil {
	//       system.Logger().Errorf("failed to retrieve cluster leader: %v", err)
	//   } else if leader != nil {
	//       system.Logger().Infof("current cluster leader is at %s", leader.Address())
	//   } else {
	//       system.Logger().Info("no cluster leader is currently elected")
	//   }
	Leader(ctx context.Context) (leader *remote.Peer, err error)
	// RegisterGrainKind registers a Grain kind in the local registry.
	//
	// Registration associates the Grain's kind (as returned by the Grain implementation) with the
	// factory/metadata used by the actor system to instantiate that kind on demand.
	//
	// This is required for:
	//   - Remote activation/recreation: when another node asks this node to activate a Grain of a given kind.
	//   - Lazy/local activation: when a GrainIdentity is resolved locally via kind lookup.
	RegisterGrainKind(ctx context.Context, kind Grain) error
	// DeregisterGrainKind removes a previously registered Grain kind from the local registry.
	//
	// Deregistration affects future activations that rely on kind lookup (e.g. remote activation/recreation
	// requests and lazy/local activation when a GrainIdentity is resolved by kind). It does not stop or
	// deactivate already-running Grain instances of that kind; it only prevents new activations via the
	// registry.
	DeregisterGrainKind(ctx context.Context, kind Grain) error
	// handleRemoteAsk handles a synchronous message to another actor and expect a response.
	// This block until a response is received or timed out.
	handleRemoteAsk(ctx context.Context, to *PID, message proto.Message, timeout time.Duration) (response proto.Message, err error)
	// handleRemoteTell handles an asynchronous message to an actor
	handleRemoteTell(ctx context.Context, to *PID, message proto.Message) error
	// putActorOnCluster sets actor in the actor system actors registry
	putActorOnCluster(actor *PID) error
	// reservedName returns reserved actor's name
	reservedName(nameType nameType) string
	// getCluster returns the cluster engine
	getCluster() cluster.Cluster
	// tree returns the actors tree
	tree() *tree

	completeRelocation()
	getRootGuardian() *PID
	getSystemGuardian() *PID
	getUserGuardian() *PID
	getDeathWatch() *PID
	getDeadletter() *PID
	getSingletonManager() *PID
	getRelocator() *PID
	getReflection() *reflection
	findRoutee(routeeName string) (*PID, bool)
	isStopping() bool
	getRemoting() remote.Remoting
	getGrains() *xsync.Map[string, *grainPID]
	recreateGrain(ctx context.Context, props *internalpb.Grain) error
	decreaseActorsCounter()
	increaseActorsCounter()
	passivationManager() *passivationManager
	removeNodeLeft(address string)
	getClusterStore() cluster.Store
	getDataCenterController() *datacentercontroller.Controller
}

// ActorSystem represent a collection of actors on a given node
// Only a single instance of the ActorSystem can be created on a given node
type actorSystem struct {
	_ locker.NoCopy
	// hold the actors tree in the system
	actors *tree

	// states whether the actor system has started or not
	started  atomic.Bool
	starting atomic.Bool

	// Specifies the actor system name
	name string
	// Specifies the logger to use in the system
	logger log.Logger

	// Specifies how long the sender of a message should wait to receive a reply
	// when using SendReply. The default value is 5s
	askTimeout time.Duration
	// Specifies the shutdown timeout. The default value is 3mn
	shutdownTimeout time.Duration
	// Specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	actorInitMaxRetries int
	// Specifies the actors initialization timeout
	// The default value is 1s
	actorInitTimeout time.Duration

	// Specifies whether remoting is enabled.
	// This allows to handle remote messaging
	remotingEnabled atomic.Bool
	remoting        remote.Remoting

	// Specifies the remoting server
	server       *stdhttp.Server
	listener     net.Listener
	remoteConfig *remote.Config

	// cluster settings
	clusterEnabled  atomic.Bool
	cluster         cluster.Cluster
	actorsQueue     chan *internalpb.Actor
	eventsQueue     <-chan *cluster.Event
	partitionHasher hash.Hasher
	clusterNode     *discovery.Node

	// help protect some the fields to set
	locker sync.RWMutex

	// specifies the events stream
	eventsStream eventstream.Stream

	// specifies the message scheduler
	scheduler *scheduler

	// manages passivation deadlines without per-actor goroutines
	passivator *passivationManager

	defaultSupervisor          *sup.Supervisor
	defaultPassivationStrategy passivation.Strategy

	registry   registry.Registry
	reflection *reflection

	clusterConfig    *ClusterConfig
	rebalancingQueue chan *internalpb.PeerState
	rebalancedNodes  goset.Set[string]

	relocator        *PID
	rootGuardian     *PID
	userGuardian     *PID
	systemGuardian   *PID
	deathWatch       *PID
	deadletter       *PID
	singletonManager *PID
	topicActor       *PID
	noSender         *PID
	peerStatesWriter *PID

	startedAt        atomic.Int64
	relocating       atomic.Bool
	relocatingLocker sync.Mutex
	shutdownHooks    []ShutdownHook

	actorsCounter      atomic.Uint64
	deadlettersCounter atomic.Uint64

	tlsInfo           *gtls.Info
	pubsubEnabled     atomic.Bool
	relocationEnabled atomic.Bool
	extensions        *xsync.Map[string, extension.Extension]

	shuttingDown     atomic.Bool
	grainsQueue      chan *internalpb.Grain
	grains           *xsync.Map[string, *grainPID]
	grainBarrier     *grainActivationBarrier
	grainActivation  singleflight.Group
	evictionStrategy *EvictionStrategy
	evictionInterval time.Duration
	evictionStopSig  chan types.Unit

	metricProvider *metric.Provider

	clusterStore cluster.Store

	dataCenterController        *datacentercontroller.Controller
	dataCenterControllerMutex   sync.Mutex
	dataCenterLeaderTicker      *ticker.Ticker
	dataCenterLeaderStopWatch   chan types.Unit
	dataCenterLeaderMutex       sync.Mutex
	dataCenterReconcileInFlight atomic.Bool
}

var (
	// enforce compilation error when all methods of the ActorSystem interface are not implemented
	// by the struct actorSystem
	_ ActorSystem                              = (*actorSystem)(nil)
	_ internalpbconnect.RemotingServiceHandler = (*actorSystem)(nil)
	_ internalpbconnect.ClusterServiceHandler  = (*actorSystem)(nil)
)

// NewActorSystem creates and configures a new ActorSystem instance.
//
// The actor system is the root container for all actors on a node. Only one ActorSystem
// can exist per process. In cluster mode, the system name must be identical across all nodes.
//
// Options allow customization of logging, clustering, remoting, pub/sub, TLS, extensions, and more.
// The returned ActorSystem is not started; use Start or Run to initialize it.
//
// Parameters:
//   - name: Unique name for the actor system (required; must match across cluster nodes).
//   - opts: Optional configuration options (see WithLogger, WithCluster, WithPubSub, etc).
//
// Returns:
//   - ActorSystem: The configured actor system instance.
//   - error: If the name is invalid, options are misconfigured, or required settings are missing.
//
// Example:
//
//	system, err := NewActorSystem("my-system", WithLogger(myLogger), WithCluster(clusterConfig))
//	if err != nil {
//	    log.Fatalf("Failed to create actor system: %v", err)
//	}
//	if err := system.Start(ctx); err != nil {
//	    log.Fatalf("Failed to start actor system: %v", err)
//	}
//
// Notes:
//   - The actor system must be started before spawning actors.
//   - In cluster mode, ensure all nodes use the same system name and compatible options.
//   - Use Run for typical applications to handle signals
func NewActorSystem(name string, opts ...Option) (ActorSystem, error) {
	if name == "" {
		return nil, gerrors.ErrNameRequired
	}
	if match, _ := regexp.MatchString("^[a-zA-Z0-9][a-zA-Z0-9-_]*$", name); !match {
		return nil, gerrors.ErrInvalidActorSystemName
	}

	system := &actorSystem{
		actorsQueue:         make(chan *internalpb.Actor, 10),
		name:                name,
		logger:              log.New(log.ErrorLevel, os.Stderr),
		actorInitMaxRetries: DefaultInitMaxRetries,
		shutdownTimeout:     DefaultShutdownTimeout,
		eventsStream:        eventstream.New(),
		partitionHasher:     hash.DefaultHasher(),
		actorInitTimeout:    DefaultInitTimeout,
		eventsQueue:         make(chan *cluster.Event, 1),
		registry:            registry.NewRegistry(),
		remoteConfig:        remote.DefaultConfig(),
		actors:              newTree(),
		shutdownHooks:       make([]ShutdownHook, 0),
		rebalancedNodes:     goset.NewSet[string](),
		topicActor:          nil,
		extensions:          xsync.NewMap[string, extension.Extension](),
		grainsQueue:         make(chan *internalpb.Grain, 10),
		grains:              xsync.NewMap[string, *grainPID](),
		askTimeout:          DefaultAskTimeout,
		evictionStopSig:     make(chan types.Unit, 1),
	}

	system.startedAt.Store(0)
	system.relocating.Store(false)
	system.actorsCounter.Store(0)
	system.deadlettersCounter.Store(0)
	system.shuttingDown.Store(false)
	system.started.Store(false)
	system.starting.Store(false)
	system.relocationEnabled.Store(true)
	system.started.Store(false)
	system.remotingEnabled.Store(false)
	system.clusterEnabled.Store(false)
	system.pubsubEnabled.Store(false)

	system.reflection = newReflection(system.registry)
	system.defaultSupervisor = sup.NewSupervisor()
	system.defaultPassivationStrategy = passivation.NewTimeBasedStrategy(DefaultPassivationTimeout)
	system.passivator = newPassivationManager(system.logger)

	// apply the various options
	for _, opt := range opts {
		opt.Apply(system)
	}

	if err := system.validate(); err != nil {
		return nil, err
	}

	return system, nil
}

// Run starts the actor system, blocks on the signals channel, and then
// gracefully shuts the application down and terminate the running processing.
// It's designed to make typical applications simple to run.
// The minimal GoAkt application looks like this:
//
//	NewActorSystem(name, opts).Run(ctx, startHook, stopHook)
//
// All of Run's functionality is implemented in terms of the exported
// Start and Stop methods. Applications with more specialized needs
// can use those methods directly instead of relying on Run.
func (x *actorSystem) Run(ctx context.Context, startHook func(ctx context.Context) error, stophook func(ctx context.Context) error) {
	if err := chain.
		New(chain.WithFailFast(), chain.WithContext(ctx)).
		AddContextRunner(startHook).
		AddContextRunner(x.Start).
		Run(); err != nil {
		x.logger.Fatal(err)
		os.Exit(1)
		return
	}

	// wait for interruption/termination
	notifier := make(chan os.Signal, 1)
	done := make(chan types.Unit, 1)
	signal.Notify(notifier, syscall.SIGINT, syscall.SIGTERM)

	// wait for a shutdown signal, and then shutdown
	go func() {
		sig := <-notifier
		x.logger.Infof("Received an interrupt signal (%s) to shutdown", sig.String())

		if err := chain.
			New(chain.WithFailFast(), chain.WithContext(ctx)).
			AddContextRunner(stophook).
			AddContextRunner(x.Stop).
			Run(); err != nil {
			x.logger.Fatal(err)
		}

		signal.Stop(notifier)
		done <- types.Unit{}
	}()
	<-done
	pid := os.Getpid()
	// make sure if it is unix init process to exit
	if pid == 1 {
		os.Exit(0)
	}

	process, _ := os.FindProcess(pid)
	switch runtime.GOOS {
	case "windows":
		_ = process.Kill()
	default:
		_ = process.Signal(syscall.SIGTERM)
	}
}

// Start initializes the actor system.
// To guarantee a clean shutdown during unexpected system terminations,
// developers must handle SIGTERM and SIGINT signals appropriately and invoke Stop.
func (x *actorSystem) Start(ctx context.Context) error {
	// make sure we don't start the actor system twice
	if x.started.Load() {
		return gerrors.ErrActorSystemAlreadyStarted
	}

	x.logger.Infof("Starting Actor System (%s) on %s/%s..", x.name, runtime.GOOS, runtime.GOARCH)
	x.starting.Store(true)

	if err := chain.
		New(chain.WithFailFast(), chain.WithContext(ctx)).
		AddRunner(x.setupRemoting).
		AddRunner(x.setupCluster).
		AddContextRunner(x.spawnRootGuardian).
		AddContextRunner(x.spawnSystemGuardian).
		AddContextRunner(x.spawnNoSender).
		AddContextRunner(x.spawnUserGuardian).
		AddContextRunner(x.spawnDeathWatch).
		AddContextRunner(x.spawnDeadletter).
		AddContextRunner(x.spawnSingletonManager).
		AddContextRunner(x.spawnRelocator).
		AddContextRunner(x.spawnTopicActor).
		AddContextRunner(x.startRemoting).
		AddContextRunner(x.startClustering).
		AddContextRunner(x.startDataCenterController).
		AddContextRunner(x.startDataCenterLeaderWatch).
		Run(); err != nil {
		if stopErr := x.shutdown(ctx); stopErr != nil {
			return errors.Join(err, stopErr)
		}

		return err
	}

	x.startMessagesScheduler(ctx)
	if x.passivator != nil {
		x.passivator.Start(ctx)
	}
	x.startEviction()
	x.started.Store(true)
	x.starting.Store(false)
	x.startedAt.Store(time.Now().Unix())

	if err := x.registerMetrics(); err != nil {
		x.logger.Errorf("Failed to register actor system metrics: %v", err)
		if stopErr := x.shutdown(ctx); stopErr != nil {
			return errors.Join(err, stopErr)
		}

		return err
	}

	x.logger.Infof("Actor System (%s) successfully started..:)", x.name)
	return nil
}

// Stop stops the actor system and does not terminate the program.
// One needs to explicitly call os.Exit to terminate the program.
func (x *actorSystem) Stop(ctx context.Context) error {
	return x.shutdown(ctx)
}

// Metric returns the actor system metrics.
// The metrics does not include any cluster data
func (x *actorSystem) Metric(ctx context.Context) *Metric {
	if x.started.Load() {
		x.getSetDeadlettersCount(ctx)
		// we ignore the error here
		memSize, _ := memory.Size()
		memAvail, _ := memory.Free()
		memUsed := memory.Used()

		return &Metric{
			deadlettersCount: int64(x.deadlettersCounter.Load()),
			actorsCount:      int64(x.actorsCounter.Load()),
			uptime:           x.Uptime(),
			memSize:          memSize,
			memAvail:         memAvail,
			memUsed:          memUsed,
		}
	}
	return nil
}

// Running returns true when the actor system is running
func (x *actorSystem) Running() bool {
	return x.started.Load()
}

// Uptime returns the number of seconds since the actor system started
func (x *actorSystem) Uptime() int64 {
	if x.started.Load() {
		return time.Now().Unix() - x.startedAt.Load()
	}
	return 0
}

// Host returns the actor system node host address
// This is the bind address for remote communication
func (x *actorSystem) Host() string {
	x.locker.RLock()
	host := x.remoteConfig.BindAddr()
	x.locker.RUnlock()
	return host
}

// Port returns the actor system node port.
// This is the bind port for remote communication
func (x *actorSystem) Port() int {
	x.locker.RLock()
	port := x.remoteConfig.BindPort()
	x.locker.RUnlock()
	return port
}

// Peers returns a best-effort snapshot of currently known peer nodes in the cluster.
//
// Behavior:
//   - Requires cluster mode. If clustering is disabled, an empty slice is returned
//     and ErrClusterDisabled may be reported depending on the implementation.
//   - Returns an eventually consistent view of membership as observed by this node.
//     The result can be stale or incomplete while membership information propagates.
//   - If the lookup exceeds the provided timeout or fails, a partial snapshot may
//     be returned (possibly empty) along with a non-nil error.
//
// Concurrency and Ordering:
//   - The returned slice is a point-in-time snapshot. Cluster membership can change
//     immediately after the call returns.
//   - The order of peers is unspecified and should not be relied upon.
//
// Parameters:
//   - ctx: Context for cancellation and deadline control.
//   - timeout: Maximum duration allowed for cluster membership lookup.
//
// Returns:
//   - []*remote.Peer: Zero or more peer descriptors known to this node at call time.
//   - error: May be non-nil if the lookup timed out, was canceled, or another error occurred.
//
// Possible Errors:
//   - ErrActorSystemNotStarted: The actor system has not been started.
//   - ErrClusterDisabled: Clustering is not enabled for this node.
//   - context.DeadlineExceeded: The operation exceeded the supplied timeout.
//   - context.Canceled: The context was canceled.
//   - Other transient network/storage errors from the underlying cluster engine.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
//	defer cancel()
//
//	peers, err := system.Peers(ctx, 2*time.Second)
//	if err != nil {
//	    system.Logger().Warnf("peer lookup returned an error: %v", err)
//	}
//	for _, p := range peers {
//	    // Use p (e.g., p.Address(), p.Host(), p.Port(), etc.)
//	}
func (x *actorSystem) Peers(ctx context.Context, timeout time.Duration) ([]*remote.Peer, error) {
	if !x.Running() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	if !x.clusterEnabled.Load() {
		return nil, gerrors.ErrClusterDisabled
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	peers, err := x.cluster.Peers(ctx)
	if err != nil {
		return nil, err
	}

	return cluster.ToRemotePeers(peers), nil
}

// IsLeader returns true if the current node is the cluster leader.
//
// Behavior:
//   - Requires cluster mode. If clustering is disabled, false is returned
//     along with ErrClusterDisabled.
//   - The leader is determined by the underlying cluster engine and may change
//     over time as nodes join/leave or failures occur.
//
// Parameters:
//   - ctx: Context for cancellation and deadline control.
//
// Returns:
//   - bool: True if this node is the current cluster leader; false otherwise.
//   - error: May be non-nil if clustering is disabled or another error occurred.
//
// Possible Errors:
//   - ErrActorSystemNotStarted: The actor system has not been started.
//   - ErrClusterDisabled: Clustering is not enabled for this node.
//   - context.Canceled: The context was canceled.
//   - Other transient errors from the underlying cluster engine.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
//	defer cancel()
//
//	isLeader, err := system.IsLeader(ctx)
//	if err != nil {
//	    system.Logger().Errorf("failed to determine cluster leader status: %v", err)
//	} else if isLeader {
//	    system.Logger().Info("this node is the cluster leader")
//	} else {
//	    system.Logger().Info("this node is not the cluster leader")
//	}
func (x *actorSystem) IsLeader(ctx context.Context) (bool, error) {
	if !x.Running() {
		return false, gerrors.ErrActorSystemNotStarted
	}

	if !x.clusterEnabled.Load() {
		return false, gerrors.ErrClusterDisabled
	}

	return x.cluster.IsLeader(ctx), nil
}

// Leader returns the current cluster leader peer information.
//
// Behavior:
//   - Requires cluster mode. If clustering is disabled, nil is returned
//     along with ErrClusterDisabled.
//   - The leader is determined by the underlying cluster engine and may change
//     over time as nodes join/leave or failures occur.
//
// Parameters:
//   - ctx: Context for cancellation and deadline control.
//
// Returns:
//   - *remote.Peer: The current cluster leader peer, or nil if not available.
//   - error: May be non-nil if clustering is disabled or another error occurred.
//
// Possible Errors:
//   - ErrActorSystemNotStarted: The actor system has not been started.
//   - ErrClusterDisabled: Clustering is not enabled for this node.
//   - context.Canceled: The context was canceled.
//   - Other transient errors from the underlying cluster engine.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
//	defer cancel()
//
//	leader, err := system.Leader(ctx)
//	if err != nil {
//	    system.Logger().Errorf("failed to retrieve cluster leader: %v", err)
//	} else if leader != nil {
//	    system.Logger().Infof("current cluster leader is at %s", leader.Address())
//	} else {
//	    system.Logger().Info("no cluster leader is currently elected")
//	}
func (x *actorSystem) Leader(ctx context.Context) (leader *remote.Peer, err error) {
	if !x.Running() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	if !x.clusterEnabled.Load() {
		return nil, gerrors.ErrClusterDisabled
	}

	members, err := x.cluster.Members(ctx)
	if err != nil {
		return nil, err
	}

	sort.Slice(members, func(i int, j int) bool {
		return members[i].CreatedAt < members[j].CreatedAt
	})

	leader = cluster.ToRemotePeer(members[0])
	return leader, nil
}

// DiscoveryPort returns the port used for service discovery.
// This port is zero when cluster mode is not activated
func (x *actorSystem) DiscoveryPort() int {
	if !x.InCluster() {
		return 0
	}
	x.locker.RLock()
	port := x.clusterNode.DiscoveryPort
	x.locker.RUnlock()
	return port
}

// PeersPort returns the port known in the cluster.
// That port is used by other nodes to communicate with this actor system.
// This port is zero when cluster mode is not activated
func (x *actorSystem) PeersPort() int {
	if !x.InCluster() {
		return 0
	}
	x.locker.RLock()
	port := x.clusterNode.PeersPort
	x.locker.RUnlock()
	return port
}

// Logger returns the logger sets when creating the actor system
func (x *actorSystem) Logger() log.Logger {
	x.locker.RLock()
	logger := x.logger
	x.locker.RUnlock()
	return logger
}

// Deregister removes a registered actor from the registry
func (x *actorSystem) Deregister(_ context.Context, actor Actor) error {
	if !x.Running() {
		return gerrors.ErrActorSystemNotStarted
	}

	x.locker.Lock()
	x.registry.Deregister(actor)
	x.locker.Unlock()
	return nil
}

// Register registers an actor for future use. This is necessary when creating an actor remotely
func (x *actorSystem) Register(_ context.Context, actor Actor) error {
	if !x.Running() {
		return gerrors.ErrActorSystemNotStarted
	}

	x.locker.Lock()
	x.registry.Register(actor)
	x.locker.Unlock()
	return nil
}

// Schedule schedules a recurring message to be delivered to the specified actor (PID) at a fixed interval.
//
// This function sets up a message to be sent repeatedly to the target actor, with each delivery occurring
// after the specified interval. The scheduling continues until explicitly canceled or if the actor is no longer available.
//
// Parameters:
//   - ctx: The context for managing cancellation and deadlines.
//   - message: The proto.Message to be delivered at regular intervals.
//   - pid: The PID of the actor that will receive the message.
//   - interval: The time duration between each delivery of the message.
//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
//
// Returns:
//   - error: An error is returned if the message could not be scheduled due to invalid input or internal issues.
//
// Note:
//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the scheduled message.
//   - If no reference is set, an automatic one will be generated internally, which may not be easily retrievable for later operations.
//   - This function does not provide built-in delivery guarantees such as at-least-once or exactly-once semantics; ensure idempotency where needed.
func (x *actorSystem) Schedule(_ context.Context, message proto.Message, pid *PID, interval time.Duration, opts ...ScheduleOption) error {
	return x.scheduler.Schedule(message, pid, interval, opts...)
}

// RemoteSchedule schedules a recurring message to be sent to a remote actor at a specified interval.
//
// This method sends the given message repeatedly to the remote actor located at the specified address,
// with each delivery occurring after the configured time interval.
// Remoting must be enabled in the actor system for this functionality to work.
//
// Parameters:
//   - ctx: The context for managing cancellation and deadlines.
//   - message: The proto.Message to be delivered periodically.
//   - to: The address.Address of the remote actor that will receive the message.
//   - interval: The time duration between each message delivery.
//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
//
// Returns:
//   - error: An error is returned if remoting is not enabled, the target address is invalid, or scheduling fails.
//
// Note:
//   - Remoting must be enabled in the actor system for this method to function correctly.
//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the scheduled message.
//   - If no reference is set, an automatic one will be generated internally, which may not be easily retrievable for later operations.
func (x *actorSystem) RemoteSchedule(_ context.Context, message proto.Message, receiver *address.Address, interval time.Duration, opts ...ScheduleOption) error {
	return x.scheduler.RemoteSchedule(message, receiver, interval, opts...)
}

// ScheduleOnce schedules a one-time delivery of a message to the specified actor (PID) after a given delay.
//
// The message will be sent exactly once to the target actor after the specified duration has elapsed.
// This is a fire-and-forget scheduling mechanism — once delivered, the message will not be retried or repeated.
//
// Parameters:
//   - ctx: The context for managing cancellation and deadlines.
//   - message: The proto.Message to be sent.
//   - pid: The PID of the actor that will receive the message.
//   - delay: The duration to wait before delivering the message.
//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
//
// Returns:
//   - error: An error is returned if scheduling fails due to invalid input or internal errors.
//
// Note:
//   - It's strongly recommended to set a unique reference ID using WithReference if you intend to cancel, pause, or resume the message later.
//   - If no reference is set, an automatic one will be generated, which may not be easily retrievable.
func (x *actorSystem) ScheduleOnce(_ context.Context, message proto.Message, pid *PID, interval time.Duration, opts ...ScheduleOption) error {
	return x.scheduler.ScheduleOnce(message, pid, interval, opts...)
}

// RemoteScheduleOnce schedules a one-time delivery of a message to a remote actor after a specified delay.
//
// This method schedules a message to be sent to an actor located at a remote address once the given interval has elapsed.
// It requires that remoting is enabled in the actor system configuration.
//
// Parameters:
//   - ctx: The context for managing cancellation and deadlines.
//   - message: The proto.Message to be delivered.
//   - to: The address.Address of the remote actor that will receive the message.
//   - delay: The time duration to wait before delivering the message.
//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
//
// Returns:
//   - error: An error is returned if remoting is not enabled, the target address is invalid, or scheduling fails.
//
// Note:
//   - Remoting must be enabled in the actor system for this function to work.
//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the message later.
//   - If no reference is set, an automatic one will be generated internally, which may not be retrievable.
func (x *actorSystem) RemoteScheduleOnce(_ context.Context, message proto.Message, receiver *address.Address, interval time.Duration, opts ...ScheduleOption) error {
	return x.scheduler.RemoteScheduleOnce(message, receiver, interval, opts...)
}

// ScheduleWithCron schedules a message to be delivered to the specified actor (PID) using a cron expression.
//
// This method enables flexible time-based scheduling using standard cron syntax, allowing you to specify complex recurring schedules.
// The message will be sent to the target actor according to the schedule defined by the cron expression.
//
// Parameters:
//   - ctx: The context for managing cancellation and deadlines.
//   - message: The proto.Message to be delivered.
//   - pid: The PID of the actor that will receive the message.
//   - cronExpression: A standard cron-formatted string (e.g., "0 */5 * * * *") representing the schedule.
//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
//
// Returns:
//   - error: An error is returned if the cron expression is invalid or if scheduling fails due to internal errors.
//
// Note:
//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the scheduled message.
//   - If no reference is set, an automatic one will be generated internally, which may not be easily retrievable for future operations.
//   - The cron expression must follow the format supported by the scheduler (typically 6 or 5 fields depending on implementation).
func (x *actorSystem) ScheduleWithCron(_ context.Context, message proto.Message, pid *PID, cronExpression string, opts ...ScheduleOption) error {
	return x.scheduler.ScheduleWithCron(message, pid, cronExpression, opts...)
}

// RemoteScheduleWithCron schedules a message to be sent to a remote actor according to a cron expression.
//
// This method allows scheduling messages to remote actors using flexible cron-based timing,
// enabling complex recurring schedules for message delivery.
// Remoting must be enabled in the actor system for this method to work.
//
// Parameters:
//   - ctx: The context for managing cancellation and deadlines.
//   - message: The proto.Message to be delivered according to the cron schedule.
//   - to: The address.Address of the remote actor that will receive the message.
//   - cronExpression: A standard cron-formatted string defining the schedule (e.g., "0 0 * * *").
//   - opts: Optional ScheduleOption values such as WithReference to control scheduling behavior.
//
// Returns:
//   - error: An error is returned if the cron expression is invalid, remoting is disabled, or scheduling fails.
//
// Note:
//   - Remoting must be enabled in the actor system for this functionality.
//   - It's strongly recommended to set a unique reference ID using WithReference if you intend to cancel, pause, or resume the scheduled message.
//   - If no reference is set, an automatic one will be generated internally and may not be easily retrievable.
//   - The cron expression must conform to the scheduler’s supported format (usually 5 or 6 fields).
func (x *actorSystem) RemoteScheduleWithCron(_ context.Context, message proto.Message, receiver *address.Address, cronExpression string, opts ...ScheduleOption) error {
	return x.scheduler.RemoteScheduleWithCron(message, receiver, cronExpression, opts...)
}

// CancelSchedule cancels a previously scheduled message intended for delivery to a target actor.
//
// It attempts to locate and cancel the scheduled task associated with the specified message reference.
// If the scheduled message cannot be found, has already been delivered, or was previously canceled, an error is returned.
//
// Parameters:
//   - reference: The message reference previously used when scheduling the message
//
// Returns:
//   - error: An error is returned if the scheduled message could not be found or canceled.
func (x *actorSystem) CancelSchedule(reference string) error {
	return x.scheduler.CancelSchedule(reference)
}

// PauseSchedule pauses a previously scheduled message that was set to be delivered to a target actor.
//
// This function temporarily halts the delivery of the scheduled message. It can be resumed later using a corresponding resume mechanism,
// depending on the scheduler's capabilities. If the message has already been delivered or cannot be found, an error is returned.
//
// Parameters:
//   - reference: The message reference previously used when scheduling the message
//
// Returns:
//   - error: An error is returned if the scheduled message cannot be found, has already been delivered, or cannot be paused.
func (x *actorSystem) PauseSchedule(reference string) error {
	return x.scheduler.PauseSchedule(reference)
}

// ResumeSchedule resumes a previously paused scheduled message intended for delivery to a target actor.
//
// This function reactivates a scheduled message that was previously paused, allowing it to continue toward delivery.
// If the message has already been delivered, canceled, or cannot be found, an error is returned.
//
// Parameters:
//   - reference: The message reference previously used when scheduling the message
//
// Returns:
//   - error: An error is returned if the scheduled message cannot be found, was never paused, has already been delivered, or cannot be resumed.
func (x *actorSystem) ResumeSchedule(reference string) error {
	return x.scheduler.ResumeSchedule(reference)
}

// Subscribe creates an event subscriber to consume events from the actor system.
// Remember to use the Unsubscribe method to avoid resource leakage.
func (x *actorSystem) Subscribe() (eventstream.Subscriber, error) {
	if !x.Running() {
		return nil, gerrors.ErrActorSystemNotStarted
	}
	x.locker.Lock()
	subscriber := x.eventsStream.AddSubscriber()
	x.eventsStream.Subscribe(subscriber, eventsTopic)
	x.locker.Unlock()
	return subscriber, nil
}

// Unsubscribe unsubscribes a subscriber.
func (x *actorSystem) Unsubscribe(subscriber eventstream.Subscriber) error {
	if !x.Running() {
		return gerrors.ErrActorSystemNotStarted
	}
	x.locker.Lock()
	x.eventsStream.Unsubscribe(subscriber, eventsTopic)
	x.eventsStream.RemoveSubscriber(subscriber)
	x.locker.Unlock()
	return nil
}

// GetPartition returns the partition where a given actor is located
func (x *actorSystem) GetPartition(actorName string) uint64 {
	if x.InCluster() {
		return x.cluster.GetPartition(actorName)
	}
	return uint64(0)
}

// InCluster states whether the actor system is started within a cluster of nodesMap
func (x *actorSystem) InCluster() bool {
	return x.started.Load() &&
		x.clusterEnabled.Load() &&
		x.cluster != nil
}

// NumActors returns the total number of active actors on a given running node.
// This does not account for the total number of actors in the cluster
func (x *actorSystem) NumActors() uint64 {
	return x.actorsCounter.Load()
}

// Kill stops a given actor in the system
func (x *actorSystem) Kill(ctx context.Context, name string) error {
	if !x.Running() {
		return gerrors.ErrActorSystemNotStarted
	}

	// user should not query system actors
	if isSystemName(name) {
		return gerrors.NewErrActorNotFound(name)
	}

	pidNode, exist := x.actors.nodeByName(name)
	if exist {
		pid := pidNode.value()
		return pid.Shutdown(ctx)
	}

	if x.InCluster() {
		actor, err := x.cluster.GetActor(ctx, name)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				x.logger.Warnf("Actor %s not found", name)
				return gerrors.NewErrActorNotFound(name)
			}
			return fmt.Errorf("failed to fetch remote actor=%s: %w", name, err)
		}

		addr, err := address.Parse(actor.GetAddress())
		if err != nil {
			return fmt.Errorf("failed to parse remote actor=%s address=%s: %w", name, actor.GetAddress(), err)
		}

		return x.getRemoting().RemoteStop(ctx, addr.Host(), addr.Port(), name)
	}

	return gerrors.NewErrActorNotFound(name)
}

// ReSpawn recreates a given actor in the system
// During restart all messages that are in the mailbox and not yet processed will be ignored.
// Only the direct alive children of the given actor will be shudown and respawned with their initial state.
// Bear in mind that restarting an actor will reinitialize the actor to initial state.
// In case any of the direct child restart fails the given actor will not be started at all.
func (x *actorSystem) ReSpawn(ctx context.Context, name string) (*PID, error) {
	if !x.Running() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	// user should not query system actors
	if isSystemName(name) {
		return nil, gerrors.NewErrActorNotFound(name)
	}

	node, exist := x.actors.nodeByName(name)
	if exist {
		pid := node.value()
		if err := pid.Restart(ctx); err != nil {
			return nil, fmt.Errorf("failed to restart actor=%s: %w", pid.ID(), err)
		}
		return pid, nil
	}

	return nil, gerrors.NewErrActorNotFound(name)
}

// Name returns the actor system name
func (x *actorSystem) Name() string {
	x.locker.RLock()
	name := x.name
	x.locker.RUnlock()
	return name
}

// Actors returns the list of Actors that are alive on a given running node.
// This does not account for the total number of actors in the cluster
func (x *actorSystem) Actors() []*PID {
	x.locker.RLock()
	nodes := x.actors.nodes()
	x.locker.RUnlock()
	var actors []*PID
	for _, node := range nodes {
		pid := node.value()
		if !isSystemName(pid.Name()) {
			actors = append(actors, pid)
		}
	}
	return actors
}

// ActorRefs retrieves a list of active actors, including both local actors
// and, when cluster mode is enabled, actors across the cluster. Use this
// method cautiously, as the scanning process may impact system performance.
// If the cluster request fails, an error is returned.
// The timeout parameter defines the maximum duration for cluster-based requests
// before they are terminated.
func (x *actorSystem) ActorRefs(ctx context.Context, timeout time.Duration) ([]ActorRef, error) {
	pids := x.Actors()
	actorRefs := make([]ActorRef, len(pids))
	uniques := make(map[string]types.Unit)
	for index, pid := range pids {
		actorRefs[index] = fromPID(pid)
		uniques[pid.Address().String()] = types.Unit{}
	}

	if x.InCluster() {
		actors, err := x.getCluster().Actors(ctx, timeout)
		if err != nil {
			return nil, err
		}

		for _, actor := range actors {
			actorRef := toActorRef(actor)
			if _, ok := uniques[actorRef.Address().String()]; !ok {
				actorRefs = append(actorRefs, actorRef)
			}
		}
	}

	return actorRefs, nil
}

// PeerAddress returns the actor system address known in the cluster. That address is used by other nodesMap to communicate with the actor system.
// This address is empty when cluster mode is not activated
func (x *actorSystem) PeersAddress() string {
	if x.clusterEnabled.Load() {
		x.locker.RLock()
		address := x.clusterNode.PeersAddress()
		x.locker.RUnlock()
		return address
	}
	return ""
}

// ActorOf retrieves an existing actor within the local system or across the cluster if clustering is enabled.
//
// If the actor is found locally, its PID is returned. If the actor resides on a remote host, its address is returned.
// If the actor is not found, an error of type "actor not found" is returned.
func (x *actorSystem) ActorOf(ctx context.Context, actorName string) (addr *address.Address, pid *PID, err error) {
	if !x.Running() {
		return nil, nil, gerrors.ErrActorSystemNotStarted
	}

	x.locker.RLock()
	// user should not query system actors
	if isSystemName(actorName) {
		x.locker.RUnlock()
		return nil, nil, gerrors.NewErrActorNotFound(actorName)
	}

	// first check whether the actor exist locally
	if pidnode, ok := x.actors.nodeByName(actorName); ok {
		pid := pidnode.value()
		x.locker.RUnlock()
		if pid.IsStopping() {
			return nil, nil, gerrors.NewErrActorNotFound(actorName)
		}
		return pid.Address(), pid, nil
	}

	// check in the cluster
	if x.clusterEnabled.Load() {
		actor, err := x.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				x.logger.Warnf("Actor %s not found", actorName)
				x.locker.RUnlock()
				return nil, nil, gerrors.NewErrActorNotFound(actorName)
			}

			x.locker.RUnlock()
			return nil, nil, fmt.Errorf("failed to fetch remote actor=%s: %w", actorName, err)
		}

		x.locker.RUnlock()
		addr, err := address.Parse(actor.GetAddress())
		return addr, nil, err
	}

	if x.remotingEnabled.Load() {
		x.locker.RUnlock()
		return nil, nil, gerrors.ErrMethodCallNotAllowed
	}

	x.logger.Warnf("Actor %s not found", actorName)
	x.locker.RUnlock()
	return nil, nil, gerrors.NewErrActorNotFound(actorName)
}

// ActorExists checks whether an actor with the given name exists in the system,
// either locally, or on another node in the cluster if clustering is enabled.
func (x *actorSystem) ActorExists(ctx context.Context, actorName string) (bool, error) {
	if !x.Running() {
		return false, gerrors.ErrActorSystemNotStarted
	}

	x.locker.RLock()
	defer x.locker.RUnlock()

	// check locally
	if node, ok := x.actors.nodeByName(actorName); ok {
		pid := node.value()
		if pid.IsStopping() {
			return false, nil
		}
		return true, nil
	}

	// check in the cluster
	if x.clusterEnabled.Load() {
		return x.cluster.ActorExists(ctx, actorName)
	}

	return false, nil
}

// LocalActor returns the reference of a local actor.
// A local actor is an actor that reside on the same node where the given actor system has started
func (x *actorSystem) LocalActor(actorName string) (*PID, error) {
	if !x.Running() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	x.locker.RLock()
	// user should not query system actors
	if isSystemName(actorName) {
		x.locker.RUnlock()
		return nil, gerrors.NewErrActorNotFound(actorName)
	}

	if pidnode, ok := x.actors.nodeByName(actorName); ok {
		pid := pidnode.value()
		x.locker.RUnlock()
		return pid, nil
	}

	x.logger.Warnf("Actor %s not found", actorName)
	x.locker.RUnlock()
	return nil, gerrors.NewErrActorNotFound(actorName)
}

// RemoteActor returns the address of a remote actor when cluster is enabled
// When the cluster mode is not enabled an actor not found error will be returned
// One can always check whether cluster is enabled before calling this method or just use the ActorOf method.
func (x *actorSystem) RemoteActor(ctx context.Context, actorName string) (addr *address.Address, err error) {
	if !x.Running() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	x.locker.RLock()
	// user should not query system actors
	if isSystemName(actorName) {
		x.locker.RUnlock()
		return nil, gerrors.NewErrActorNotFound(actorName)
	}

	if x.cluster == nil {
		x.locker.RUnlock()
		return nil, gerrors.ErrClusterDisabled
	}

	actor, err := x.cluster.GetActor(ctx, actorName)
	if err != nil {
		if errors.Is(err, cluster.ErrActorNotFound) {
			x.logger.Warnf("Actor %s not found", actorName)
			x.locker.RUnlock()
			return nil, gerrors.NewErrActorNotFound(actorName)
		}

		x.locker.RUnlock()
		return nil, fmt.Errorf("failed to fetch remote actor=%s: %w", actorName, err)
	}

	x.locker.RUnlock()
	return address.Parse(actor.GetAddress())
}

// RemoteLookup for an actor on a remote host.
func (x *actorSystem) RemoteLookup(ctx context.Context, request *connect.Request[internalpb.RemoteLookupRequest]) (*connect.Response[internalpb.RemoteLookupResponse], error) {
	logger := x.logger
	msg := request.Msg

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, gerrors.ErrInvalidHost)
	}

	actorName := msg.GetName()
	if !isSystemName(actorName) && x.clusterEnabled.Load() {
		actor, err := x.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				err := gerrors.NewErrAddressNotFound(actorName)
				logger.Error(err.Error())
				return nil, err
			}

			return nil, connect.NewError(connect.CodeInternal, err)
		}
		return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: actor.GetAddress()}), nil
	}

	addr := address.New(actorName, x.Name(), msg.GetHost(), int(msg.GetPort()))
	pidNode, exist := x.actors.node(addr.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(addr.String())
		logger.Error(err.Error())
		return nil, err
	}

	pid := pidNode.value()
	return connect.NewResponse(&internalpb.RemoteLookupResponse{Address: pid.ID()}), nil
}

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately. With this type of message the receiver cannot communicate back to Sender
// except reply the message with a response. This one-way communication
func (x *actorSystem) RemoteAsk(ctx context.Context, request *connect.Request[internalpb.RemoteAskRequest]) (*connect.Response[internalpb.RemoteAskResponse], error) {
	logger := x.logger

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrRemotingDisabled)
	}

	req := request.Msg
	timeout := x.askTimeout
	if req.GetTimeout() != nil {
		timeout = req.GetTimeout().AsDuration()
	}

	if propagator := x.remoteConfig.ContextPropagator(); propagator != nil {
		var err error
		ctx, err = propagator.Extract(ctx, request.Header())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}

	responses := make([]*anypb.Any, 0, len(req.GetRemoteMessages()))
	for _, message := range req.GetRemoteMessages() {
		receiver := message.GetReceiver()
		addr, err := address.Parse(receiver)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}

		remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
		if remoteAddr != net.JoinHostPort(addr.Host(), strconv.Itoa(addr.Port())) {
			return nil, connect.NewError(connect.CodeInvalidArgument, gerrors.ErrInvalidHost)
		}

		node, exist := x.actors.node(addr.String())
		if !exist {
			err := gerrors.NewErrAddressNotFound(addr.String())
			logger.Error(err.Error())
			return nil, err
		}

		pid := node.value()
		if !pid.IsRunning() {
			err := gerrors.NewErrRemoteSendFailure(gerrors.ErrDead)
			logger.Error(err.Error())
			return nil, err
		}

		reply, err := x.handleRemoteAsk(ctx, pid, message, timeout)
		if err != nil {
			err := gerrors.NewErrRemoteSendFailure(err)
			logger.Error(err.Error())
			return nil, err
		}

		marshaled, _ := anypb.New(reply)
		responses = append(responses, marshaled)
	}

	return connect.NewResponse(&internalpb.RemoteAskResponse{Messages: responses}), nil
}

// RemoteTell is used to send a message to an actor remotely by another actor
func (x *actorSystem) RemoteTell(ctx context.Context, request *connect.Request[internalpb.RemoteTellRequest]) (*connect.Response[internalpb.RemoteTellResponse], error) {
	logger := x.logger

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrRemotingDisabled)
	}

	req := request.Msg

	if propagator := x.remoteConfig.ContextPropagator(); propagator != nil {
		var err error
		ctx, err = propagator.Extract(ctx, request.Header())
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}

	for _, message := range req.GetRemoteMessages() {
		receiver := message.GetReceiver()
		addr, err := address.Parse(receiver)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}

		node, exist := x.actors.node(addr.String())
		if !exist {
			err := gerrors.NewErrAddressNotFound(addr.String())
			logger.Error(err)
			return nil, err
		}

		pid := node.value()
		if !pid.IsRunning() {
			err := gerrors.NewErrRemoteSendFailure(gerrors.ErrDead)
			logger.Error(err.Error())
			return nil, err
		}

		if err := x.handleRemoteTell(ctx, pid, message); err != nil {
			err := gerrors.NewErrRemoteSendFailure(err)
			logger.Error(err)
			return nil, err
		}
	}

	return connect.NewResponse(new(internalpb.RemoteTellResponse)), nil
}

// RemoteReSpawn is used the handle the re-creation of an actor from a remote host or from an api call
func (x *actorSystem) RemoteReSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteReSpawnRequest]) (*connect.Response[internalpb.RemoteReSpawnResponse], error) {
	logger := x.logger

	msg := request.Msg

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, gerrors.ErrInvalidHost)
	}

	// make sure we don't interfere with system actors.
	if isSystemName(msg.GetName()) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.NewErrActorNotFound(msg.GetName()))
	}

	actorAddress := address.New(msg.GetName(), x.Name(), msg.GetHost(), int(msg.GetPort()))
	node, exist := x.actors.node(actorAddress.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(actorAddress.String())
		logger.Error(err)
		return nil, err
	}

	pid := node.value()
	if err := pid.Restart(ctx); err != nil {
		return nil, fmt.Errorf("failed to restart actor=%s: %w", actorAddress.String(), err)
	}

	return connect.NewResponse(new(internalpb.RemoteReSpawnResponse)), nil
}

// RemoteStop stops an actor on a remote machine
func (x *actorSystem) RemoteStop(ctx context.Context, request *connect.Request[internalpb.RemoteStopRequest]) (*connect.Response[internalpb.RemoteStopResponse], error) {
	logger := x.logger

	msg := request.Msg

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, gerrors.ErrInvalidHost)
	}

	// make sure we don't interfere with system actors.
	if isSystemName(msg.GetName()) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.NewErrActorNotFound(msg.GetName()))
	}

	actorAddress := address.New(msg.GetName(), x.Name(), msg.GetHost(), int(msg.GetPort()))
	pidNode, exist := x.actors.node(actorAddress.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(actorAddress.String())
		logger.Error(err.Error())
		return nil, err
	}

	pid := pidNode.value()
	if err := pid.Shutdown(ctx); err != nil {
		return nil, fmt.Errorf("failed to stop actor=%s: %w", actorAddress.String(), err)
	}

	return connect.NewResponse(new(internalpb.RemoteStopResponse)), nil
}

// RemoteSpawn handles the remoteSpawn call
func (x *actorSystem) RemoteSpawn(ctx context.Context, request *connect.Request[internalpb.RemoteSpawnRequest]) (*connect.Response[internalpb.RemoteSpawnResponse], error) {
	logger := x.logger

	msg := request.Msg
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, gerrors.ErrInvalidHost)
	}

	// make sure we don't interfere with system actors.
	if isSystemName(msg.GetActorName()) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.NewErrActorNotFound(msg.GetActorName()))
	}

	actor, err := x.reflection.instantiateActor(msg.GetActorType())
	if err != nil {
		logger.Errorf(
			"Failed to create Actor [(%s) of type (%s)] on [host=%s, port=%d]: reason: (%v)",
			msg.GetActorName(), msg.GetActorType(), msg.GetHost(), msg.GetPort(), err,
		)

		if errors.Is(err, gerrors.ErrTypeNotRegistered) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrTypeNotRegistered)
		}

		return nil, connect.NewError(connect.CodeInternal, err)
	}

	wrapSpawnErr := func(err error) error {
		if errors.Is(err, gerrors.ErrActorAlreadyExists) || errors.Is(err, gerrors.ErrSingletonAlreadyExists) {
			return connect.NewError(connect.CodeAlreadyExists, err)
		}
		if cluster.IsQuorumError(err) {
			return connect.NewError(connect.CodeUnavailable, cluster.NormalizeQuorumError(err))
		}
		return connect.NewError(connect.CodeInternal, err)
	}

	if msg.GetSingleton() != nil {
		// define singleton options
		singletonOpts := []ClusterSingletonOption{
			WithSingletonSpawnTimeout(msg.GetSingleton().GetSpawnTimeout().AsDuration()),
			WithSingletonSpawnWaitInterval(msg.GetSingleton().GetWaitInterval().AsDuration()),
			WithSingletonSpawnRetries(int(msg.GetSingleton().GetMaxRetries())),
		}

		if msg.GetRole() != "" {
			singletonOpts = append(singletonOpts, WithSingletonRole(msg.GetRole()))
		}

		if err := x.SpawnSingleton(ctx, msg.GetActorName(), actor, singletonOpts...); err != nil {
			logger.Errorf("Failed to create Actor (%s) on [host=%s, port=%d]: reason: (%v)", msg.GetActorName(), msg.GetHost(), msg.GetPort(), err)
			return nil, wrapSpawnErr(err)
		}

		logger.Infof("Actor (%s) successfully created on [host=%s, port=%d]", msg.GetActorName(), msg.GetHost(), msg.GetPort())
		return connect.NewResponse(new(internalpb.RemoteSpawnResponse)), nil
	}

	opts := []SpawnOption{
		WithPassivationStrategy(codec.DecodePassivationStrategy(msg.GetPassivationStrategy())),
	}

	if !msg.GetRelocatable() {
		opts = append(opts, WithRelocationDisabled())
	}

	if msg.GetEnableStash() {
		opts = append(opts, WithStashing())
	}

	if msg.GetReentrancy() != nil {
		reentrancy := codec.DecodeReentrancy(msg.GetReentrancy())
		opts = append(opts, WithReentrancy(reentrancy))
	}

	if msg.GetRole() != "" {
		opts = append(opts, WithRole(msg.GetRole()))
	}

	if msg.GetSupervisor() != nil {
		if decoded := codec.DecodeSupervisor(msg.GetSupervisor()); decoded != nil {
			opts = append(opts, WithSupervisor(decoded))
		}
	}

	// set the dependencies if any
	if len(msg.GetDependencies()) > 0 {
		dependencies, err := x.reflection.dependenciesFromProto(msg.GetDependencies()...)
		if err != nil {
			logger.Errorf("Failed to create Actor (%s) on [host=%s, port=%d]: reason: (%v)", msg.GetActorName(), msg.GetHost(), msg.GetPort(), err)
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		opts = append(opts, WithDependencies(dependencies...))
	}

	if _, err = x.Spawn(ctx, msg.GetActorName(), actor, opts...); err != nil {
		logger.Errorf("Failed to create Actor (%s) on [host=%s, port=%d]: reason: (%v)", msg.GetActorName(), msg.GetHost(), msg.GetPort(), err)
		return nil, wrapSpawnErr(err)
	}

	logger.Infof("Actor (%s) successfully created on [host=%s, port=%d]", msg.GetActorName(), msg.GetHost(), msg.GetPort())
	return connect.NewResponse(new(internalpb.RemoteSpawnResponse)), nil
}

// RemoteReinstate handles the remoteReinstate call
func (x *actorSystem) RemoteReinstate(_ context.Context, request *connect.Request[internalpb.RemoteReinstateRequest]) (*connect.Response[internalpb.RemoteReinstateResponse], error) {
	logger := x.logger

	msg := request.Msg

	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrRemotingDisabled)
	}

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != net.JoinHostPort(msg.GetHost(), strconv.Itoa(int(msg.GetPort()))) {
		return nil, connect.NewError(connect.CodeInvalidArgument, gerrors.ErrInvalidHost)
	}

	// make sure we don't interfere with system actors.
	if isSystemName(msg.GetName()) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.NewErrActorNotFound(msg.GetName()))
	}

	actorAddress := address.New(msg.GetName(), x.Name(), msg.GetHost(), int(msg.GetPort()))
	node, exist := x.actors.node(actorAddress.String())
	if !exist {
		err := gerrors.NewErrAddressNotFound(actorAddress.String())
		logger.Error(err.Error())
		return nil, err
	}

	pid := node.value()
	pid.doReinstate()

	return connect.NewResponse(new(internalpb.RemoteReinstateResponse)), nil
}

// GetNodeMetric handles the GetNodeMetric request send the given node
func (x *actorSystem) GetNodeMetric(_ context.Context, request *connect.Request[internalpb.GetNodeMetricRequest]) (*connect.Response[internalpb.GetNodeMetricResponse], error) {
	if !x.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrClusterDisabled)
	}

	req := request.Msg

	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if remoteAddr != req.GetNodeAddress() {
		return nil, connect.NewError(connect.CodeInvalidArgument, gerrors.ErrInvalidHost)
	}

	load := x.actorsCounter.Load() + uint64(x.grains.Len())
	return connect.NewResponse(
		&internalpb.GetNodeMetricResponse{
			NodeAddress: remoteAddr,
			Load:        load,
		},
	), nil
}

// GetKinds returns the cluster kinds
func (x *actorSystem) GetKinds(_ context.Context, request *connect.Request[internalpb.GetKindsRequest]) (*connect.Response[internalpb.GetKindsResponse], error) {
	if !x.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrClusterDisabled)
	}

	req := request.Msg
	remoteAddr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())

	// routine check
	if remoteAddr != req.GetNodeAddress() {
		return nil, connect.NewError(connect.CodeInvalidArgument, gerrors.ErrInvalidHost)
	}

	kinds := make([]string, len(x.clusterConfig.kinds.Values()))
	for i, kind := range x.clusterConfig.kinds.Values() {
		kinds[i] = registry.Name(kind)
	}

	return connect.NewResponse(&internalpb.GetKindsResponse{Kinds: kinds}), nil
}

// PersistPeerState persists the peer state for a given node that is gracefully leaving the cluster
func (x *actorSystem) PersistPeerState(ctx context.Context, request *connect.Request[internalpb.PersistPeerStateRequest]) (*connect.Response[internalpb.PersistPeerStateResponse], error) {
	if !x.clusterEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrClusterDisabled)
	}

	peerAddr := fmt.Sprintf("%s:%d", request.Msg.GetPeerState().GetHost(), request.Msg.GetPeerState().GetPeersPort())
	x.logger.Infof("Node (%s) is persisting its Peer (%s) state", x.PeersAddress(), peerAddr)

	if err := x.clusterStore.PersistPeerState(ctx, request.Msg.GetPeerState()); err != nil {
		x.logger.Errorf("Node (%s) failed to persist Peer (%s) state: %v", x.PeersAddress(), peerAddr, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(new(internalpb.PersistPeerStateResponse)), nil
}

// TopicActor returns the topic actor, a system-managed actor responsible for handling
// publish-subscribe (pub-sub) functionality within the actor system.
//
// The topic actor maintains a registry of subscribers (actors) per topic and ensures
// that messages published to a topic are delivered to all registered subscribers.
//
// Requirements:
//   - PubSub mode must be enabled via the WithPubSub() option when initializing the actor system.
//
// Cluster Behavior:
//   - In cluster mode, messages published to a topic on one node are forwarded to other nodes,
//     but only once per topic actor with active local subscribers. This ensures efficient message
//     propagation without redundant network traffic.
//
// Usage:
//
//	system := NewActorSystem(WithPubSub())
//
// Returns the actor reference for the topic actor.
func (x *actorSystem) TopicActor() *PID {
	x.locker.RLock()
	topicActor := x.topicActor
	x.locker.RUnlock()
	return topicActor
}

// Extensions returns a slice of all registered extensions in the ActorSystem.
//
// This allows system-level introspection or iteration over all available extensions.
// It can be useful for diagnostics, monitoring, or applying configuration across all extensions.
//
// Returns:
//   - []extension.Extension: A list of all registered extensions.
func (x *actorSystem) Extensions() []extension.Extension {
	return x.extensions.Values()
}

// Extension retrieves a specific registered extension by its unique ID.
//
// This method allows actors or system components to access a specific Extension
// instance that was previously registered using WithExtensions.
//
// Parameters:
//   - extensionID: The unique identifier of the extension to retrieve.
//
// Returns:
//   - extension.Extension: The registered extension with the given ID, or nil if not found.
func (x *actorSystem) Extension(extensionID string) extension.Extension {
	if ext, ok := x.extensions.Get(extensionID); ok {
		return ext
	}
	return nil
}

// Inject provides a way to register shared dependencies with the ActorSystem.
//
// These dependencies — such as clients, services, or repositories — will be injected
// into actors that declare them as required when using SpawnOptions.
//
// This mechanism ensures actors are provisioned consistently with the resources they
// depend on, even when they're relocated (e.g., during failover or rescheduling in a
// distributed cluster environment).
//
// Actors can retrieve these dependencies via their context, enabling decoupled,
// testable, and runtime-injected configurations.
//
// Returns an error if the actor system has not started.
func (x *actorSystem) Inject(dependencies ...extension.Dependency) error {
	x.locker.Lock()

	if !x.started.Load() {
		x.locker.Unlock()
		return gerrors.ErrActorSystemNotStarted
	}

	for _, dependency := range dependencies {
		x.registry.Register(dependency)
	}
	x.locker.Unlock()
	return nil
}

// String implements fmt.Stringer and returns a human-readable identifier for the
// actor system node.
//
// The format is:
//
//	Node[name=<name>, remoteAddr=<host:port>, peersAddr=<peers>]
//
// remoteAddr is constructed from Host() and Port(), and peersAddr is the value
// returned by PeersAddress(). The output is intended for logging and debugging
// only and should not be relied upon as a stable, machine-parsed format.
//
// Example:
//
//	Node[name=alpha, remoteAddr=127.0.0.1:8080, peersAddr=10.0.0.2:8080]
//
// Note: peersAddr may be empty if the node has no known peers.
func (x *actorSystem) String() string {
	return fmt.Sprintf("[name=%s, remoteAddr=%s, peersAddr=%s]", x.name,
		net.JoinHostPort(x.Host(), strconv.Itoa(x.Port())),
		x.PeersAddress())
}

// validate checks the actor system configuration and ensures that all required settings are properly defined.
func (x *actorSystem) validate() error {
	// validate extensions when defined
	if x.extensions.Len() > 0 {
		if err := x.validateExtensions(); err != nil {
			return err
		}
	}

	if err := x.remoteConfig.Sanitize(); err != nil {
		return err
	}

	// we need to make sure the cluster kinds are defined
	if x.clusterEnabled.Load() {
		if err := x.clusterConfig.Validate(); err != nil {
			return err
		}
	}

	if x.tlsInfo != nil {
		// we need both server and client TLS configurations
		// to be defined when TLS is enabled
		if x.tlsInfo.ServerConfig == nil || x.tlsInfo.ClientConfig == nil {
			return gerrors.ErrInvalidTLSConfiguration
		}
	}

	return nil
}

// handleRemoteAsk handles a synchronous message to another actor and expect a response.
// This block until a response is received or timed out.
func (x *actorSystem) handleRemoteAsk(ctx context.Context, to *PID, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	receiveContext, err := toReceiveContext(ctx, x.NoSender(), to, message, false)
	if err != nil {
		return nil, err
	}
	msg := receiveContext.Message()
	sender := receiveContext.Sender()

	responseCh := receiveContext.response
	if responseCh != nil {
		defer func() {
			// Mark closed so a late Response won't write into a pooled channel that may
			// be reused by another Ask call.
			receiveContext.responseClosed.Store(true)
			putResponseChannel(responseCh)
		}()
	}
	to.doReceive(receiveContext)
	timer := timers.Get(timeout)

	// await patiently to receive the response from the actor
	// or wait for the context to be done
	select {
	case response = <-responseCh:
		timers.Put(timer)
		return
	case <-ctx.Done():
		err = errors.Join(ctx.Err(), gerrors.ErrRequestTimeout)
		to.handleReceivedErrorWithMessage(sender, msg, err)
		timers.Put(timer)
		return nil, err
	case <-timer.C:
		err = gerrors.ErrRequestTimeout
		to.handleReceivedErrorWithMessage(sender, msg, err)
		timers.Put(timer)
		return
	}
}

// handleRemoteTell handles an asynchronous message to an actor
func (x *actorSystem) handleRemoteTell(ctx context.Context, to *PID, message proto.Message) error {
	receiveContext, err := toReceiveContext(ctx, x.NoSender(), to, message, true)
	if err != nil {
		return err
	}

	to.doReceive(receiveContext)
	return nil
}

// getRootGuardian returns the system rootGuardian guardian
func (x *actorSystem) getRootGuardian() *PID {
	x.locker.RLock()
	rootGuardian := x.rootGuardian
	x.locker.RUnlock()
	return rootGuardian
}

// getUserGuardian returns the user guardian
func (x *actorSystem) getUserGuardian() *PID {
	x.locker.RLock()
	userGuardian := x.userGuardian
	x.locker.RUnlock()
	return userGuardian
}

// getSystemGuardian returns the system guardian
func (x *actorSystem) getSystemGuardian() *PID {
	x.locker.RLock()
	systemGuardian := x.systemGuardian
	x.locker.RUnlock()
	return systemGuardian
}

// getJanitor returns the system deathWatch
func (x *actorSystem) getDeathWatch() *PID {
	x.locker.RLock()
	pid := x.deathWatch
	x.locker.RUnlock()
	return pid
}

// getDeadletters returns the system deadletter actor
func (x *actorSystem) getDeadletter() *PID {
	x.locker.RLock()
	deadletters := x.deadletter
	x.locker.RUnlock()
	return deadletters
}

// getReflection returns the system reflection
func (x *actorSystem) getReflection() *reflection {
	x.locker.RLock()
	r := x.reflection
	x.locker.RUnlock()
	return r
}

// findRoutee searches for a routee by its name within the actor system and returns its PID if found or an error otherwise.
func (x *actorSystem) findRoutee(routeeName string) (*PID, bool) {
	x.locker.RLock()
	if pidnode, ok := x.actors.nodeByName(routeeName); ok {
		pid := pidnode.value()
		x.locker.RUnlock()
		return pid, true
	}
	x.locker.RLock()
	return nil, false
}

// isStopping checks whether the actor system is shutting down
func (x *actorSystem) isStopping() bool {
	return x.shuttingDown.Load()
}

func (x *actorSystem) decreaseActorsCounter() {
	x.actorsCounter.Dec()
}

func (x *actorSystem) increaseActorsCounter() {
	x.actorsCounter.Inc()
}

// getRemoting returns the remoting instance of the actor system
// This method is used internally to access the remoting functionality
// and is not intended for external use.
func (x *actorSystem) getRemoting() remote.Remoting {
	x.locker.RLock()
	remoting := x.remoting
	x.locker.RUnlock()
	return remoting
}

// getDataCenterController returns the data center controller
func (x *actorSystem) getDataCenterController() *datacentercontroller.Controller {
	x.locker.RLock()
	dataCenterController := x.dataCenterController
	x.locker.RUnlock()
	return dataCenterController
}

func (x *actorSystem) getClusterStore() cluster.Store {
	x.locker.RLock()
	store := x.clusterStore
	x.locker.RUnlock()
	return store
}

// getGrains returns the grains map of the actor system
func (x *actorSystem) getGrains() *xsync.Map[string, *grainPID] {
	x.locker.RLock()
	grains := x.grains
	x.locker.RUnlock()
	return grains
}

// getSingletonManager returns the system singleton manager
func (x *actorSystem) getSingletonManager() *PID {
	x.locker.RLock()
	singletonManager := x.singletonManager
	x.locker.RUnlock()
	return singletonManager
}

// getRelocator returns the relocator PID
func (x *actorSystem) getRelocator() *PID {
	x.locker.RLock()
	rebalancer := x.relocator
	x.locker.RUnlock()
	return rebalancer
}

func (x *actorSystem) completeRelocation() {
	x.relocating.Store(false)
}

// putActorOnCluster broadcast the newly (re)spawned actor into the cluster
func (x *actorSystem) putActorOnCluster(pid *PID) error {
	if x.clusterEnabled.Load() {
		actor, err := pid.toWireActor()
		if err != nil {
			return err
		}
		x.actorsQueue <- actor
	}
	return nil
}

// putGrainOnCluster broadcast the newly (re)activated grain into the cluster
func (x *actorSystem) putGrainOnCluster(pid *grainPID) error {
	if x.clusterEnabled.Load() {
		grain, err := pid.toWireGrain()
		if err != nil {
			return err
		}

		x.grainsQueue <- grain
	}
	return nil
}

// setupCluster prepares the cluster engine when clustering is enabled
func (x *actorSystem) setupCluster() error {
	if !x.clusterEnabled.Load() {
		return nil
	}

	x.logger.Info("Enabling clustering...")

	if !x.remotingEnabled.Load() {
		x.logger.Error("Remoting must be enabled to use clustering")
		return errors.New("clustering needs remoting to be enabled")
	}

	x.clusterNode = &discovery.Node{
		Name:          x.name,
		Host:          x.remoteConfig.BindAddr(),
		DiscoveryPort: x.clusterConfig.discoveryPort,
		PeersPort:     x.clusterConfig.peersPort,
		RemotingPort:  x.remoteConfig.BindPort(),
		Roles:         x.clusterConfig.getRoles(),
	}

	x.cluster = cluster.New(
		x.name,
		x.clusterConfig.discovery,
		x.clusterNode,
		cluster.WithLogger(x.logger),
		cluster.WithShardCount(x.clusterConfig.partitionCount),
		cluster.WithPartitioner(x.partitionHasher),
		cluster.WithMinimumMembersQuorum(x.clusterConfig.minimumPeersQuorum),
		cluster.WithMembersWriteQuorum(x.clusterConfig.writeQuorum),
		cluster.WithMembersReadQuorum(x.clusterConfig.readQuorum),
		cluster.WithReplicasCount(x.clusterConfig.replicaCount),
		cluster.WithTLS(x.tlsInfo),
		cluster.WithWriteTimeout(x.clusterConfig.writeTimeout),
		cluster.WithReadTimeout(x.clusterConfig.readTimeout),
		cluster.WithShutdownTimeout(x.clusterConfig.shutdownTimeout),
		cluster.WithDataTableSize(x.clusterConfig.tableSize),
		cluster.WithBootstrapTimeout(x.clusterConfig.bootstrapTimeout),
		cluster.WithRoutingTableInterval(x.clusterConfig.clusterStateSyncInterval),
		cluster.WithBalancerInterval(x.clusterConfig.clusterBalancerInterval),
	)

	var err error
	x.clusterStore, err = cluster.NewBoltStore()
	if err != nil {
		x.logger.Errorf("Failed to initialize cluster store: %v", err)
		return err
	}

	for _, kind := range x.clusterConfig.kinds.Values() {
		x.registry.Register(kind)
		x.logger.Infof("Kind (%s) registered", registry.Name(kind))
	}

	grains := x.clusterConfig.grains.Values()
	if len(grains) > 0 {
		for _, grain := range grains {
			x.registry.Register(grain)
			x.logger.Infof("Grain (%s) registered", registry.Name(grain))
		}
	}

	x.logger.Info("Clustering is enabled...:)")
	return nil
}

// startClustering enables clustering. When clustering is enabled remoting is also enabled to facilitate remote
// communication
func (x *actorSystem) startClustering(ctx context.Context) error {
	if !x.clusterEnabled.Load() {
		return nil
	}

	x.logger.Info("Starting cluster engine...")
	if err := x.cluster.Start(ctx); err != nil {
		x.logger.Errorf("failed to start cluster engine: %v", err)
		return err
	}

	x.logger.Info("Cluster engine successfully started...")

	x.setupGrainActivationBarrier(ctx)

	x.eventsQueue = x.cluster.Events()
	x.rebalancingQueue = make(chan *internalpb.PeerState, 1)
	go x.clusterEventsLoop()
	go x.replicateActors()
	go x.replicateGrains()

	// start the various relocation loops when relocation is enabled
	if x.relocationEnabled.Load() {
		go x.rebalancingLoop()
	}

	if err := x.cleanupStaleLocalActors(ctx); err != nil {
		x.logger.Warnf("Failed to cleanup stale cluster actors: %v", err)
	}

	x.logger.Info("Clustering started...:)")
	return nil
}

// cleanupStaleLocalActors removes cluster actor records that belong to this node
// but have no corresponding local PID. This is a best-effort cleanup for unclean
// restarts and does not fail startup on errors.
func (x *actorSystem) cleanupStaleLocalActors(ctx context.Context) error {
	if !x.clusterEnabled.Load() || x.cluster == nil {
		return nil
	}

	timeout := time.Second
	if x.clusterConfig != nil {
		timeout = x.clusterConfig.readTimeout
	}

	actors, err := x.cluster.Actors(ctx, timeout)
	if err != nil {
		return err
	}

	host := x.remoteConfig.BindAddr()
	port := x.remoteConfig.BindPort()

	for _, actor := range actors {
		addr, err := address.Parse(actor.GetAddress())
		if err != nil {
			x.logger.Warnf("Failed to parse cluster Actor address %q: %v", actor.GetAddress(), err)
			continue
		}

		if !strings.EqualFold(addr.System(), x.name) {
			continue
		}
		if addr.Host() != host || addr.Port() != port {
			continue
		}
		if isSystemName(addr.Name()) {
			continue
		}
		if _, ok := x.actors.node(addr.String()); ok {
			continue
		}

		if err := x.cluster.RemoveActor(ctx, addr.Name()); err != nil {
			x.logger.Warnf("Failed to remove stale cluster Actor %s: %v", addr.String(), err)
			continue
		}

		x.logger.Debugf("Removed stale cluster Actor %s", addr.String())
	}

	return nil
}

// startRemoting enables the remoting service to handle remote messaging
func (x *actorSystem) startRemoting(ctx context.Context) error {
	if !x.remotingEnabled.Load() {
		return nil
	}

	x.logger.Info("Starting remoting...")

	opts := []connect.HandlerOption{
		connectproto.WithBinary(
			proto.MarshalOptions{},
			proto.UnmarshalOptions{DiscardUnknown: true},
		),
		connect.WithRecover(func(_ context.Context, spec connect.Spec, _ stdhttp.Header, recovered any) error {
			x.logger.Errorf("Remoting panic in %s: %v", spec.Procedure, recovered)
			return connect.NewError(connect.CodeInternal, fmt.Errorf("internal server error"))
		}),
	}

	if x.remoteConfig.MaxFrameSize() > 0 {
		opts = append(opts, connect.WithReadMaxBytes(int(x.remoteConfig.MaxFrameSize()))) // nolint
	}

	switch x.remoteConfig.Compression() {
	case remote.BrotliCompression:
		opts = append(opts, brotli.WithCompression())
	case remote.ZstdCompression:
		opts = append(opts, zstd.WithCompression())
	default:
		// no op
	}

	remotingServicePath, remotingServiceHandler := internalpbconnect.NewRemotingServiceHandler(x, opts...)
	clusterServicePath, clusterServiceHandler := internalpbconnect.NewClusterServiceHandler(x, opts...)

	mux := stdhttp.NewServeMux()
	mux.Handle(remotingServicePath, remotingServiceHandler)
	mux.Handle(clusterServicePath, clusterServiceHandler)

	// configure the appropriate server
	if err := x.configureServer(ctx, mux); err != nil {
		x.logger.Error(fmt.Errorf("failed enable remoting: %w", err))
		return err
	}

	go func() {
		if err := x.startHTTPServer(); err != nil {
			if !errors.Is(err, stdhttp.ErrServerClosed) {
				x.logger.Panic(fmt.Errorf("failed to start remoting service: %w", err))
			}
		}
	}()

	x.logger.Info("Remoting started...:)")
	return nil
}

// setupRemoting sets the remoting service
func (x *actorSystem) setupRemoting() error {
	opts := []remote.RemotingOption{
		remote.WithRemotingMaxReadFameSize(int(x.remoteConfig.MaxFrameSize())), // nolint
		remote.WithRemotingCompression(x.remoteConfig.Compression()),
	}

	if propagator := x.remoteConfig.ContextPropagator(); propagator != nil {
		opts = append(opts, remote.WithRemotingContextPropagator(propagator))
	}

	if x.tlsInfo != nil {
		opts = append(opts, remote.WithRemotingTLS(x.tlsInfo.ClientConfig))
	}

	x.remoting = remote.NewRemoting(opts...)
	return nil
}

// startMessagesScheduler starts the messages scheduler
func (x *actorSystem) startMessagesScheduler(ctx context.Context) {
	// set the scheduler
	x.scheduler = newScheduler(x.logger,
		x.shutdownTimeout,
		withSchedulerRemoting(x.remoting))
	// start the scheduler
	x.scheduler.Start(ctx)
}

// startEviction starts the eviction process for the actor system
func (x *actorSystem) startEviction() {
	if x.evictionStrategy != nil {
		go x.evictionLoop()
	}
}

// validateExtensions validates extensions
func (x *actorSystem) validateExtensions() error {
	for _, ext := range x.extensions.Values() {
		if ext != nil {
			if err := validation.NewIDValidator(ext.ID()).Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// reset the actor system
func (x *actorSystem) reset() {
	x.started.Store(false)
	x.starting.Store(false)
	x.extensions.Reset()
	x.actors.reset()
	x.grains.Reset()
	x.shuttingDown.Store(false)
	x.clusterStore = nil
	x.dataCenterController = nil
	x.dataCenterLeaderTicker = nil
	x.dataCenterReconcileInFlight.Store(false)
}

// shutdown stops the actor system
func (x *actorSystem) shutdown(ctx context.Context) (err error) {
	// we only shut down the actor system when it is starting or not yet started
	if x.starting.Load() || !x.started.Load() {
		return gerrors.ErrActorSystemNotStarted
	}

	x.logger.Info("Shutdown process begins.:)")
	x.shuttingDown.Store(true)

	defer func() {
		x.reset()
		err = multierr.Combine(err, x.logger.Flush())
	}()

	if x.evictionStrategy != nil {
		close(x.evictionStopSig)
	}

	if x.passivator != nil {
		x.passivator.Stop(ctx)
	}

	if x.scheduler != nil {
		x.scheduler.Stop(ctx)
	}

	actorRefs := make([]ActorRef, 0, len(x.Actors()))
	for _, actor := range x.Actors() {
		actorRefs = append(actorRefs, fromPID(actor))
	}

	// create a context with timeout to avoid blocking shutdown indefinitely
	ctx, cancel := context.WithTimeout(ctx, x.shutdownTimeout)
	defer cancel()

	// Run shutdown hooks and collect errors
	hooksErr := x.runShutdownHooks(ctx)

	// Stop data center leader watch and controller
	x.stopDataCenterLeaderWatch()
	stoperr := x.stopDataCenterController(ctx)
	hooksErr = multierr.Combine(hooksErr, stoperr)

	// Build peer state snapshot early in shutdown process.
	// Actual persistence happens later in shutdownCluster before leaving membership.
	peerState, preShutdownErr := x.preShutdown()
	if preShutdownErr != nil {
		x.logger.Errorf("Failed to build peer state snapshot: %v", preShutdownErr)
		// Continue with shutdown to free resources, but track error
	}

	// Helper to shut down cluster and remoting
	// Includes preShutdownErr in the chain so it's properly tracked and returned
	shutdownClusterAndRemoting := func() error {
		return chain.
			New(chain.WithRunAll()).
			AddRunner(func() error { return preShutdownErr }).
			AddRunner(func() error { return x.shutdownCluster(ctx, actorRefs, peerState) }).
			AddRunner(func() error { return x.shutdownRemoting(ctx) }).
			Run()
	}

	// shutdown user-facing actors first so grains can be safely deactivated without in-flight usage
	if err := chain.
		New(chain.WithFailFast(), chain.WithContext(ctx)).
		AddContextRunnerIf(x.getUserGuardian() != nil, x.getUserGuardian().Shutdown).
		AddContextRunnerIf(x.getSingletonManager() != nil, x.getSingletonManager().Shutdown).
		AddContextRunnerIf(x.getRelocator() != nil, x.getRelocator().Shutdown).
		AddContextRunnerIf(x.getDeadletter() != nil, x.getDeadletter().Shutdown).
		AddContextRunnerIf(x.getDeathWatch() != nil, x.getDeathWatch().Shutdown).
		Run(); err != nil {
		x.logger.Errorf("Failed to shutdown cleanly: %v", err)
		clusterErr := shutdownClusterAndRemoting()
		// Combine all errors if present
		return multierr.Combine(hooksErr, err, clusterErr)
	}

	// deactivate all grains while cluster + pubsub system actors are still alive
	if x.grains.Len() > 0 {
		x.logger.Info("Deactivating all grains...")
		grains := x.grains.Values()
		for _, grain := range grains {
			if err := grain.deactivate(ctx); err != nil {
				x.logger.Errorf("Failed to deactivate Grain (%s): %v", grain.getIdentity().String(), err)
				// TODO: should we return here or continue with the next grain?
				//return multierr.Combine(hooksErr, err)
			}
			x.grains.Delete(grain.getIdentity().String())
			x.logger.Infof("Grain (%s) successfully deactivated", grain.getIdentity().String())
		}
	}

	// shutdown remaining system actors
	if err := chain.
		New(chain.WithFailFast(), chain.WithContext(ctx)).
		AddContextRunnerIf(x.TopicActor() != nil, x.TopicActor().Shutdown).
		AddContextRunnerIf(x.NoSender() != nil, x.NoSender().Shutdown).
		AddContextRunnerIf(x.getSystemGuardian() != nil, x.getSystemGuardian().Shutdown).
		AddContextRunnerIf(x.getRootGuardian() != nil, x.getRootGuardian().Shutdown).
		Run(); err != nil {
		x.logger.Errorf("Failed to shutdown cleanly: %v", err)
		clusterErr := shutdownClusterAndRemoting()
		// Combine all errors if present
		return multierr.Combine(hooksErr, err, clusterErr)
	}

	x.actors.deleteNode(x.getRootGuardian())

	if x.eventsStream != nil {
		x.eventsStream.Close()
	}

	// Always attempt to shutdown cluster and remoting
	// clusterErr includes preShutdownErr if it exists (via chain)
	clusterErr := shutdownClusterAndRemoting()
	if clusterErr != nil {
		x.logger.Errorf("Failed shutdown: %v", clusterErr)
		return multierr.Combine(hooksErr, clusterErr)
	}

	if hooksErr != nil {
		x.logger.Errorf("Failed shutdown cleanly: %v", hooksErr)
		return hooksErr
	}

	x.logger.Info("Shutdown successfully")
	return nil
}

// replicateActors publishes newly created actor into the cluster when cluster is enabled
func (x *actorSystem) replicateActors() {
	for actor := range x.actorsQueue {
		// never replicate system actors because there are specific to the
		// started node
		addr, _ := address.Parse(actor.GetAddress())
		if isSystemName(addr.Name()) {
			continue
		}

		if !x.isStopping() && x.InCluster() {
			ctx := context.Background()
			cluster := x.getCluster()
			if actor.GetSingleton() != nil {
				kind := actor.GetType()
				role := actor.GetRole()
				if role != "" {
					kind = kindRole(kind, role)
				}

				if err := cluster.PutKind(ctx, kind); err != nil {
					x.logger.Warn(err.Error())
					continue
				}
			}

			if err := cluster.PutActor(ctx, actor); err != nil {
				x.logger.Warn(err.Error())
			}
		}
	}
}

// replicateGrains publishes newly created grain into the cluster when cluster is enabled
func (x *actorSystem) replicateGrains() {
	for grain := range x.grainsQueue {
		// never replicate system grains because there are specific to the
		// started node
		if isSystemName(grain.GetGrainId().GetName()) {
			continue
		}

		if !x.isStopping() && x.InCluster() {
			ctx := context.Background()

			if err := x.cluster.PutGrain(ctx, grain); err != nil {
				x.logger.Warn(err.Error())
			}

			if err := x.cluster.PutKind(ctx, grain.GetGrainId().GetKind()); err != nil {
				x.logger.Warn(err.Error())
			}
		}
	}
}

// resyncActors resyncs all actors in the actor system
// This is only called during cluster events like NodeLeft or NodeJoined
// to ensure that all actors are properly synchronized across the cluster.
func (x *actorSystem) resyncActors() error {
	actors := x.Actors()
	for _, actor := range actors {
		if err := x.putActorOnCluster(actor); err != nil {
			x.logger.Errorf("Failed to resync Actor (%s): %v", actor.Address().String(), err)
			return fmt.Errorf("failed to resync Actor (%s): %w", actor.Address().String(), err)
		}
	}
	return nil
}

// resyncGrains resyncs all grains in the actor system
// This is only called during cluster events like NodeLeft or NodeJoined
// to ensure that all actors are properly synchronized across the cluster.
func (x *actorSystem) resyncGrains() error {
	grains := x.grains.Values()
	for _, grain := range grains {
		if err := x.putGrainOnCluster(grain); err != nil {
			x.logger.Errorf("Failed to resync Grain (%s): %v", grain.getIdentity().String(), err)
			return fmt.Errorf("failed to resync Grain (%s): %w", grain.getIdentity().String(), err)
		}
	}
	return nil
}

// clusterEventsLoop listens to cluster events and send them to the event streams
func (x *actorSystem) clusterEventsLoop() {
	for event := range x.eventsQueue {
		if x.isStopping() || !x.InCluster() || event == nil || event.Payload == nil {
			continue
		}

		message, _ := event.Payload.UnmarshalNew()

		if x.eventsStream != nil {
			x.logger.Debugf("Node (%s) publishing cluster event=(%s)....", x.String(), event.Type)
			x.eventsStream.Publish(eventsTopic, message)
			x.logger.Debugf("Node (%s) published cluster event=(%s) successfully", x.String(), event.Type)
		}

		switch event.Type {
		case cluster.NodeLeft:
			x.handleNodeLeftEvent(event)
		case cluster.NodeJoined:
			x.handleNodeJoinedEvent(event)
		}
	}
}

// handleNodeJoinedEvent processes a NodeJoined cluster event.
func (x *actorSystem) handleNodeJoinedEvent(event *cluster.Event) {
	nodeJoined := new(goaktpb.NodeJoined)
	_ = event.Payload.UnmarshalTo(nodeJoined)
	x.logger.Infof("Node %s detected node joined event: Node (%s)",
		x.String(),
		nodeJoined.GetAddress())

	x.tryOpenGrainActivationBarrier(context.Background())
	x.resyncAfterClusterEvent("node joined", nodeJoined.GetAddress())
}

// handleNodeLeftEvent processes a NodeLeft cluster event.
func (x *actorSystem) handleNodeLeftEvent(event *cluster.Event) {
	nodeLeft := new(goaktpb.NodeLeft)
	_ = event.Payload.UnmarshalTo(nodeLeft)
	x.logger.Infof(
		"Node (%s) detected node left event: Node (%s)",
		x.String(), nodeLeft.GetAddress(),
	)

	x.resyncAfterClusterEvent("node left", nodeLeft.GetAddress())

	if !x.relocationEnabled.Load() {
		return
	}

	ctx := context.Background()

	if x.cluster.IsLeader(ctx) {
		x.logger.Infof(
			"Leader (%s) initiating Node (%s)'s state rebalancing",
			x.String(), nodeLeft.GetAddress(),
		)

		if !x.rebalancedNodes.Contains(nodeLeft.GetAddress()) {
			x.rebalancedNodes.Add(nodeLeft.GetAddress())

			// fetch the peer state of the node that left from the cluster store
			// and enqueue it for rebalancing
			peerState, ok := x.clusterStore.GetPeerState(ctx, nodeLeft.GetAddress())
			if !ok {
				x.logger.Warnf("Leader (%s) could not find Node (%s)'s state in cluster store",
					x.String(), nodeLeft.GetAddress())
				return
			}

			x.relocatingLocker.Lock()
			x.rebalancingQueue <- peerState
			x.relocatingLocker.Unlock()
		}
		return
	}

	// clean up the peer state of the node that left from the cluster store
	x.logger.Debugf(
		"Node (%s) is not the cluster leader; cleaning up Node (%s) left from state cache",
		x.String(), nodeLeft.GetAddress(),
	)

	if err := x.clusterStore.DeletePeerState(ctx, nodeLeft.GetAddress()); err != nil {
		x.logger.Errorf("Node (%s) failed to remove left Node (%s) from cluster store: %w", x.String(), nodeLeft.GetAddress(), err)
	}

	x.logger.Debugf("Node (%s) successfully cleaned up Node (%s) left from state cache", x.String(), nodeLeft.GetAddress())
}

// resyncAfterClusterEvent handles resyncing actors and grains after a cluster event.
func (x *actorSystem) resyncAfterClusterEvent(eventType, nodeAddress string) {
	x.logger.Debugf("Node (%s) resyncing actors after %s event: Node (%s)",
		x.String(), eventType, nodeAddress)

	if err := x.resyncActors(); err != nil {
		x.logger.Errorf("Node (%s) failed to resync actors after %s event: %v", x.String(), eventType, err)
	}

	x.logger.Debugf("Node (%s) successfully resynced actors after %s event: Node (%s)",
		x.String(), eventType, nodeAddress)

	if x.grains.Len() > 0 {
		x.logger.Debugf("Node (%s) resyncing grains after %s event: node=(%s)",
			x.String(), eventType, nodeAddress)

		if err := x.resyncGrains(); err != nil {
			x.logger.Errorf("Node (%s) failed to resync grains after %s event: %v", x.String(), eventType, err)
		}

		x.logger.Debugf("Node (%s) successfully resynced grains after %s event: Node (%s)",
			x.String(), eventType, nodeAddress)
	}
}

// rebalancingLoop helps perform cluster rebalancing
func (x *actorSystem) rebalancingLoop() {
	for peerState := range x.rebalancingQueue {
		ctx := context.Background()
		if !x.shouldRebalance(peerState) {
			x.logger.Debugf("Node (%s) found no Peer (%s)'s state to rebalance", x.name,
				net.JoinHostPort(peerState.GetHost(), strconv.Itoa(int(peerState.GetPeersPort()))))
			continue
		}

		if x.relocating.Load() {
			x.relocatingLocker.Lock()
			x.rebalancingQueue <- peerState
			x.relocatingLocker.Unlock()
			continue
		}

		x.relocating.Store(true)
		message := &internalpb.Rebalance{PeerState: peerState}
		if err := x.systemGuardian.Tell(ctx, x.relocator, message); err != nil {
			x.logger.Error(err)
		}
	}
}

// shouldRebalance returns true when the current node can perform the cluster rebalancing
func (x *actorSystem) shouldRebalance(peerState *internalpb.PeerState) bool {
	return peerState != nil &&
		x.InCluster() &&
		!proto.Equal(peerState, new(internalpb.PeerState)) &&
		(len(peerState.GetActors()) != 0 ||
			len(peerState.GetGrains()) != 0)
}

// configPID constructs a PID provided the actor name and the actor kind
// this is a utility function used when spawning actors
func (x *actorSystem) configPID(ctx context.Context, name string, actor Actor, opts ...SpawnOption) (*PID, error) {
	spawnConfig := newSpawnConfig(opts...)
	if err := spawnConfig.Validate(); err != nil {
		return nil, err
	}

	if !spawnConfig.isSystem {
		// you should not create a system-based actor or
		// use the system actor naming convention pattern
		if isSystemName(name) {
			return nil, gerrors.ErrReservedName
		}
	}

	addr := x.actorAddress(name)
	if err := addr.Validate(); err != nil {
		return nil, err
	}

	// define the pid options
	// pid inherit the actor system settings defined during instantiation
	pidOpts := []pidOption{
		withInitMaxRetries(x.actorInitMaxRetries),
		withCustomLogger(x.logger),
		withActorSystem(x),
		withEventsStream(x.eventsStream),
		withInitTimeout(x.actorInitTimeout),
		withRemoting(x.remoting),
		withPassivationManager(x.passivator),
		withMetricProvider(x.metricProvider),
	}

	// set the actor as a system actor when necessary
	if spawnConfig.isSystem {
		pidOpts = append(pidOpts, asSystemActor())
	}

	// set the mailbox option
	if spawnConfig.mailbox != nil {
		pidOpts = append(pidOpts, withMailbox(spawnConfig.mailbox))
	}

	supervisor := spawnConfig.supervisor
	if supervisor == nil {
		supervisor = x.defaultSupervisor
	}
	if supervisor != nil {
		pidOpts = append(pidOpts, withSupervisor(supervisor))
	}

	// define the actor as singleton when necessary
	if spawnConfig.asSingleton {
		pidOpts = append(pidOpts, asSingleton())
		// this should always be set when the actor is a singleton
		// no need to check for nil because it is always set when the actor is a singleton
		// and it is not possible to spawn a singleton actor without a singleton spec
		pidOpts = append(pidOpts, withSingletonSpec(spawnConfig.singletonSpec))
	}

	if !spawnConfig.relocatable {
		pidOpts = append(pidOpts, withRelocationDisabled())
	}

	// enable stash
	if spawnConfig.enableStash {
		pidOpts = append(pidOpts, withStash())
	}

	// enable reentrancy when configured
	if spawnConfig.reentrancy != nil {
		pidOpts = append(pidOpts, withReentrancy(spawnConfig.reentrancy))
	}

	// set the role
	if spawnConfig.role != nil {
		pidOpts = append(pidOpts, withRole(pointer.Deref(spawnConfig.role, "")))
	}

	// set the dependencies when defined
	if spawnConfig.dependencies != nil {
		for _, dependency := range spawnConfig.dependencies {
			x.registry.Register(dependency)
		}
		pidOpts = append(pidOpts, withDependencies(spawnConfig.dependencies...))
	}

	strategy := spawnConfig.passivationStrategy
	if strategy == nil {
		strategy = x.defaultPassivationStrategy
	}
	pidOpts = append(pidOpts, withPassivationStrategy(strategy))

	pid, err := newPID(
		ctx,
		addr,
		actor,
		pidOpts...,
	)

	if err != nil {
		return nil, err
	}
	return pid, nil
}

// tree returns the actors tree
func (x *actorSystem) tree() *tree {
	return x.actors
}

// getCluster returns the cluster engine
func (x *actorSystem) getCluster() cluster.Cluster {
	x.locker.RLock()
	cluster := x.cluster
	x.locker.RUnlock()
	return cluster
}

// reservedName returns the reserved actor's name
func (x *actorSystem) reservedName(nameType nameType) string {
	return reservedNames[nameType]
}

// actorAddress returns the actor path provided the actor name
func (x *actorSystem) actorAddress(name string) *address.Address {
	return address.New(name, x.name, x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
}

// startHTTPServer starts the appropriate http server
func (x *actorSystem) startHTTPServer() error {
	return x.server.Serve(x.listener)
}

// shutdownHTTPServer stops the appropriate http server
func (x *actorSystem) shutdownHTTPServer(ctx context.Context) error {
	return x.server.Shutdown(ctx)
}

// configureServer configure the various http server and listeners based upon the various settings
func (x *actorSystem) configureServer(ctx context.Context, mux *stdhttp.ServeMux) error {
	hostPort := net.JoinHostPort(x.remoteConfig.BindAddr(), strconv.Itoa(x.remoteConfig.BindPort()))
	httpServer := getServer(ctx, hostPort)
	listener, err := network.NewKeepAliveListener(httpServer.Addr)
	if err != nil {
		return err
	}

	// Configure HTTP/2 with performance tuning
	http2Server := &http2.Server{
		MaxConcurrentStreams: 1000, // Allow up to 1000 concurrent streams
		MaxReadFrameSize:     x.remoteConfig.MaxFrameSize(),
		IdleTimeout:          x.remoteConfig.IdleTimeout(),
		WriteByteTimeout:     x.remoteConfig.WriteTimeout(),
		ReadIdleTimeout:      x.remoteConfig.ReadIdleTimeout(),
	}

	// set the http TLS server
	if x.tlsInfo != nil {
		x.server = httpServer
		x.server.TLSConfig = x.tlsInfo.ServerConfig
		x.server.Handler = mux
		x.listener = tls.NewListener(listener, x.tlsInfo.ServerConfig)
		return http2.ConfigureServer(x.server, http2Server)
	}

	// http/2 server with h2c (HTTP/2 Cleartext).
	x.server = httpServer
	x.server.Handler = h2c.NewHandler(mux, http2Server)
	x.listener = listener
	return nil
}

// spawnRootGuardian creates the rootGuardian guardian
func (x *actorSystem) spawnRootGuardian(ctx context.Context) error {
	actorName := x.reservedName(rootGuardianType)
	x.rootGuardian, _ = x.configPID(ctx, actorName, newRootGuardian(), asSystem(), WithLongLived())
	// rootGuardian is the rootGuardian node of the actors tree
	return x.actors.addRootNode(x.rootGuardian)
}

// spawnSystemGuardian creates the system guardian
func (x *actorSystem) spawnSystemGuardian(ctx context.Context) error {
	actorName := x.reservedName(systemGuardianType)
	x.systemGuardian, _ = x.configPID(ctx,
		actorName,
		newSystemGuardian(),
		asSystem(),
		WithLongLived(),
		WithSupervisor(sup.NewSupervisor(
			sup.WithStrategy(sup.OneForOneStrategy),
			sup.WithAnyErrorDirective(sup.EscalateDirective),
		)))

	// systemGuardian is a child actor of the rootGuardian actor
	return x.actors.addNode(x.rootGuardian, x.systemGuardian)
}

// spawnUserGuardian creates the user guardian
func (x *actorSystem) spawnUserGuardian(ctx context.Context) error {
	actorName := x.reservedName(userGuardianType)
	x.userGuardian, _ = x.configPID(ctx,
		actorName,
		newUserGuardian(),
		asSystem(),
		WithLongLived(),
		WithSupervisor(sup.NewSupervisor(
			sup.WithStrategy(sup.OneForOneStrategy),
			sup.WithAnyErrorDirective(sup.EscalateDirective),
		)))

	// userGuardian is a child actor of the rootGuardian actor
	return x.actors.addNode(x.rootGuardian, x.userGuardian)
}

// spawnDeathWatch creates the deathWatch actor
func (x *actorSystem) spawnDeathWatch(ctx context.Context) error {
	actorName := x.reservedName(deathWatchType)
	// define the supervisor strategy to use
	supervisor := sup.NewSupervisor(
		sup.WithStrategy(sup.OneForOneStrategy),
		sup.WithAnyErrorDirective(sup.EscalateDirective),
	)

	x.deathWatch, _ = x.configPID(ctx,
		actorName,
		newDeathWatch(),
		asSystem(),
		WithLongLived(),
		WithSupervisor(supervisor),
	)

	// the deathWatch is a child actor of the system guardian
	return x.actors.addNode(x.systemGuardian, x.deathWatch)
}

// spawnRelocator creates the actor responsible for re-deploying actors when a node leaves the cluster
func (x *actorSystem) spawnRelocator(ctx context.Context) error {
	if x.clusterEnabled.Load() && x.relocationEnabled.Load() {
		actorName := x.reservedName(rebalancerType)

		// define the supervisor strategy to use
		supervisor := sup.NewSupervisor(
			sup.WithStrategy(sup.OneForOneStrategy),
			sup.WithDirective(&gerrors.PanicError{}, sup.RestartDirective),
			sup.WithDirective(&runtime.PanicNilError{}, sup.RestartDirective),
			sup.WithDirective(&gerrors.RebalancingError{}, sup.RestartDirective),
			sup.WithDirective(&gerrors.InternalError{}, sup.ResumeDirective),
			sup.WithDirective(&gerrors.SpawnError{}, sup.ResumeDirective),
		)

		x.relocator, _ = x.configPID(ctx,
			actorName,
			newRelocator(x.remoting),
			asSystem(),
			WithLongLived(),
			WithSupervisor(supervisor),
		)
		// the relocator is a child actor of the system guardian
		return x.actors.addNode(x.systemGuardian, x.relocator)
	}
	return nil
}

// spawnDeadletter creates the deadletter synthetic actor
func (x *actorSystem) spawnDeadletter(ctx context.Context) error {
	actorName := x.reservedName(deadletterType)
	x.deadletter, _ = x.configPID(ctx,
		actorName,
		newDeadLetter(),
		asSystem(),
		WithLongLived(),
		WithSupervisor(
			sup.NewSupervisor(
				sup.WithStrategy(sup.OneForOneStrategy),
				sup.WithAnyErrorDirective(sup.ResumeDirective),
			),
		),
	)
	// the deadletter is a child actor of the system guardian
	return x.actors.addNode(x.systemGuardian, x.deadletter)
}

// checkSpawnPreconditions make sure before an actor is created some pre-conditions are checks
func (x *actorSystem) checkSpawnPreconditions(ctx context.Context, actorName string, actor Actor, singleton bool, singletonRole *string) error {
	// check the existence of the actor given the kind prior to creating it
	if x.clusterEnabled.Load() {
		var reservedKind string
		// a singleton actor must only have one instance at a given time of its kind
		// in the whole cluster
		if singleton {
			kind := registry.Name(actor)
			role := strings.TrimSpace(pointer.Deref(singletonRole, ""))
			if role != "" {
				kind = kindRole(kind, role)
			}

			if err := cluster.PutKindIfAbsent(ctx, x.cluster, kind); err != nil {
				if errors.Is(err, cluster.ErrKindAlreadyExists) {
					return gerrors.ErrSingletonAlreadyExists
				}
				return err
			}
			// We successfully reserved the singleton kind in the cluster. If we fail later in
			// preconditions (e.g., name already taken), we must release it to avoid leaking the
			// singleton reservation.
			reservedKind = kind
		}

		// here we make sure in cluster mode that the given actor is uniquely created
		exists, err := x.cluster.ActorExists(ctx, actorName)
		if err != nil {
			if reservedKind != "" {
				_ = x.cluster.RemoveKind(ctx, reservedKind)
			}
			return err
		}

		if exists {
			if reservedKind != "" {
				_ = x.cluster.RemoveKind(ctx, reservedKind)
			}
			return gerrors.NewErrActorAlreadyExists(actorName)
		}
	}

	return nil
}

// cleanupCluster cleans up the cluster
func (x *actorSystem) cleanupCluster(ctx context.Context, actorRefs []ActorRef) error {
	eg, ctx := errgroup.WithContext(ctx)

	// Remove singleton actors from the cluster
	if x.cluster.IsLeader(ctx) {
		for _, actorRef := range actorRefs {
			if actorRef.IsSingleton() {
				actorRef := actorRef
				eg.Go(func() error {
					kind := actorRef.Kind()
					if err := x.cluster.RemoveKind(ctx, kind); err != nil {
						x.logger.Errorf("Failed to remove Kind (%s) from cluster: %v", kind, err)
						return err
					}
					x.logger.Infof("Kind (%s) removed from cluster", kind)
					return nil
				})
			}
		}
	}

	// Remove all actors from the cluster
	for _, actorRef := range actorRefs {
		actorRef := actorRef
		eg.Go(func() error {
			actorName := actorRef.Name()
			if err := x.cluster.RemoveActor(ctx, actorName); err != nil {
				x.logger.Errorf("Failed to Actor (%s) from cluster: %v", actorName, err)
				return err
			}
			x.logger.Infof("Actor (%s) removed from cluster", actorName)
			return nil
		})
	}

	// Remove all grains from the cluster if exists
	if x.grains.Len() > 0 {
		for _, grain := range x.grains.Values() {
			grain := grain
			eg.Go(func() error {
				if err := x.cluster.RemoveGrain(ctx, grain.identity.String()); err != nil {
					x.logger.Errorf("Failed to remove Grain (%s) from cluster: %v", grain.identity.String(), err)
					return err
				}
				x.logger.Infof("Grain (%s) removed from cluster", grain.identity.String())
				return nil
			})
		}
	}

	return eg.Wait()
}

// getSetDeadlettersCount gets and sets the deadletter count
func (x *actorSystem) getSetDeadlettersCount(ctx context.Context) {
	var (
		to      = x.getDeadletter()
		from    = x.getSystemGuardian()
		message = new(internalpb.DeadlettersCountRequest)
	)
	if to.IsRunning() {
		// ask the deadletter actor for the count
		// using the default ask timeout
		// note: no need to check for error because this call is internal
		reply, _ := from.Ask(ctx, to, message, DefaultAskTimeout)
		// Be defensive: if the actor is shutting down or a call was timed out and a late
		// response got dropped, reply can be nil or an unexpected type.
		if deadlettersCount, ok := reply.(*internalpb.DeadlettersCountResponse); ok && deadlettersCount != nil {
			x.deadlettersCounter.Store(uint64(deadlettersCount.GetTotalCount()))
		}
	}
}

func (x *actorSystem) shutdownCluster(ctx context.Context, actors []ActorRef, peerState *internalpb.PeerState) error {
	if x.clusterEnabled.Load() {
		if x.cluster != nil {
			// Persist peer state to all cluster peers before leaving membership.
			// This ensures state is available for relocation when NodeLeft event
			// is processed. Errors are returned but we proceed with shutdown to
			// ensure resources are freed (chain uses WithRunAll).
			if err := chain.
				New(chain.WithRunAll(), chain.WithContext(ctx)).
				AddContextRunnerIf(peerState != nil, func(cctx context.Context) error { return x.persistPeerStateToPeers(cctx, peerState) }).
				AddContextRunner(func(cctx context.Context) error { return x.cleanupCluster(cctx, actors) }).
				AddContextRunner(func(cctx context.Context) error { return x.cluster.Stop(cctx) }).
				AddContextRunnerIf(x.clusterStore != nil, func(cctx context.Context) error { return x.clusterStore.Close() }).
				Run(); err != nil {
				x.logger.Errorf("Failed to shutdown cleanly: %w", err)
				return err
			}
		}

		if x.actorsQueue != nil {
			close(x.actorsQueue)
		}

		x.clusterEnabled.Store(false)
		x.relocating.Store(false)
		x.pubsubEnabled.Store(false)
		x.relocatingLocker.Lock()

		if x.rebalancingQueue != nil {
			close(x.rebalancingQueue)
		}

		if x.grainsQueue != nil {
			close(x.grainsQueue)
		}

		x.relocatingLocker.Unlock()
	}
	return nil
}

func (x *actorSystem) shutdownRemoting(ctx context.Context) error {
	if x.remotingEnabled.Load() {
		if x.remoting != nil {
			x.remoting.Close()
		}

		if x.server != nil {
			if err := x.shutdownHTTPServer(ctx); err != nil {
				x.logger.Errorf("Failed to shutdown server: %w", err)
				return err
			}
			x.remotingEnabled.Store(false)
			x.server = nil
			x.listener = nil
		}
	}
	return nil
}

// runShutdownHooks executes all registered shutdown hooks in the CoordinatedShutdown instance.
// nolint: gocyclo
func (x *actorSystem) runShutdownHooks(ctx context.Context) (err error) {
	hooks := x.shutdownHooks
	if len(hooks) == 0 {
		// No hooks to run, return nil
		return nil
	}

	var errs []error
	// add some recovery mechanism to handle panics
	defer func() {
		if r := recover(); r != nil {
			switch v, ok := r.(error); {
			case ok:
				var pe *gerrors.PanicError
				if errors.As(v, &pe) {
					err = pe
					return
				}

				// this is a normal error just wrap it with some stack trace
				// for rich logging purpose
				pc, fn, line, _ := runtime.Caller(2)
				err = gerrors.NewPanicError(
					fmt.Errorf("%w at %s[%s:%d]", v, runtime.FuncForPC(pc).Name(), fn, line),
				)

			default:
				// we have no idea what panic it is. Enrich it with some stack trace for rich
				// logging purpose
				pc, fn, line, _ := runtime.Caller(2)
				err = gerrors.NewPanicError(
					fmt.Errorf("%#v at %s[%s:%d]", r, runtime.FuncForPC(pc).Name(), fn, line),
				)
			}
		}
	}()

	for _, hook := range hooks {
		if hook == nil {
			continue
		}
		err := hook.Execute(ctx, x)
		if err == nil {
			continue
		}

		recovery := hook.Recovery()
		if recovery == nil {
			// If no recovery strategy is defined, we treat it as a failure.
			return err
		}

		switch recovery.Strategy() {
		case ShouldFail:
			return err
		case ShouldRetryAndFail:
			if retryErr := runWithRetry(ctx, hook, x, recovery); retryErr != nil {
				return retryErr
			}
		case ShouldSkip:
			errs = append(errs, err)
		case ShouldRetryAndSkip:
			if retryErr := runWithRetry(ctx, hook, x, recovery); retryErr != nil {
				errs = append(errs, retryErr)
			}
		}
	}
	if len(errs) > 0 {
		return multierr.Combine(errs...)
	}
	return nil
}

// evictionLoop starts the system wide eviction loop
func (x *actorSystem) evictionLoop() {
	x.logger.Info("Start the system wide eviction loop", x.Name())
	x.logger.Infof("System wide eviction policy %s", x.evictionStrategy.String())
	var clock *ticker.Ticker
	tickerStopSig := make(chan types.Unit, 1)
	clock = ticker.New(x.evictionInterval)
	clock.Start()

	go func() {
		for {
			select {
			case <-clock.Ticks:
				x.runEviction()
			case <-x.evictionStopSig:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()

	<-tickerStopSig
	clock.Stop()
	x.logger.Info("System wide eviction loop stopped")
}

// runEviction returns the eviction function based on the eviction strategy
func (x *actorSystem) runEviction() {
	ctx := context.Background()
	var actors []*PID
	switch x.evictionStrategy.Policy() {
	case LRU:
		actors = x.getLRUActors(x.evictionStrategy.Limit(), x.evictionStrategy.Percentage())
	case LFU:
		actors = x.getLFUActors(x.evictionStrategy.Limit(), x.evictionStrategy.Percentage())
	case MRU:
		actors = x.getMRUActors(x.evictionStrategy.Limit(), x.evictionStrategy.Percentage())
	default:
	}

	for _, actor := range actors {
		if err := actor.Shutdown(ctx); err != nil {
			x.logger.Errorf("Failed to shutdown Actor %s: %w", actor.Name(), err)
		}
	}
}

// getLRUActors identifies and returns the least recently used actors.
// The LRU algorithm is triggered when the number of items reaches or exceeds the threshold.
// It sorts items primarily by LatestActivityTime (ascending) and then by ProcessedCount (ascending)
// to determine the least recently used.
// percentageToReturn: The percentage of items to return (e.g., 20 for 20%).
func (x *actorSystem) getLRUActors(threshold uint64, percentageToReturn int) []*PID {
	total := x.NumActors()
	if total <= threshold {
		return nil
	}

	actors := x.Actors()
	evictions := make([]*PID, len(actors))
	copy(evictions, actors)

	sort.Slice(evictions, func(i, j int) bool {
		if evictions[i].LatestActivityTime().Unix() != evictions[j].LatestActivityTime().Unix() {
			return evictions[i].LatestActivityTime().Unix() < evictions[j].LatestActivityTime().Unix()
		}

		return evictions[i].ProcessedCount() < evictions[j].ProcessedCount()
	})

	evictionCount := computeEvictionCount(total, threshold, len(evictions), percentageToReturn)
	return evictions[:evictionCount]
}

// getLFUActors identifies and returns the least frequently used actors
// The LFU algorithm is triggered when the number of items reaches or exceeds the threshold.
// It sorts actors primarily by ProcessedCount (ascending) and then by LatestActivityTime (ascending)
// to determine the least frequently used.
// percentageToReturn: The percentage of items to return (e.g., 20 for 20%).
func (x *actorSystem) getLFUActors(threshold uint64, percentageToReturn int) []*PID {
	total := x.NumActors()
	if total <= threshold {
		return nil
	}

	actors := x.Actors()
	evictions := make([]*PID, len(actors))
	copy(evictions, actors)

	sort.Slice(evictions, func(i, j int) bool {
		if evictions[i].ProcessedCount() != evictions[j].ProcessedCount() {
			return evictions[i].ProcessedCount() < evictions[j].ProcessedCount()
		}

		return evictions[i].LatestActivityTime().Unix() < evictions[j].LatestActivityTime().Unix()
	})

	evictionCount := computeEvictionCount(total, threshold, len(evictions), percentageToReturn)
	return evictions[:evictionCount]
}

// getMRUActors identifies and returns the most recently used actors.
// The MRU algorithm is triggered when the number of actors reaches or exceeds the threshold.
// It sorts items primarily by LatestActivityTime (descending) and then by ProcessedCount (descending)
// to determine the most recently used.
// percentageToReturn: The percentage of actors to return (e.g., 20 for 20%)
func (x *actorSystem) getMRUActors(threshold uint64, percentageToReturn int) []*PID {
	total := x.NumActors()
	if total <= threshold {
		return nil
	}

	actors := x.Actors()
	evictions := make([]*PID, len(actors))
	copy(evictions, actors)

	sort.Slice(evictions, func(i, j int) bool {
		if evictions[i].LatestActivityTime().Unix() != evictions[j].LatestActivityTime().Unix() {
			return evictions[i].LatestActivityTime().Unix() > evictions[j].LatestActivityTime().Unix()
		}

		return evictions[i].ProcessedCount() > evictions[j].ProcessedCount()
	})

	evictionCount := computeEvictionCount(total, threshold, len(evictions), percentageToReturn)
	return evictions[:evictionCount]
}

// passivationManager returns the passivation manager
func (x *actorSystem) passivationManager() *passivationManager {
	x.locker.RLock()
	passivator := x.passivator
	x.locker.RUnlock()
	return passivator
}

// removeNodeLeft removes the given left node from the actor system
func (x *actorSystem) removeNodeLeft(address string) {
	x.locker.RLock()
	x.rebalancedNodes.Remove(address)
	x.locker.RUnlock()
}

func (x *actorSystem) registerMetrics() error {
	if x.metricProvider != nil && x.metricProvider.Meter() != nil {
		meter := x.metricProvider.Meter()
		metrics, err := metric.NewActorSystemMetric(meter)
		if err != nil {
			return err
		}

		observeOptions := []otelmetric.ObserveOption{
			otelmetric.WithAttributes(attribute.String("actor.system", x.Name())),
		}

		_, err = meter.RegisterCallback(func(ctx context.Context, observer otelmetric.Observer) error {
			var peersCount int64
			if x.clusterEnabled.Load() && x.cluster != nil {
				peers, err := x.cluster.Members(ctx)
				if err != nil {
					return err
				}
				peersCount = int64(len(peers))
			}

			observer.ObserveInt64(metrics.PIDsCount(), int64(x.actorsCounter.Load()), observeOptions...)
			observer.ObserveInt64(metrics.Uptime(), x.Uptime(), observeOptions...)
			observer.ObserveInt64(metrics.PeersCount(), peersCount, observeOptions...)
			observer.ObserveInt64(metrics.DeadlettersCount(), int64(x.deadlettersCounter.Load()), observeOptions...)
			return nil
		}, metrics.PIDsCount(),
			metrics.Uptime(),
			metrics.PeersCount(),
			metrics.DeadlettersCount(),
		)

		return err
	}
	return nil
}

// preShutdown builds the peer state snapshot for cluster persistence.
// This snapshot includes all actors and grains currently active on this node.
// The actual persistence to remote peers happens later in shutdownCluster
// to ensure proper ordering: persist state before leaving membership.
func (x *actorSystem) preShutdown() (*internalpb.PeerState, error) {
	if !x.clusterEnabled.Load() || x.cluster == nil {
		x.logger.Infof("Node (%s) is not part of a cluster; skipping peer state build", x.PeersAddress())
		return nil, nil
	}

	actors := x.Actors()
	grains := x.grains.Values()

	wireActors := make(map[string]*internalpb.Actor, len(actors))
	for _, actor := range actors {
		wireActor, err := actor.toWireActor()
		if err != nil {
			return nil, err
		}
		wireActors[actor.ID()] = wireActor
	}

	wireGrains := make(map[string]*internalpb.Grain, len(grains))
	for _, grain := range grains {
		wireGrain, err := grain.toWireGrain()
		if err != nil {
			return nil, err
		}
		wireGrains[grain.getIdentity().String()] = wireGrain
	}

	peerState := &internalpb.PeerState{
		Host:         x.Host(),
		PeersPort:    int32(x.clusterNode.PeersPort), // nolint
		RemotingPort: int32(x.Port()),                // nolint
		Actors:       wireActors,
		Grains:       wireGrains,
	}

	return peerState, nil
}

// persistPeerStateToPeers sends the peer state to the oldest cluster peers via RPC.
// This is called during shutdown before leaving membership to ensure state
// is available for relocation.
//
// The function implements quorum-based replication with early termination:
//   - Sends state to the K oldest peers (most likely to become leader)
//   - Returns successfully once quorum (majority) acknowledges
//   - Cancels remaining RPCs after quorum to avoid unnecessary waiting
//   - Accepts partial success if at least one peer receives the state
func (x *actorSystem) persistPeerStateToPeers(ctx context.Context, peerState *internalpb.PeerState) error {
	if peerState == nil {
		return nil
	}

	peers, err := x.selectOldestPeers(ctx, defaultReplicationFactor)
	if err != nil {
		x.logger.Errorf("Node (%s) failed to get cluster peers: %v", x.PeersAddress(), err)
		return err
	}

	if len(peers) == 0 {
		x.logger.Infof("Node (%s) found no cluster peers to persist state", x.PeersAddress())
		return nil
	}

	peerAddr := x.PeersAddress()
	totalPeers := len(peers)
	x.logger.Infof("Node (%s) replicating state to %d oldest peers", peerAddr, totalPeers)

	// Create a cancellable context for early termination after quorum
	rpcCtx, cancelRPCs := context.WithCancel(ctx)
	defer cancelRPCs()

	// Create a custom remoting client for replication with compression enabled
	remotingOpts := []remote.RemotingOption{
		remote.WithRemotingMaxReadFameSize(int(x.remoteConfig.MaxFrameSize())), // nolint
		remote.WithRemotingCompression(x.remoteConfig.Compression()),
		remote.WithRemotingContextPropagator(x.remoteConfig.ContextPropagator()),
	}

	if x.tlsInfo != nil {
		remotingOpts = append(remotingOpts, remote.WithRemotingTLS(x.tlsInfo.ClientConfig))
	}

	remoting := remote.NewRemoting(remotingOpts...)
	defer remoting.Close()

	// Channel to collect results from all goroutines
	// Each goroutine sends exactly one result (nil for success, error for failure)
	results := make(chan error, totalPeers)

	// Launch parallel RPCs to all selected peers
	for _, peer := range peers {
		peer := peer
		go func() {
			remoteClient := remoting.RemotingServiceClient(peer.Host, peer.RemotingPort)
			request := connect.NewRequest(&internalpb.PersistPeerStateRequest{
				PeerState: peerState,
			})

			_, err := remoteClient.PersistPeerState(rpcCtx, request)
			if err != nil {
				x.logger.Errorf("Node (%s) failed to persist peer state to remote Peer (%s:%d): %v",
					peerAddr, peer.Host, peer.RemotingPort, err)
				results <- err
				return
			}

			x.logger.Infof("Node (%s) successfully persisted peer state to remote Peer (%s:%d)",
				peerAddr, peer.Host, peer.RemotingPort)
			results <- nil // Success
		}()
	}

	// Collect results with early termination on quorum
	var (
		successCount int
		lastErr      error
	)

	for range totalPeers {
		select {
		case err := <-results:
			if err == nil {
				successCount++
				if successCount >= defaultReplicationQuorum {
					// Quorum reached - cancel remaining RPCs and return success
					cancelRPCs()
					x.logger.Infof("Node (%s) replication quorum reached (%d/%d peers)",
						peerAddr, successCount, totalPeers)
					return nil
				}
			} else if !errors.Is(err, context.Canceled) {
				// Don't count context cancellation as a real failure
				lastErr = err
			}
		case <-ctx.Done():
			// Parent context cancelled (e.g., shutdown timeout)
			x.logger.Warnf("Node (%s) replication interrupted: %v (successes: %d/%d)",
				peerAddr, ctx.Err(), successCount, totalPeers)
			if successCount > 0 {
				return nil // Partial success is acceptable
			}
			return fmt.Errorf("replication interrupted: %w", ctx.Err())
		}
	}

	// All RPCs completed without reaching quorum
	if successCount > 0 {
		x.logger.Warnf("Node (%s) partial replication: %d/%d peers acknowledged",
			peerAddr, successCount, totalPeers)
		return nil // Partial success is acceptable
	}

	// Complete failure
	if lastErr != nil {
		return fmt.Errorf("failed to replicate state to any peer: %w", lastErr)
	}

	return fmt.Errorf("failed to replicate state to any peer")
}

// selectOldestPeers returns up to k peers sorted by age (oldest first).
//
// This function is used during graceful shutdown to select peers for state
// replication. The oldest peers are chosen because:
//
//   - Leadership in this cluster is determined by node age (oldest = coordinator)
//   - The oldest peers are most likely to remain as leader or become leader
//     after the current node leaves
//   - Replicating to the oldest peers ensures the state is available to
//     whichever node becomes leader after topology changes
//
// Parameters:
//   - ctx: Context for the cluster membership query
//   - k: Maximum number of peers to return (typically 3 for quorum-based replication)
//
// Returns:
//   - Up to k peers sorted by CreatedAt ascending (oldest first)
//   - nil slice if no peers exist (single-node cluster)
//   - All available peers if fewer than k exist
//   - Error if cluster membership query fails
//
// Complexity: O(n log n) where n is the cluster size, which is negligible
// for typical cluster sizes (< 100 nodes).
func (x *actorSystem) selectOldestPeers(ctx context.Context, k int) ([]*cluster.Peer, error) {
	peers, err := x.cluster.Peers(ctx)
	if err != nil {
		return nil, err
	}

	n := len(peers)

	// Edge case: No peers (single node cluster)
	if n == 0 {
		x.logger.Info("No peers available, skipping state replication")
		return nil, nil
	}

	// Edge case: Fewer peers than k
	if n < k {
		x.logger.Infof("Only %d peers available, replicating to all", n)
		// Still sort for consistency
		sort.Slice(peers, func(i, j int) bool {
			return peers[i].CreatedAt < peers[j].CreatedAt
		})
		return peers, nil
	}

	// Normal case: k or more peers
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].CreatedAt < peers[j].CreatedAt
	})

	return peers[:k], nil
}

func computeEvictionCount(total, threshold uint64, totalEvictions, percentageToReturn int) int {
	requiredToEvict := int(total - threshold)
	percentageBasedEvict := (totalEvictions * percentageToReturn) / 100
	toReturn := maxInt(requiredToEvict, percentageBasedEvict)

	// Ensure at least 1 item is returned if there are actors over the threshold
	// and the percentage is non-zero, but the calculation resulted in 0.
	if toReturn == 0 && requiredToEvict > 0 && percentageToReturn > 0 {
		toReturn = 1
	}

	// Ensure toReturn does not exceed the total number of actors available
	if toReturn > totalEvictions {
		toReturn = totalEvictions
	}
	return toReturn
}

func runWithRetry(ctx context.Context, hook ShutdownHook, sys *actorSystem, recovery *ShutdownHookRecovery) error {
	retries, delay := recovery.Retry()
	retrier := retry.NewRetrier(retries, delay, delay)
	return retrier.RunContext(ctx, func(ctx context.Context) error {
		return hook.Execute(ctx, sys)
	})
}

// getServer creates an instance of http server
func getServer(ctx context.Context, address string) *stdhttp.Server {
	return &stdhttp.Server{
		Addr:              address,
		ReadTimeout:       5 * time.Minute,
		ReadHeaderTimeout: time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       1200 * time.Second,
		MaxHeaderBytes:    8 * 1024, // 8KiB
		BaseContext: func(_ net.Listener) context.Context {
			return ctx
		},
	}
}

// maxInt returns the maximum of two integers. Helper for calculation.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
