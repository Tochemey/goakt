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
	"errors"
	"fmt"
	"net"
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

	"github.com/flowchartsman/retry"
	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.uber.org/atomic"
	"go.uber.org/multierr"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/datacenter"
	"github.com/tochemey/goakt/v4/discovery"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/hash"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/chain"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/datacentercontroller"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/locker"
	"github.com/tochemey/goakt/v4/internal/metric"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/pointer"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/strconvx"
	"github.com/tochemey/goakt/v4/internal/ticker"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/internal/validation"
	"github.com/tochemey/goakt/v4/internal/xsync"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/memory"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/remote"
	sup "github.com/tochemey/goakt/v4/supervisor"
	gtls "github.com/tochemey/goakt/v4/tls"
)

const (
	defaultReplicationFactor = 3
	defaultReplicationQuorum = 2 // Wait for 2-of-3 acks

	// correlatedDepartureWindow bounds how long a node departure is remembered
	// when detecting a correlated (multi-node) failure. Departures within this
	// window are treated as concurrent: once replicaCount of them accumulate,
	// the registry repair runs even at replicaCount > 1 because olric's backups
	// may not have survived the burst.
	//
	// It must comfortably exceed the time olric needs to re-replicate a
	// partition after a loss, otherwise sequential failures spaced just past the
	// window each look isolated and a partition that lost every replica is never
	// repaired (permanent registry loss). olric drives re-replication from its
	// routing table, which refreshes on the order of a minute by default
	// (internal/cluster's routingTableInterval), so this window is set to a
	// small multiple of that cadence rather than a few seconds.
	//
	// The bias is deliberately toward safety: too long only costs redundant
	// re-puts of each survivor's own local entries during a slow rolling restart
	// (idempotent Puts, O(local actors) each), whereas too short risks losing
	// registry entries for actors that are still alive on survivors.
	//
	// This constant is the floor, sized for the default cadence. When the user
	// stretches the routing-table refresh via WithClusterStateSyncInterval the
	// effective window scales with it (twice the configured interval; see
	// NewActorSystem), so a slower cadence never outlives the window.
	correlatedDepartureWindow = 2 * time.Minute
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
	// Actors retrieves all active actors visible to this node.
	// Local actors are returned as live PIDs. When cluster mode is enabled,
	// actors on peer nodes are returned as lightweight remote PIDs that carry
	// only the address and a remoting handle, routing all messaging through
	// the remoting layer. Use pid.IsLocal() / pid.IsRemote() to distinguish them.
	// The timeout bounds the cluster scan; it is ignored when not in cluster mode.
	// Use this method cautiously as the cluster scan may impact system performance.
	Actors(ctx context.Context, timeout time.Duration) ([]*PID, error)
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
	// This method is location-transparent: with options such as WithHostAndPort, the actor
	// may be spawned on a remote node when remoting is enabled; otherwise it is created locally.
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
	// Note: For cluster placement strategies (e.g., Random, LeastLoad), use SpawnOn instead.
	Spawn(ctx context.Context, name string, actor Actor, opts ...SpawnOption) (*PID, error)
	// SpawnOn creates and starts an actor locally, on another node in the current cluster,
	// or on a node in a different data center, depending on options and actor system configuration.
	//
	// # Cross–data center placement
	//
	// When opts include WithDataCenter, the actor is spawned on a node in that data center.
	// Placement is a random node among the target DC's advertised remoting endpoints: SpawnOn
	// calls spawnOnDatacenter, which requires DataCenterReady(), looks up the target DC by the
	// given datacenter.DataCenter's ID() in the controller's active records, then sends a
	// RemoteSpawn to one of that record's Endpoints chosen at random. There is no leader
	// selection—which node runs the actor depends entirely on which addresses the target DC
	// registered (via datacenter.Config.Endpoints). If that DC advertises only its leader, every
	// cross-DC spawn goes to the leader; if it advertises all nodes, the actor is placed on a
	// random node in that DC. The actor kind must be registered on the target data center's
	// actor systems. See WithDataCenter and spawnOnDatacenter for details and errors
	// (e.g. ErrDataCenterNotReady, ErrDataCenterStaleRecords, ErrDataCenterRecordNotFound).
	//
	// # Same–data center placement
	//
	// When no target data center is specified, behavior depends on cluster mode:
	//
	//   - In cluster mode, the actor may be placed on any node in the local cluster according to
	//     the placement strategy and role filter. Supported strategies:
	//     RoundRobin, Random, Local, LeastLoad. Placement uses cluster.Members and thus stays
	//     within the current data center when the architecture is one-cluster-per-DC.
	//   - In non-cluster mode, the actor is created on the local actor system, like Spawn.
	//
	// Unlike Spawn, SpawnOn does not return a PID. Use ActorOf to resolve the actor's PID or
	// Address after it has been successfully created.
	//
	// Parameters:
	//   - ctx: Context for cancellation and timeouts during the spawn process.
	//   - name: A globally unique name for the actor in the cluster or across data centers.
	//   - actor: An instance implementing the Actor interface.
	//   - opts: Optional SpawnOptions: placement strategy, WithDataCenter for cross-DC spawn,
	//     role, dependencies, mailbox, supervisor strategy, etc.
	//
	// Returns:
	//   - error: An error if the actor could not be spawned (e.g., name conflict, network or
	//     remoting failure, misconfiguration; or, when using WithDataCenter, data center
	//     not ready, stale records, or target DC not found).
	//
	// Example (same-DC, cluster placement):
	//
	//	err := system.SpawnOn(ctx, "actor-1", NewCartActor(), WithPlacement(Random))
	//	if err != nil {
	//	    log.Fatalf("Failed to spawn actor: %v", err)
	//	}
	//
	// Example (cross-DC):
	//
	//	dc := &datacenter.DataCenter{Name: "dc-west", Region: "us-west-2"}
	//	err := system.SpawnOn(ctx, "actor-1", NewCartActor(), WithDataCenter(dc))
	//	if err != nil {
	//	    log.Fatalf("Failed to spawn actor in dc-west: %v", err)
	//	}
	//
	// ⚠️ Note: The created actor uses the default mailbox from the actor system unless overridden in opts.
	SpawnOn(ctx context.Context, name string, actor Actor, opts ...SpawnOption) (*PID, error)
	// SpawnFromFunc creates and starts an actor whose behavior is defined by the given receive function,
	// generating a unique name automatically. This is a lightweight alternative to implementing the Actor
	// interface when only message handling logic is required and the actor does not need a stable, well-known name.
	//
	// The receiveFunc is invoked for every message the actor receives. Optional lifecycle hooks and a custom
	// mailbox can be supplied through opts:
	//   - [WithPreStart] runs a hook before the actor starts processing messages.
	//   - [WithPostStop] runs a hook after the actor has stopped.
	//   - [WithFuncMailbox] overrides the default mailbox.
	//
	// The created actor is not relocatable: when running in a cluster it will not be redeployed to another node
	// if its host node leaves the cluster.
	//
	// It returns the [PID] of the spawned actor, or an error if the actor system is not running, the
	// preconditions fail, or the actor cannot be initialized.
	SpawnFromFunc(ctx context.Context, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error)
	// SpawnNamedFromFunc creates and starts an actor whose behavior is defined by the given receive function,
	// registering it under the provided name. This is a lightweight alternative to implementing the Actor
	// interface when only message handling logic is required.
	//
	// The receiveFunc is invoked for every message the actor receives. Optional lifecycle hooks and a custom
	// mailbox can be supplied through opts:
	//   - [WithPreStart] runs a hook before the actor starts processing messages.
	//   - [WithPostStop] runs a hook after the actor has stopped.
	//   - [WithFuncMailbox] overrides the default mailbox.
	//
	// The name must be unique within the actor system. If an actor with the same name already exists and is
	// running, that actor is returned and no new actor is created.
	//
	// The created actor is not relocatable: when running in a cluster it will not be redeployed to another node
	// if its host node leaves the cluster.
	//
	// It returns the [PID] of the spawned actor, or an error if the actor system is not running, the
	// preconditions fail, or the actor cannot be initialized.
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
	SpawnSingleton(ctx context.Context, name string, actor Actor, opts ...ClusterSingletonOption) (*PID, error)
	// Kill stops a given actor in the system either locally or on a remote node(when clustering is enabled)
	Kill(ctx context.Context, name string) error
	// ReSpawn recreates a given actor in the system.
	//
	// During restart all messages that are in the mailbox and not yet processed will be ignored.
	// Only the direct alive children of the given actor will be shutdown and respawned with their initial state.
	// Bear in mind that restarting an actor will reinitialize the actor to initial state.
	// In case any of the direct child restart fails the given actor will not be started at all.
	//
	// This method is location-transparent: it works identically whether the actor is local or on a
	// remote node (when clustering/remoting is enabled).
	ReSpawn(ctx context.Context, name string) (*PID, error)
	// NumActors returns the total number of active actors on a given running node.
	// This does not account for the total number of actors in the cluster
	NumActors() uint64
	// ActorOf retrieves an existing actor within the local system or across the cluster if clustering is enabled.
	//
	// If the actor is found locally, its live PID is returned. If the actor resides on a remote node (cluster
	// mode enabled), a lightweight remote PID is returned; it carries only the actor's address and a remoting
	// handle, and routes all messaging operations through the remoting layer. If the actor is not found, an
	// error of type "actor not found" is returned.
	//
	// Use pid.IsLocal() / pid.IsRemote() to distinguish the two cases when location matters.
	ActorOf(ctx context.Context, actorName string) (*PID, error)
	// ActorExists checks whether an actor with the given name exists in the system,
	// either locally, or on another node in the cluster if clustering is enabled.
	ActorExists(ctx context.Context, actorName string) (exists bool, err error)
	// InCluster states whether the actor system has started within a cluster of nodes
	InCluster() bool
	// Partition returns the partition where a given actor is located
	Partition(actorName string) uint64
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
	// This method is location-transparent: it works identically whether the target actor is local or on a
	// remote node (when clustering/remoting is enabled).
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
	ScheduleOnce(ctx context.Context, message any, pid *PID, delay time.Duration, opts ...ScheduleOption) error
	// Schedule schedules a recurring message to be delivered to the specified actor (PID) at a fixed interval.
	//
	// This function sets up a message to be sent repeatedly to the target actor, with each delivery occurring
	// after the specified interval. The scheduling continues until explicitly canceled or if the actor is no longer available.
	//
	// This method is location-transparent: it works identically whether the target actor is local or on a
	// remote node (when clustering/remoting is enabled).
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
	Schedule(ctx context.Context, message any, pid *PID, interval time.Duration, opts ...ScheduleOption) error
	// ScheduleWithCron schedules a message to be delivered to the specified actor (PID) using a cron expression.
	//
	// This method enables flexible time-based scheduling using standard cron syntax, allowing you to specify complex recurring schedules.
	// The message will be sent to the target actor according to the schedule defined by the cron expression.
	//
	// This method is location-transparent: it works identically whether the target actor is local or on a
	// remote node (when clustering/remoting is enabled).
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
	//   - In cluster mode the message is delivered exactly once per trigger tick across the
	//     cluster, WithReference is required (the call is rejected with
	//     ErrScheduleReferenceRequired otherwise), and the cron expression is evaluated in
	//     UTC so every node computes the same tick instants. Outside cluster mode the
	//     expression is evaluated in the process's local timezone.
	//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the scheduled message.
	//   - If no reference is set, an automatic one will be generated internally, which may not be easily retrievable for future operations.
	//   - The cron expression must follow the format supported by the scheduler (typically 6 or 5 fields depending on implementation).
	ScheduleWithCron(ctx context.Context, message any, pid *PID, cronExpression string, opts ...ScheduleOption) error
	// CancelSchedule cancels the schedule registered under the given reference, stopping any
	// future deliveries for it.
	//
	// In cluster mode each node runs its own copy of the schedule, so CancelSchedule only
	// cancels the copy on the node it is called on; call it on every node that registered the
	// schedule to stop it cluster-wide.
	//
	// It returns an error if no schedule is registered under the reference on this node (for
	// example when it was never scheduled, already delivered, or already canceled).
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
	// ListSchedules returns a read-only snapshot of every schedule currently known to the scheduler:
	// its reference and the target actor path.
	//
	// It has no effect on the schedules themselves. A schedule stops appearing once it has been
	// canceled via CancelSchedule or, for one-shot schedules created via ScheduleOnce, once it has
	// fired and been delivered.
	ListSchedules() []ScheduleInfo
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
	// TopicStats returns a snapshot of the given topic's subscription state across
	// the cluster. In non-clustered mode, TopicInstanceCount is 0 or 1 based on
	// local subscribers.
	TopicStats(ctx context.Context, topic string, timeout time.Duration) (*TopicStats, error)
	// Replicator returns the PID of the local CRDT Replicator system actor.
	// Returns nil if CRDT replication is not enabled via ClusterConfig.WithCRDT.
	Replicator() *PID
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
	//
	// Deprecated: use the package-level GrainOf function instead. GrainOf derives the grain kind
	// from its type parameter, requires no factory, and only constructs the grain (as a zero value)
	// when local activation is needed. The factory-based path remains functional but will be
	// removed in a future major release.
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
	AskGrain(ctx context.Context, identity *GrainIdentity, message any, timeout time.Duration) (response any, err error)
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
	TellGrain(ctx context.Context, identity *GrainIdentity, message any) error
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
	//       // Use p (e.g., p.Path(), p.Host(), p.Port(), etc.)
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
	//       system.Logger().Infof("current cluster leader is at %s", leader.RemotingAddress())
	//   } else {
	//       system.Logger().Info("no cluster leader is currently elected")
	//   }
	Leader(ctx context.Context) (leader *remote.Peer, err error)
	// DataCenterReady reports whether the multi-datacenter controller is operational.
	//
	// The controller is considered ready when:
	//   - Multi-DC mode is enabled (cluster mode with a datacenter config)
	//   - The controller has started successfully
	//   - The cache has been refreshed at least once
	//
	// Returns true immediately if multi-DC mode is not enabled, as there is no
	// DC controller to wait for.
	//
	// This method is intended for use in readiness probes (e.g., Kubernetes readinessProbe)
	// to gate traffic until the controller has a usable view of active data centers.
	//
	// Note: Ready does not guarantee the cache is fresh; the controller uses
	// MaxCacheStaleness internally to determine routing behavior.
	DataCenterReady() bool
	// DataCenterLastRefresh returns the time of the last successful datacenter cache refresh.
	//
	// Returns the zero time if:
	//   - Multi-DC mode is not enabled
	//   - The cache has never been refreshed
	//
	// This can be used for debugging, monitoring, or custom readiness logic that
	// requires more granular control than DataCenterReady provides.
	DataCenterLastRefresh() time.Time
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
	handleRemoteAsk(ctx context.Context, to *PID, message any, timeout time.Duration) (response any, err error)
	// handleRemoteTell handles an asynchronous message to an actor. The
	// caller may pass a pre-resolved sender PID; callers that don't have one
	// (or only have an ad-hoc NoSender) can pass the system's NoSender and
	// let toReceiveContext materialise the sender from the wire message.
	handleRemoteTell(ctx context.Context, from *PID, to *PID, message any) error
	// putActorOnCluster sets actor in the actor system actors registry
	putActorOnCluster(actor *PID) error
	// getCluster returns the cluster engine
	getCluster() cluster.Cluster
	// tree returns the actors tree
	tree() *tree
	// getRemoteWatches returns the remote watch registry
	getRemoteWatches() *remoteWatchRegistry
	// getRemoteWatchTimeout returns the deadline applied to remote
	// PID.Watch / PID.UnWatch RPCs
	getRemoteWatchTimeout() time.Duration

	beginRelocation(peerAddress string, peerState *internalpb.PeerState) bool
	relocationJob(peerAddress string) (*internalpb.PeerState, bool)
	endRelocation(peerAddress string)
	// recordRelocationMetrics records the duration and relocated/failed item
	// counts of a single departed node's relocation. It is a no-op when metrics
	// are disabled.
	recordRelocationMetrics(ctx context.Context, departed string, duration time.Duration, relocated, failed int)
	// reportAbortedRelocation applies the shared accounting for a relocation
	// that could not run to completion (peer enumeration failure, worker spawn
	// failure or abnormal worker death). It releases lazy grain directory
	// entries locally and publishes a RelocationFailed event plus failure
	// metrics using the same rule as the normal per-item path.
	reportAbortedRelocation(ctx context.Context, pid *PID, peersAddress string, peerState *internalpb.PeerState, elapsed time.Duration, cause error)
	// isEndpointRelocating reports whether addr resolves to a remoting endpoint
	// whose host recently left the cluster and is still within its handoff
	// window.
	isEndpointRelocating(addr *address.Address) bool
	// relocationInFlight reports whether any departed endpoint is still within
	// its handoff window.
	relocationInFlight() bool
	// recordRelocationHandoff records that a name-based send was buffered and
	// retried while its target was relocating. It is a no-op when metrics are
	// disabled.
	recordRelocationHandoff(ctx context.Context)
	// getNodeRoles returns the roles advertised by the local node, used to
	// decide whether the leader is an eligible relocation target for a
	// role-constrained actor.
	getNodeRoles() []string
	// clusterReadTimeout returns the user-configured cluster read timeout, or
	// fallback when clustering is not configured or the value is unset.
	clusterReadTimeout(fallback time.Duration) time.Duration
	recreateActorFromWire(ctx context.Context, props *internalpb.Actor, departedNode string) error
	recreateGrainFromWire(ctx context.Context, grain *internalpb.Grain, departedNode string) error
	releaseGrainForLazyRelocation(ctx context.Context, grain *internalpb.Grain, departedNode string) error
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
	getRemoting() remoteclient.Client
	getGrains() *xsync.Map[string, *grainPID]
	recreateGrain(ctx context.Context, props *internalpb.Grain) error
	grainOf(ctx context.Context, grainType Grain, name string, opts ...GrainOption) (*GrainIdentity, error)
	decreaseActorsCounter()
	increaseActorsCounter()
	passivationManager() *passivationManager
	getDispatcher() *dispatcher
	getClusterStore() cluster.Store
	getDataCenterController() *datacentercontroller.Controller
	getDataCenterConfig() *datacenter.Config
}

// ActorSystem represent a collection of actors on a given node
// Only a single instance of the ActorSystem can be created on a given node
type actorSystem struct {
	_ locker.NoCopy
	// hold the actors tree in the system
	actors *tree
	// tracks remote watch relationships separately from the pid tree.
	// Sparse: empty for actor systems that do not participate in remote
	// watching.
	remoteWatches *remoteWatchRegistry

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
	// Canonical "host:port" of the remoting bind address, precomputed once
	// at remoting startup so per-message handlers do not rebuild it.
	remoteHostPort string
	// Specifies the deadline applied to remote PID.Watch / PID.UnWatch RPCs.
	// The default value is DefaultRemoteWatchTimeout (5s).
	remoteWatchTimeout time.Duration
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
	remoting        remoteclient.Client

	// Specifies the remoting server
	remoteServer *inet.ProtoServer // Proto TCP server
	remoteConfig *remote.Config

	// coalescedFailureQueue receives whole-batch failures surfaced by the
	// outbound RemoteTell coalescer. A dedicated goroutine drains it and
	// publishes each failed message to the local dead-letter actor, keeping
	// the coalescer's writer goroutine unblocked by the fan-out work.
	coalescedFailureQueue chan coalescedFailure
	coalescedFailureWG    sync.WaitGroup

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

	// dispatcher drives the shared worker pool that processes actor
	// mailboxes. Created in Start, torn down non-blockingly in shutdown.
	dispatcher *dispatcher

	// dispatcherThroughput is the per-turn message budget applied to the
	// dispatcher at construction time. Set via WithThroughputBudget.
	dispatcherThroughput int

	// manages passivation deadlines without per-actor goroutines
	passivator *passivationManager

	defaultSupervisor          *sup.Supervisor
	defaultPassivationStrategy passivation.Strategy

	registry   types.Registry
	reflection *reflection

	clusterConfig *ClusterConfig

	relocator        *PID
	rootGuardian     *PID
	userGuardian     *PID
	systemGuardian   *PID
	deathWatch       *PID
	deadletter       *PID
	singletonManager *PID
	topicActor       *PID
	replicator       *PID
	noSender         *PID
	peerStatesWriter *PID

	startedAt atomic.Int64
	// relocationJobs tracks the in-flight relocations by departed peer address,
	// each holding the departed node's peer state snapshot. An entry is added
	// when the leader dispatches a rebalance and removed on completion, so a
	// node address that departs again after a completed relocation is
	// rebalanced again while duplicate NodeLeft events for an in-flight
	// relocation are ignored.
	relocationJobs       map[string]*internalpb.PeerState
	relocationJobsLocker sync.Mutex

	// peerRemotingPorts caches each known peer's remoting port keyed by its
	// peers address (host:peersPort). It exists so crash recovery can identify
	// a departed node's registry records: a NodeLeft event only carries the
	// peers address, but actor/grain records key on the remoting address, and a
	// crashed node is already gone from membership when the event arrives. The
	// cache is populated from live membership on cluster events and consulted on
	// NodeLeft when no graceful-shutdown snapshot exists.
	peerRemotingPorts *xsync.Map[string, int]

	// relocatingEndpoints records the remoting endpoints (host:port) of nodes
	// that recently left the cluster while relocation is enabled, each retained
	// for a bounded handoff window. Senders consult it to mask the window in
	// which a departed node's actors are being recreated on a survivor: a send
	// routed by name to such an endpoint is briefly retried (re-resolving the
	// target) instead of failing with a connection error or a spurious "actor
	// not found". Entries expire on their own, so steady-state sends never pay
	// for this.
	relocatingEndpoints *xsync.TTLMap[string, types.Unit]

	// recentDepartures records the peers addresses of nodes that left within the
	// correlated-departure window, each retained for that window. It lets the
	// registry repair-on-departure detect a correlated (multi-node) failure: at
	// replicaCount > 1 the repair is normally skipped because olric promotes
	// backups, but olric only tolerates replicaCount-1 simultaneous losses, so
	// once this many nodes depart together the repair must still run to restore
	// entries whose every replica was lost. Entries expire on their own.
	recentDepartures *xsync.TTLMap[string, types.Unit]

	shutdownHooks []ShutdownHook

	actorsCounter      atomic.Uint64
	deadlettersCounter atomic.Uint64

	tlsInfo           *gtls.Info
	pubsubEnabled     atomic.Bool
	relocationEnabled atomic.Bool
	// messageRetention bounds how long the pub/sub topic actor remembers a
	// delivered message identifier in order to suppress duplicate deliveries.
	messageRetention time.Duration
	extensions       *xsync.Map[string, extension.Extension]

	shuttingDown atomic.Bool
	// shutdownSignal is closed once at the start of shutdown() or
	// startupCleanup(). Producers of actorsQueue / grainsQueue select
	// against it to avoid racing a concurrent close of those queues.
	// Guarded by the shuttingDown CAS so close runs exactly once.
	shutdownSignal chan types.Unit
	// drainers tracks the replicateActors / replicateGrains goroutines.
	// shutdown waits on it so the drainers finish draining the cluster
	// queues before reset() clears the rest of the system state.
	drainers    sync.WaitGroup
	grainsQueue chan *internalpb.Grain
	grains      *xsync.Map[string, *grainPID]
	// Caches parsed sender addresses of inbound remote messages so the hot
	// receive path skips address.Parse. Addresses are immutable, so entries
	// never go stale; the cache is flushed wholesale when it reaches
	// remoteSenderAddressCacheCap to stay bounded.
	remoteSenderAddresses *xsync.Map[string, *address.Address]
	grainBarrier          *grainActivationBarrier
	grainActivation       singleflight.Group
	evictionStrategy      *EvictionStrategy
	evictionInterval      time.Duration
	evictionStopSig       chan types.Unit

	metricProvider *metric.Provider
	// relocationMetric holds the synchronous relocation instruments. It is nil
	// unless OpenTelemetry metrics are enabled, so all recording is guarded.
	relocationMetric *metric.RelocationMetric

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
	_ ActorSystem = (*actorSystem)(nil)
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
		actorsQueue:           make(chan *internalpb.Actor, 10),
		name:                  name,
		logger:                log.NewZap(log.ErrorLevel, os.Stderr),
		actorInitMaxRetries:   DefaultInitMaxRetries,
		shutdownTimeout:       DefaultShutdownTimeout,
		eventsStream:          eventstream.New(),
		partitionHasher:       hash.DefaultHasher(),
		actorInitTimeout:      DefaultInitTimeout,
		eventsQueue:           make(chan *cluster.Event, 1),
		registry:              types.NewRegistry(),
		remoteConfig:          remote.DefaultConfig(),
		actors:                newTree(),
		remoteWatches:         newRemoteWatchRegistry(),
		remoteWatchTimeout:    DefaultRemoteWatchTimeout,
		shutdownHooks:         make([]ShutdownHook, 0),
		relocationJobs:        make(map[string]*internalpb.PeerState),
		peerRemotingPorts:     xsync.NewMap[string, int](),
		relocatingEndpoints:   xsync.NewTTLMap[string, types.Unit](relocationHandoffWindow),
		recentDepartures:      xsync.NewTTLMap[string, types.Unit](correlatedDepartureWindow),
		topicActor:            nil,
		extensions:            xsync.NewMap[string, extension.Extension](),
		grainsQueue:           make(chan *internalpb.Grain, 10),
		shutdownSignal:        make(chan types.Unit),
		grains:                xsync.NewMap[string, *grainPID](),
		remoteSenderAddresses: xsync.NewMap[string, *address.Address](),
		askTimeout:            DefaultAskTimeout,
		messageRetention:      DefaultMessageRetention,
		evictionStopSig:       make(chan types.Unit, 1),
		dispatcherThroughput:  dispatcherThroughput,
	}

	system.startedAt.Store(0)
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

	// The correlated-departure window must cover olric's re-replication time,
	// which is driven by the routing-table refresh cadence. That cadence is
	// user-configurable (WithClusterStateSyncInterval), so when it is stretched
	// beyond the default the window scales with it, keeping the compile-time
	// constant as a floor rather than a silent cap (see correlatedDepartureWindow).
	if system.clusterConfig != nil {
		if window := 2 * system.clusterConfig.clusterStateSyncInterval; window > correlatedDepartureWindow {
			system.recentDepartures = xsync.NewTTLMap[string, types.Unit](window)
		}
	}

	// build the dispatcher after options so tuning knobs like WithThroughputBudget take effect.
	system.dispatcher = newDispatcher(dispatcherWorkerCount(), system.dispatcherThroughput)

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
		x.logger.Error(err)
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
		x.logger.Infof("received interrupt signal=%s to shutdown", sig.String())

		if err := chain.
			New(chain.WithFailFast(), chain.WithContext(ctx)).
			AddContextRunner(stophook).
			AddContextRunner(x.Stop).
			Run(); err != nil {
			x.logger.Error(err)
			os.Exit(1)
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

	x.logger.Infof("starting actor system=%s os=%s arch=%s", x.name, runtime.GOOS, runtime.GOARCH)
	x.starting.Store(true)

	// (Re)create shutdownSignal here, the only point where no cluster
	// producer or replicate drainer is alive yet. Recreating it during
	// teardown (in reset()) would race the concurrent reads in
	// putActorOnCluster / putGrainOnCluster. A fresh channel per start
	// lets the next shutdown close it exactly once without double-closing
	// the one a prior teardown already closed.
	x.shutdownSignal = make(chan types.Unit)

	x.scheduler = newScheduler(x.logger, x.shutdownTimeout, x)

	x.dispatcher.start()

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
		AddContextRunner(x.startRemoteServer).
		AddContextRunner(x.startCluster).
		AddContextRunner(x.startDataCenterController).
		AddContextRunner(x.startDataCenterLeaderWatch).
		Run(); err != nil {
		x.startupCleanup(ctx)
		return err
	}

	x.startMessagesScheduler(ctx)

	if err := x.spawnReplicator(ctx); err != nil {
		var shutdownErr error
		if x.replicator != nil {
			shutdownErr = x.replicator.Shutdown(ctx)
		}
		x.scheduler.Stop(ctx)
		x.startupCleanup(ctx)
		return errors.Join(err, shutdownErr)
	}

	if x.passivator != nil {
		x.passivator.Start(ctx)
	}
	x.startEviction()
	x.started.Store(true)
	x.starting.Store(false)
	x.startedAt.Store(time.Now().Unix())

	if err := x.registerMetrics(); err != nil {
		x.logger.Errorf("failed to register actor system metrics: %v (hint: check metrics endpoint config, port availability)", err)
		if stopErr := x.shutdown(ctx); stopErr != nil {
			return errors.Join(err, stopErr)
		}

		return err
	}

	x.logger.Infof("actor system=%s started", x.name)
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

	return x.coordinatorPeer(ctx)
}

// coordinatorPeer returns the member currently flagged as cluster coordinator
// by the underlying engine, or nil when no single coordinator is visible (for
// example during an election window). Unlike Leader it does not require the
// actor system to be marked running, so it is safe to call during startup and
// from the cluster events loop. Using the authoritative coordinator flag keeps
// Leader, IsLeader and the LeaderChanged event consistent with one another.
func (x *actorSystem) coordinatorPeer(ctx context.Context) (*remote.Peer, error) {
	members, err := x.cluster.Members(ctx)
	if err != nil {
		return nil, err
	}

	for _, member := range members {
		if member.Coordinator {
			return cluster.ToRemotePeer(member), nil
		}
	}

	return nil, nil
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
// This method is location-transparent: it works identically whether the target actor is local or on a
// remote node (when clustering/remoting is enabled).
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
func (x *actorSystem) Schedule(_ context.Context, message any, pid *PID, interval time.Duration, opts ...ScheduleOption) error {
	return x.scheduler.Schedule(message, pid, interval, opts...)
}

// ScheduleOnce schedules a one-time delivery of a message to the specified actor (PID) after a given delay.
//
// The message will be sent exactly once to the target actor after the specified duration has elapsed.
// This is a fire-and-forget scheduling mechanism — once delivered, the message will not be retried or repeated.
//
// This method is location-transparent: it works identically whether the target actor is local or on a
// remote node (when clustering/remoting is enabled).
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
func (x *actorSystem) ScheduleOnce(_ context.Context, message any, pid *PID, interval time.Duration, opts ...ScheduleOption) error {
	return x.scheduler.ScheduleOnce(message, pid, interval, opts...)
}

// ScheduleWithCron schedules a message to be delivered to the specified actor (PID) using a cron expression.
//
// This method enables flexible time-based scheduling using standard cron syntax, allowing you to specify complex recurring schedules.
// The message will be sent to the target actor according to the schedule defined by the cron expression.
//
// This method is location-transparent: it works identically whether the target actor is local or on a
// remote node (when clustering/remoting is enabled).
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
//   - In cluster mode the message is delivered exactly once per trigger tick across the
//     cluster, WithReference is required (the call is rejected with
//     ErrScheduleReferenceRequired otherwise), and the cron expression is evaluated in UTC
//     so every node computes the same tick instants. Outside cluster mode the expression is
//     evaluated in the process's local timezone.
//   - It's strongly recommended to set a unique reference ID using WithReference if you plan to cancel, pause, or resume the scheduled message.
//   - If no reference is set, an automatic one will be generated internally, which may not be easily retrievable for future operations.
//   - The cron expression must follow the format supported by the scheduler (typically 6 or 5 fields depending on implementation).
func (x *actorSystem) ScheduleWithCron(_ context.Context, message any, pid *PID, cronExpression string, opts ...ScheduleOption) error {
	return x.scheduler.ScheduleWithCron(message, pid, cronExpression, opts...)
}

// CancelSchedule cancels the schedule registered under the given reference, stopping any
// future deliveries for it.
//
// In cluster mode each node runs its own copy of the schedule, so CancelSchedule only cancels
// the copy on the node it is called on; call it on every node that registered the schedule to
// stop it cluster-wide.
//
// It returns an error if no schedule is registered under the reference on this node (for
// example when it was never scheduled, already delivered, or already canceled).
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

// ListSchedules returns a read-only snapshot of every schedule currently known to the scheduler:
// its reference and the target actor path.
//
// It has no effect on the schedules themselves. A schedule stops appearing once it has been
// canceled via CancelSchedule or, for one-shot schedules created via ScheduleOnce, once it has
// fired and been delivered.
func (x *actorSystem) ListSchedules() []ScheduleInfo {
	return x.scheduler.ListSchedules()
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

// Partition returns the partition where a given actor is located
func (x *actorSystem) Partition(actorName string) uint64 {
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
				x.logger.Warnf("actor=%s not found", name)
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

// ReSpawn recreates a given actor in the system.
//
// During restart all messages that are in the mailbox and not yet processed will be ignored.
// Only the direct alive children of the given actor will be shutdown and respawned with their initial state.
// Bear in mind that restarting an actor will reinitialize the actor to initial state.
// In case any of the direct child restart fails the given actor will not be started at all.
//
// This method is location-transparent: it works identically whether the actor is local or on a
// remote node (when clustering/remoting is enabled).
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

	if x.InCluster() {
		pid, err := x.ActorOf(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch remote actor=%s: %w", name, err)
		}

		p := pid.Path()
		if _, err := x.remoting.RemoteReSpawn(ctx, p.Host(), p.Port(), name); err != nil {
			return nil, fmt.Errorf("failed to re-spawn remote actor=%s: %w", name, err)
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
// localActors returns the list of user actors that are alive on this node.
// System actors (those with reserved names) are excluded.
// This is an internal helper; use Actors for the full cluster-aware view.
func (x *actorSystem) localActors() []*PID {
	x.locker.RLock()
	nodes := x.actors.nodes()
	x.locker.RUnlock()
	var actors []*PID
	for _, node := range nodes {
		pid := node.value()
		if pid == nil {
			continue
		}
		if !isSystemName(pid.Name()) {
			actors = append(actors, pid)
		}
	}
	return actors
}

// Actors retrieves all active actors visible to this node.
// Local actors are returned as live PIDs. When cluster mode is enabled,
// actors on peer nodes are returned as lightweight remote PIDs that carry
// only the address and a remoting handle, routing all messaging through
// the remoting layer. Use pid.IsLocal() / pid.IsRemote() to distinguish them.
// The timeout bounds the cluster scan; it is ignored when not in cluster mode.
// Use this method cautiously as the cluster scan may impact system performance.
func (x *actorSystem) Actors(ctx context.Context, timeout time.Duration) ([]*PID, error) {
	local := x.localActors()
	pids := make([]*PID, len(local))
	copy(pids, local)

	seen := make(map[string]types.Unit, len(local))
	for _, pid := range local {
		seen[pathString(pid.Path())] = types.Unit{}
	}

	if x.InCluster() {
		actors, err := x.getCluster().Actors(ctx, timeout)
		if err != nil {
			return nil, err
		}

		for _, actor := range actors {
			addr, err := address.Parse(actor.GetAddress())
			if err != nil {
				continue
			}
			if _, ok := seen[addr.String()]; !ok {
				pids = append(pids, newRemotePID(addr, x.remoting))
				seen[addr.String()] = types.Unit{}
			}
		}
	}

	return pids, nil
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
// If the actor is found locally, its live PID is returned. If the actor resides on a remote node (cluster mode
// enabled), a lightweight remote PID is returned; it carries only the actor's address and a remoting handle,
// and routes all messaging operations through the remoting layer. If the actor is not found, an error of type
// "actor not found" is returned.
//
// Use pid.IsLocal() / pid.IsRemote() to distinguish the two cases when location matters.
func (x *actorSystem) ActorOf(ctx context.Context, actorName string) (*PID, error) {
	if !x.Running() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	// user should not query system actors
	if isSystemName(actorName) {
		return nil, gerrors.NewErrActorNotFound(actorName)
	}

	// Fast path: local actor lookup uses only the tree's internal RWMutex.
	// The tree reference (x.actors) is immutable after construction, so no
	// system lock is needed. This avoids the double-lock contention that
	// dominated SendAsync/SendSync throughput under high parallelism.
	if pidnode, ok := x.actors.nodeByName(actorName); ok {
		pid := pidnode.value()
		if pid.IsStopping() {
			return nil, gerrors.NewErrActorNotFound(actorName)
		}
		return pid, nil
	}

	// Slow path: actor not found locally. Acquire the system lock for
	// cluster and remote lookups which access mutable system state.
	x.locker.RLock()

	// check in the cluster
	if x.clusterEnabled.Load() {
		actor, err := x.cluster.GetActor(ctx, actorName)
		if err != nil {
			if errors.Is(err, cluster.ErrActorNotFound) {
				x.logger.Warnf("actor=%s not found", actorName)
				x.locker.RUnlock()
				return nil, gerrors.NewErrActorNotFound(actorName)
			}

			x.locker.RUnlock()
			return nil, fmt.Errorf("failed to fetch remote actor=%s: %w", actorName, err)
		}

		// Capture remoting before releasing the lock.
		remoting := x.remoting
		x.locker.RUnlock()

		addr, err := address.Parse(actor.GetAddress())
		if err != nil {
			return nil, err
		}
		return newRemotePID(addr, remoting), nil
	}

	if x.remotingEnabled.Load() {
		x.locker.RUnlock()
		return nil, gerrors.ErrMethodCallNotAllowed
	}

	x.logger.Warnf("actor=%s not found", actorName)
	x.locker.RUnlock()
	return nil, gerrors.NewErrActorNotFound(actorName)
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

// TopicStats returns a snapshot of the given topic's subscription state across
// the cluster. In non-clustered mode, TopicInstanceCount is 0 or 1 based on
// local subscribers.
func (x *actorSystem) TopicStats(ctx context.Context, topic string, timeout time.Duration) (*TopicStats, error) {
	topicActor := x.TopicActor()
	if topicActor == nil {
		return nil, gerrors.ErrTopicActorNotStarted
	}

	reply, err := x.getSystemGuardian().Ask(ctx, topicActor, &getTopicStats{topic: topic}, timeout)
	if err != nil {
		return nil, err
	}

	stats, ok := reply.(*TopicStats)
	if !ok || stats == nil {
		return nil, gerrors.ErrInvalidResponse
	}

	return stats, nil
}

// Replicator returns the PID of the local CRDT Replicator system actor.
// Returns nil if CRDT replication is not enabled via ClusterConfig.WithCRDT.
func (x *actorSystem) Replicator() *PID {
	x.locker.RLock()
	replicator := x.replicator
	x.locker.RUnlock()
	return replicator
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
func (x *actorSystem) handleRemoteAsk(ctx context.Context, to *PID, message any, timeout time.Duration) (response any, err error) {
	noSender := x.NoSender()
	decoded, from, err := resolveDispatch(to, noSender, message)
	if err != nil {
		return nil, err
	}
	message = decoded

	receiveContext := toReceiveContext(ctx, from, to, message, false)

	responseCh := receiveContext.response
	to.doReceive(receiveContext)
	timer := timers.Get(timeout)

	// await patiently to receive the response from the actor
	// or wait for the context to be done
	select {
	case response = <-responseCh:
		timers.Put(timer)
		receiveContext.responseClosed.Store(true)
		putResponseChannel(responseCh)
		return
	case <-ctx.Done():
		err = errors.Join(ctx.Err(), gerrors.ErrRequestTimeout)
		to.handleReceivedErrorWithMessage(noSender, message, err)
		timers.Put(timer)
		receiveContext.responseClosed.Store(true)
		putResponseChannel(responseCh)
		return nil, err
	case <-timer.C:
		err = gerrors.ErrRequestTimeout
		to.handleReceivedErrorWithMessage(noSender, message, err)
		timers.Put(timer)
		receiveContext.responseClosed.Store(true)
		putResponseChannel(responseCh)
		return
	}
}

// handleRemoteTell handles an asynchronous message to an actor.
//
// When the caller has already resolved the sender (the remote tell handler
// does this so the dispatch and dead-letter flows share a single parse),
// it passes a non-nil from and resolveDispatch respects it verbatim.
// Otherwise the sender carried on the wire RemoteMessage is materialized.
func (x *actorSystem) handleRemoteTell(ctx context.Context, from *PID, to *PID, message any) error {
	if from == nil {
		from = x.NoSender()
	}
	decoded, resolvedFrom, err := resolveDispatch(to, from, message)
	if err != nil {
		return err
	}
	to.doReceive(toReceiveContext(ctx, resolvedFrom, to, decoded, true))
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
func (x *actorSystem) getRemoting() remoteclient.Client {
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

// getDataCenterConfig returns the data center configuration.
func (x *actorSystem) getDataCenterConfig() *datacenter.Config {
	x.locker.RLock()
	defer x.locker.RUnlock()
	if x.clusterConfig == nil {
		return nil
	}
	return x.clusterConfig.dataCenterConfig
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

// beginRelocation registers an in-flight relocation for the departed peer
// address, holding its peer state snapshot. It returns false when a relocation
// for that address is already in flight, so duplicate NodeLeft events do not
// dispatch a second rebalance.
func (x *actorSystem) beginRelocation(peerAddress string, peerState *internalpb.PeerState) bool {
	x.relocationJobsLocker.Lock()
	defer x.relocationJobsLocker.Unlock()

	if _, exists := x.relocationJobs[peerAddress]; exists {
		return false
	}

	x.relocationJobs[peerAddress] = peerState
	return true
}

// relocationJob returns the peer state snapshot of the in-flight relocation
// registered for the departed peer address, if any.
func (x *actorSystem) relocationJob(peerAddress string) (*internalpb.PeerState, bool) {
	x.relocationJobsLocker.Lock()
	defer x.relocationJobsLocker.Unlock()

	peerState, ok := x.relocationJobs[peerAddress]
	return peerState, ok
}

// endRelocation releases the in-flight relocation registered for the departed
// peer address so a future departure of the same address can rebalance again.
func (x *actorSystem) endRelocation(peerAddress string) {
	x.relocationJobsLocker.Lock()
	delete(x.relocationJobs, peerAddress)
	x.relocationJobsLocker.Unlock()
}

// cachePeerRemotingPorts refreshes the peers-address -> remoting-port cache from
// current live membership (peers plus self). It merges rather than replaces so a
// node that has just departed remains resolvable until its NodeLeft is handled;
// forgetPeerRemotingPort prunes the entry afterwards to keep the map bounded.
func (x *actorSystem) cachePeerRemotingPorts(ctx context.Context) {
	if !x.clusterEnabled.Load() || x.cluster == nil {
		return
	}

	peers, err := x.cluster.Peers(ctx)
	if err != nil {
		x.logger.Warnf("node=%s failed to refresh peer remoting ports: %v (hint: check cluster connectivity)", x.String(), err)
		return
	}

	// include self so a same-host layout (multiple nodes on one host) never
	// misattributes the local node's remoting port to a departed peer
	x.peerRemotingPorts.Set(x.PeersAddress(), x.Port())

	for _, peer := range peers {
		x.peerRemotingPorts.Set(peer.PeerAddress(), peer.RemotingPort)
	}
}

// peerRemotingPort returns the cached remoting port for a peers address.
func (x *actorSystem) peerRemotingPort(peerAddress string) (int, bool) {
	return x.peerRemotingPorts.Get(peerAddress)
}

// forgetPeerRemotingPort drops a peers address from the cache once its departure
// has been handled, bounding the map under churn (a stable address that leaves
// and rejoins is re-added on the next membership refresh).
func (x *actorSystem) forgetPeerRemotingPort(peerAddress string) {
	x.peerRemotingPorts.Delete(peerAddress)
}

// putActorOnCluster broadcasts the newly (re)spawned actor into the
// cluster. The select guards against the shutdown race: if shutdownSignal
// closes first (shutdown has started), the send is skipped instead of
// racing shutdownCluster on the channel.
func (x *actorSystem) putActorOnCluster(pid *PID) error {
	if !x.clusterEnabled.Load() {
		return nil
	}
	actor, err := pid.toSerialize()
	if err != nil {
		return err
	}
	select {
	case x.actorsQueue <- actor:
	case <-x.shutdownSignal:
	}
	return nil
}

// putGrainOnCluster broadcasts the newly (re)activated grain into the
// cluster. See putActorOnCluster for the shutdown-race reasoning.
func (x *actorSystem) putGrainOnCluster(pid *grainPID) error {
	if !x.clusterEnabled.Load() {
		return nil
	}
	grain, err := pid.toWireGrain()
	if err != nil {
		return err
	}
	select {
	case x.grainsQueue <- grain:
	case <-x.shutdownSignal:
	}
	return nil
}

// setupCluster prepares the cluster engine when clustering is enabled
func (x *actorSystem) setupCluster() error {
	if !x.clusterEnabled.Load() {
		return nil
	}

	x.logger.Info("enabling clustering...")

	if !x.remotingEnabled.Load() {
		x.logger.Error("remoting must be enabled to use clustering (hint: call WithRemoting() before WithCluster())")
		return errors.New("clustering needs remoting to be enabled")
	}

	if x.clusterConfig.replicaCount == 1 {
		x.logger.Warn("cluster replica count is 1: registry partitions owned by a crashed node are lost with it, so registry-derived crash recovery will be partial (hint: use WithReplicaCount(2) or higher to keep a backup copy of every partition)")
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
		x.logger.Errorf("failed to initialize cluster store: %v (hint: check disk space, BoltDB path permissions)", err)
		return err
	}

	for _, kind := range x.clusterConfig.kinds.Values() {
		x.registry.Register(kind)
		x.logger.Debugf("kind=%s registered", types.Name(kind))
	}

	grains := x.clusterConfig.grains.Values()
	if len(grains) > 0 {
		for _, grain := range grains {
			x.registry.Register(grain)
			x.logger.Debugf("grain=%s registered", types.Name(grain))
		}
	}

	x.logger.Info("clustering is enabled")
	return nil
}

// startCluster enables clustering. When clustering is enabled remoting is also enabled to facilitate remote
// communication
func (x *actorSystem) startCluster(ctx context.Context) error {
	if !x.clusterEnabled.Load() {
		return nil
	}

	x.logger.Info("starting cluster engine...")
	if err := x.cluster.Start(ctx); err != nil {
		x.logger.Errorf("failed to start cluster engine: %v (hint: check discovery peers, network, firewall)", err)
		return err
	}

	x.logger.Info("cluster engine successfully started")

	x.setupGrainActivationBarrier(ctx)

	// Seed the peers-address -> remoting-port cache from the current membership
	// before the events loop starts, so a node that was already a member at
	// startup can still be resolved by handleNodeLeftEvent when it later crashes
	// without a graceful-shutdown snapshot.
	x.cachePeerRemotingPorts(ctx)

	x.eventsQueue = x.cluster.Events()
	go x.clusterEventsLoop()
	// Track the replicate drainers so shutdown can wait for them to drain
	// the cluster queues before reset() clears the rest of the state.
	x.drainers.Add(2)
	go x.replicateActors()
	go x.replicateGrains()

	if err := x.cleanupStaleLocalActors(ctx); err != nil {
		x.logger.Warnf("failed to cleanup stale cluster actors: %v", err)
	}

	x.logger.Info("clustering started")
	return nil
}

// cleanupStaleLocalActors reconciles cluster actor records that belong to this
// node but have no corresponding local PID, which happens when a previous
// incarnation of this node crashed and restarted at the same address (a
// container restart in place) without the cluster ever observing a NodeLeft.
// Relocatable actors are respawned locally from their registry record, exactly
// as crash relocation would have recreated them on a survivor; without this
// they would be erased with no relocation, no event and no trace. Singleton
// and non-relocatable records are removed: singletons are re-arbitrated by the
// leader on demand and non-relocatable actors are lost with their incarnation
// by design. This is best-effort for unclean restarts and does not fail
// startup on errors.
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
	selfAddress := net.JoinHostPort(host, strconv.Itoa(port))
	recovered := 0

	for _, actor := range actors {
		addr, err := address.Parse(actor.GetAddress())
		if err != nil {
			x.logger.Warnf("failed to parse cluster actor address %q: %v", actor.GetAddress(), err)
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

		if actor.GetSingleton() == nil && actor.GetRelocatable() {
			if err := x.recreateActorFromWire(ctx, actor, selfAddress); err != nil {
				x.logger.Warnf("failed to recover actor %s from a previous incarnation of this node: %v", addr.String(), err)
				continue
			}

			recovered++
			continue
		}

		if err := x.cluster.RemoveActor(ctx, addr.Name()); err != nil {
			x.logger.Warnf("failed to remove stale cluster actor %s: %v", addr.String(), err)
			continue
		}

		x.logger.Debugf("removed stale cluster actor %s", addr.String())
	}

	if recovered > 0 {
		x.logger.Infof("recovered %d relocatable actor(s) from a previous incarnation of node=%s", recovered, selfAddress)
	}

	return nil
}

// setupRemoting sets the remoting service
func (x *actorSystem) setupRemoting() error {
	opts := []remoteclient.ClientOption{
		// set the compression algorithm to use by the remoting client
		remoteclient.WithClientCompression(x.remoteConfig.Compression()),
		// set the idle timeout for the remoting client
		remoteclient.WithClientIdleTimeout(x.remoteConfig.IdleTimeout()),
		// set the max idle connections for the remoting client
		remoteclient.WithClientMaxIdleConns(x.remoteConfig.MaxIdleConns()),
		// set the dial timeout for the remoting client
		remoteclient.WithClientDialTimeout(x.remoteConfig.DialTimeout()),
		// set the keep alive interval for the remoting client
		remoteclient.WithClientKeepAlive(x.remoteConfig.KeepAlive()),
		// Register built-in serializers for native actor message types.
		// These are internal and not visible to application code.
		remoteclient.WithClientSerializers(new(PoisonPill), &poisonPillSerializer{}),
		remoteclient.WithClientSerializers(new(Terminated), &terminatedSerializer{}),
		// set the dependency registry for the remoting client
		remoteclient.WithDependencyRegistry(x.registry),
	}

	// Forward user-defined serializers from the remote config so that both the
	// server (inbound deserialization) and the client (outbound serialization)
	// use the same set of serializers.
	opts = append(opts, remoteclient.ClientSerializerOptions(x.remoteConfig)...)

	if propagator := x.remoteConfig.ContextPropagator(); propagator != nil {
		opts = append(opts, remoteclient.WithClientContextPropagator(propagator))
	}

	if x.tlsInfo != nil {
		opts = append(opts, remoteclient.WithClientTLS(x.tlsInfo.ClientConfig))
	}

	// Outbound RemoteTell coalescer. Internal optimization — amortizes the
	// per-RPC cost across messages that pile up while a previous send is in
	// flight (Nagle-style). On an idle destination, the first message is
	// flushed immediately; batching only kicks in naturally when load exceeds
	// the RPC completion rate. Whole-batch failures (transport or protocol)
	// are fanned out to the local dead-letter actor so operators and
	// subscribers of the dead-letter event stream observe them the same way
	// they observe local-delivery failures.
	x.startCoalescedFailureDrain()
	opts = append(opts,
		remoteclient.WithSendCoalescing(remoteSendCoalescingMaxBatch),
		remoteclient.WithCoalescingErrorHandler(x.enqueueCoalescedFailure),
	)

	x.remoting = remoteclient.NewClient(opts...)
	return nil
}

// startMessagesScheduler starts the messages scheduler
func (x *actorSystem) startMessagesScheduler(ctx context.Context) {
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

// startupCleanup tears down partially-started components on startup failure.
// Unlike shutdown(), this can be called while starting=true and started=false.
func (x *actorSystem) startupCleanup(ctx context.Context) {
	// Close shutdownSignal so any producer that already began publishing
	// stops before we tear down cluster queues. Guarded by the CAS so
	// close runs exactly once across startupCleanup / shutdown.
	if x.shuttingDown.CompareAndSwap(false, true) {
		close(x.shutdownSignal)
	}
	x.stopDataCenterLeaderWatch()
	_ = x.stopDataCenterController(ctx)

	actors := x.localActors()
	_ = x.shutdownCluster(ctx, actors, nil)
	_ = x.shutdownRemoting(ctx)

	if x.eventsStream != nil {
		x.eventsStream.Close()
	}

	x.dispatcher.signalStop()
	// Wait for the replicate drainers to drain the cluster queues before
	// reset() clears state. If cluster setup never got as far as spawning
	// them, drainers' count is zero and Wait returns immediately.
	x.drainers.Wait()
	x.reset()
}

// reset the actor system
func (x *actorSystem) reset() {
	x.started.Store(false)
	x.starting.Store(false)
	x.extensions.Reset()
	x.actors.reset()
	x.grains.Reset()
	x.remoteSenderAddresses.Reset()
	x.shuttingDown.Store(false)
	// shutdownSignal is intentionally left closed here. It is recreated in
	// Start(), the only point where no producer/drainer can race the write.
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

	x.logger.Info("shutdown process begins")
	// First CAS wins and closes shutdownSignal so producers stop sending
	// on cluster queues. Subsequent calls (e.g. racing shutdown requests)
	// observe shuttingDown=true and skip the close.
	if x.shuttingDown.CompareAndSwap(false, true) {
		close(x.shutdownSignal)
	}

	defer func() {
		// Wait for the replicate drainers to exit before reset() clears
		// state. Drainers already observed the close above and will return
		// shortly after draining any buffered items, so this does not add
		// meaningful latency.
		x.drainers.Wait()
		x.reset()
		// Signal dispatcher shutdown after all actors are torn down and
		// state is reset. We do not wait for workers to exit because this
		// shutdown path may be running on one of the worker goroutines
		// (e.g. triggered from an actor receive handler). Workers exit
		// autonomously once their current turn completes.
		x.dispatcher.signalStop()
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

	actorRefs := x.localActors()

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
		x.logger.Errorf("failed to build peer state snapshot: %v (hint: check cluster connectivity)", preShutdownErr)
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
		x.logger.Errorf("failed to shutdown cleanly: %v (hint: check actor PostStop, supervision)", err)
		clusterErr := shutdownClusterAndRemoting()
		// Combine all errors if present
		return multierr.Combine(hooksErr, err, clusterErr)
	}

	// Route grain deactivation through the mailbox. Sending a PoisonPill
	// instead of calling deactivate directly means OnDeactivate runs
	// inside the grain's dispatcher turn, under schedState.Processing —
	// the same lock that serialises OnReceive. That removes the need
	// for a dispatcher-wide drain and eliminates the OnReceive /
	// OnDeactivate race at its source. ctx bounds how long we wait for
	// all grains to finish; any grain that doesn't drain in time is
	// abandoned (the pill sits in its mailbox and is lost when the
	// dispatcher is signalled to stop later in the defer).
	poisonErr := x.poisonAllGrains(ctx)

	// shutdown remaining system actors
	if err := chain.
		New(chain.WithFailFast(), chain.WithContext(ctx)).
		AddContextRunnerIf(x.TopicActor() != nil, x.TopicActor().Shutdown).
		AddContextRunnerIf(x.NoSender() != nil, x.NoSender().Shutdown).
		AddContextRunnerIf(x.getSystemGuardian() != nil, x.getSystemGuardian().Shutdown).
		AddContextRunnerIf(x.getRootGuardian() != nil, x.getRootGuardian().Shutdown).
		Run(); err != nil {
		x.logger.Errorf("failed to shutdown cleanly: %v (hint: check system actor PostStop)", err)
		clusterErr := shutdownClusterAndRemoting()
		// Combine all errors if present
		return multierr.Combine(hooksErr, poisonErr, err, clusterErr)
	}

	x.actors.deleteNode(x.getRootGuardian())

	if x.eventsStream != nil {
		x.eventsStream.Close()
	}

	// Always attempt to shutdown cluster and remoting
	// clusterErr includes preShutdownErr if it exists (via chain)
	clusterErr := shutdownClusterAndRemoting()
	if clusterErr != nil {
		x.logger.Errorf("failed shutdown: %v (hint: check cluster/remoting shutdown)", clusterErr)
		return multierr.Combine(hooksErr, poisonErr, clusterErr)
	}

	if hooksErr != nil || poisonErr != nil {
		if hooksErr != nil {
			x.logger.Errorf("failed shutdown cleanly: %v (hint: check PreStop/PostStop hooks)", hooksErr)
		}
		return multierr.Combine(hooksErr, poisonErr)
	}

	x.logger.Info("shutdown successfully")
	return nil
}

// poisonAllGrains sends a PoisonPill to every active grain and waits
// for each to signal deactivation, bounded by ctx. Because
// handlePoisonPill runs inside the grain's dispatcher turn (under
// schedState.Processing), OnDeactivate is serialised with OnReceive —
// no cross-goroutine access on user grain state.
//
// Returns ctx.Err() if the deadline expires before every grain finishes.
// Grains that did not drain in time are left in the registry; their
// pending pill sits in the mailbox and is lost when the dispatcher is
// signalled to stop later in shutdown's defer. Callers treat this as a
// best-effort outcome — some OnDeactivate hooks may not run under a
// tight shutdown deadline.
func (x *actorSystem) poisonAllGrains(ctx context.Context) error {
	if x.grains.Len() == 0 {
		return nil
	}

	grains := x.grains.Values()
	x.logger.Debugf("deactivating %d grains via PoisonPill...", len(grains))

	pending := make([]*grainPID, 0, len(grains))
	for _, grain := range grains {
		if !grain.isActive() {
			x.grains.Delete(grain.getIdentity().String())
			continue
		}

		gctx := getGrainContext()
		gctx.build(ctx, grain, x, grain.getIdentity(), new(PoisonPill), false)
		grain.receive(gctx)
		pending = append(pending, grain)
	}

	for _, grain := range pending {
		select {
		case <-grain.deactivated:
			x.grains.Delete(grain.getIdentity().String())
			x.logger.Debugf("grain=%s deactivated", grain.getIdentity().String())
		case <-ctx.Done():
			x.logger.Errorf(
				"shutdown context expired before grain=%s finished deactivating (hint: OnDeactivate will not run for remaining grains)",
				grain.getIdentity().String(),
			)
			return ctx.Err()
		}
	}
	return nil
}

// replicateActors publishes newly created actors into the cluster when
// cluster is enabled. Exits on shutdownSignal after draining any items
// still buffered on actorsQueue. Also exits if actorsQueue is closed.
//
// The shutdown signal and queue are captured into locals once on
// goroutine entry. shutdown() waits on x.drainers before calling
// reset(), so the initial field reads here happen-before any later
// reassignment — no race even across restart cycles.
func (x *actorSystem) replicateActors() {
	defer x.drainers.Done()
	signal := x.shutdownSignal
	queue := x.actorsQueue
	for {
		select {
		case actor, ok := <-queue:
			if !ok {
				return
			}
			x.replicateOneActor(actor)
		case <-signal:
			for {
				select {
				case actor, ok := <-queue:
					if !ok {
						return
					}
					x.replicateOneActor(actor)
				default:
					return
				}
			}
		}
	}
}

// replicateOneActor performs the per-message replication body. Factored
// out so both the live-processing and post-signal drain branches of
// replicateActors share the same logic.
func (x *actorSystem) replicateOneActor(actor *internalpb.Actor) {
	addr, _ := address.Parse(actor.GetAddress())
	if isSystemName(addr.Name()) {
		return
	}
	if x.isStopping() || !x.InCluster() {
		return
	}

	// Skip replication if the actor was already removed locally. Replication is
	// asynchronous (the actor is queued on spawn and published here later), while
	// removal on kill is synchronous via the death watch (deleteNode followed by
	// cluster.RemoveActor). When an actor is killed right after being spawned, the
	// queued PutActor can run after RemoveActor and re-register a dead actor in the
	// cluster registry, where it would never be cleaned up. Mirrors the stale-actor
	// check in removeStaleClusterActors.
	if _, ok := x.actors.node(addr.String()); !ok {
		return
	}

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
			return
		}
	}
	if err := cluster.PutActor(ctx, actor); err != nil {
		x.logger.Warn(err.Error())
	}
}

// replicateGrains publishes newly created grains into the cluster when
// cluster is enabled. Exits on shutdownSignal after draining any items
// still buffered on grainsQueue. Also exits if grainsQueue is closed.
func (x *actorSystem) replicateGrains() {
	defer x.drainers.Done()
	// See replicateActors for why signal / queue are captured once here
	// rather than read from the struct on every iteration.
	signal := x.shutdownSignal
	queue := x.grainsQueue
	for {
		select {
		case grain, ok := <-queue:
			if !ok {
				return
			}
			x.replicateOneGrain(grain)
		case <-signal:
			for {
				select {
				case grain, ok := <-queue:
					if !ok {
						return
					}
					x.replicateOneGrain(grain)
				default:
					return
				}
			}
		}
	}
}

// replicateOneGrain performs the per-message replication body for grains.
func (x *actorSystem) replicateOneGrain(grain *internalpb.Grain) {
	if isSystemName(grain.GetGrainId().GetName()) {
		return
	}
	if x.isStopping() || !x.InCluster() {
		return
	}

	ctx := context.Background()
	if err := x.cluster.PutGrain(ctx, grain); err != nil {
		x.logger.Warn(err.Error())
	}
	if err := x.cluster.PutKind(ctx, grain.GetGrainId().GetKind()); err != nil {
		x.logger.Warn(err.Error())
	}
}

// resyncActors resyncs all actors in the actor system.
// This is only called on a NodeLeft event to repair registry entries whose
// owning partition was lost with the departed node (see handleNodeLeftEvent).
func (x *actorSystem) resyncActors() error {
	actors := x.localActors()
	for _, actor := range actors {
		if err := x.putActorOnCluster(actor); err != nil {
			x.logger.Errorf("failed to resync actor=%s: %v (hint: check cluster connectivity)", pathString(actor.Path()), err)
			return fmt.Errorf("failed to resync Actor (%s): %w", pathString(actor.Path()), err)
		}
	}
	return nil
}

// resyncGrains resyncs all grains in the actor system.
// This is only called on a NodeLeft event to repair registry entries whose
// owning partition was lost with the departed node (see handleNodeLeftEvent).
func (x *actorSystem) resyncGrains() error {
	grains := x.grains.Values()
	for _, grain := range grains {
		if err := x.putGrainOnCluster(grain); err != nil {
			x.logger.Errorf("failed to resync grain=%s: %v (hint: check cluster connectivity)", grain.getIdentity().String(), err)
			return fmt.Errorf("failed to resync Grain (%s): %w", grain.getIdentity().String(), err)
		}
	}
	return nil
}

// clusterEventsLoop consumes cluster topology events, publishes them on the
// event stream and reconciles cluster leadership. It runs in a single goroutine,
// so the leadership state stays free of data races without locking.
func (x *actorSystem) clusterEventsLoop() {
	for event := range x.eventsQueue {
		x.handleClusterEvent(event)
	}
}

// handleClusterEvent forwards a single cluster event to the event stream and
// applies any side effects. Leadership changes are detected by the cluster
// engine and arrive here as LeaderChangedEvent, so this only forwards them.
func (x *actorSystem) handleClusterEvent(event *cluster.Event) {
	if x.isStopping() || !x.InCluster() || event == nil || event.Payload == nil {
		return
	}

	var message any
	switch evt := event.Payload.(type) {
	case *cluster.NodeJoinedEvent:
		message = NewNodeJoined(evt.Address, evt.Timestamp)
	case *cluster.NodeLeftEvent:
		message = NewNodeLeft(evt.Address, evt.Timestamp)
	case *cluster.LeaderChangedEvent:
		message = NewLeaderChanged(evt.Address, evt.Timestamp)
	default:
		x.logger.Warnf("node=%s received unknown cluster event type=%T", x.String(), evt)
		return
	}

	if x.eventsStream != nil {
		x.logger.Debugf("node=%s publishing cluster event=%s", x.String(), event.Type)
		x.eventsStream.Publish(eventsTopic, message)
		x.logger.Debugf("node=%s published cluster event=%s successfully", x.String(), event.Type)
	}

	switch event.Type {
	case cluster.NodeLeft:
		x.handleNodeLeftEvent(event)
	case cluster.NodeJoined:
		x.handleNodeJoinedEvent(event)
	}
}

// handleNodeJoinedEvent processes a NodeJoined cluster event.
func (x *actorSystem) handleNodeJoinedEvent(event *cluster.Event) {
	nodeJoined := event.Payload.(*cluster.NodeJoinedEvent)
	x.logger.Infof("node=%s detected node joined event: node=%s", x.String(), nodeJoined.Address)

	// Refresh the peers-address -> remoting-port cache so the joining node (and
	// any membership churn since the last event) is resolvable when it later
	// departs without a graceful-shutdown snapshot (see handleNodeLeftEvent).
	x.cachePeerRemotingPorts(context.Background())

	// A member that restarts at the same host:port within its own handoff
	// window is reachable again: close the window so name-based sends resolving
	// to it are delivered instead of being stalled or refused as mid-relocation
	// for the remainder of the window.
	x.markEndpointRecovered(nodeJoined.Address)

	x.tryOpenGrainActivationBarrier(context.Background())
	// A joining node does not invalidate any existing registry entry: the
	// cluster store (Olric DMap) migrates partition data to the new owner as
	// part of rebalancing, so re-putting every local actor and grain here would
	// be O(total cluster actors) redundant writes. Repair on departure only
	// (handleNodeLeftEvent), where partitions can actually be dropped.
	//
	// Tradeoff (deliberate): with replicaCount=1 (the default) a partition has
	// no backup, so if a migration to the joining node is disrupted mid-flight
	// its entries can be lost, and that loss is not repaired until the next
	// departure-triggered resync. Repairing here on every join would trade a
	// rare, self-correcting gap for a guaranteed O(cluster actors) write storm on
	// every membership addition. Running with replicaCount>1 removes the gap
	// (olric keeps a backup of each partition during migration); single-replica
	// clusters accept it as the cost of the default.
	x.triggerDataCentersReconciliation()
}

// handleNodeLeftEvent processes a NodeLeft cluster event.
func (x *actorSystem) handleNodeLeftEvent(event *cluster.Event) {
	nodeLeft := event.Payload.(*cluster.NodeLeftEvent)
	x.logger.Infof("node=%s detected node left event: node=%s", x.String(), nodeLeft.Address)

	// The departed node is already gone from membership, so prune its cached
	// remoting port once its departure has been fully handled. Doing so keeps
	// the cache bounded under churn; a stable address that leaves and rejoins
	// is re-added on the next membership refresh. When crash recovery runs
	// asynchronously (gateCrashRecovery), the goroutine takes ownership of the
	// pruning: a deferred prune here would race the recovery's registry
	// derivation, which resolves the remoting port from this cache seconds
	// later, and losing that race silently skips the whole rebalance.
	pruneCachedPort := true

	defer func() {
		if pruneCachedPort {
			x.forgetPeerRemotingPort(nodeLeft.Address)
		}
	}()

	x.pruneRemoteWatchesForHost(context.Background(), nodeLeft.Address, nodeLeft.Timestamp)
	// Repair the cluster store after a departure. With a replica count of 1 the
	// partitions owned by the departed node are lost, so surviving nodes re-put
	// their own live actors and grains to restore any registry entries that were
	// hosted on those partitions. When the cluster keeps replicas this repair is
	// skipped because olric promotes the backups itself (see resyncAfterClusterEvent).
	// Relocation (below) handles the departed node's own actors and grains separately.
	x.resyncAfterClusterEvent(event.Type, nodeLeft.Address)
	x.triggerDataCentersReconciliation()

	if !x.relocationEnabled.Load() {
		return
	}

	// Open a bounded handoff window for the departed node's remoting endpoint so
	// senders on this node briefly retry (rather than fail) while its actors are
	// recreated on a survivor. This runs on every node, not just the leader,
	// because any node may route a message to the departed node's actors.
	x.markEndpointRelocating(nodeLeft.Address)

	ctx := context.Background()

	if x.cluster.IsLeader(ctx) {
		x.logger.Infof("leader=%s initiating rebalancing for node=%s", x.String(), nodeLeft.Address)

		// fetch the peer state of the node that left from the cluster store
		peerState, ok := x.clusterStore.GetPeerState(ctx, nodeLeft.Address)
		if !ok {
			// No graceful-shutdown snapshot: the node most likely crashed.
			// Reconstruct the relocation set from the replicated registry so its
			// actors and grains are still recovered onto surviving nodes. The
			// derivation is gated on olric's partition repair going quiet and
			// therefore runs off the events loop (see gateCrashRecovery), which
			// consults the remoting-port cache once the gate opens: the cache
			// entry must outlive this handler, so the recovery goroutine owns
			// its pruning.
			x.logger.Warnf("leader=%s found no snapshot for node=%s; deriving relocation set from the cluster registry", x.String(), nodeLeft.Address)
			pruneCachedPort = false
			go x.gateCrashRecovery(nodeLeft.Address)
			return
		}

		if !x.shouldRebalance(peerState) {
			x.logger.Debugf("leader=%s found no node=%s state to rebalance", x.String(), nodeLeft.Address)
			// Drop any stored snapshot for the departed address. A graceful
			// shutdown persists a snapshot even when it carries no relocatable
			// actors or grains; left in place, that stale empty snapshot would
			// be returned by GetPeerState if the same address later rejoins,
			// accumulates actors and then crashes, suppressing the
			// deriveRelocationSetFromRegistry crash-recovery path and losing the
			// rejoined node's actors silently. Deleting here keeps the skip path
			// self-cleaning; DeletePeerState is a no-op when nothing is stored.
			if err := x.clusterStore.DeletePeerState(ctx, nodeLeft.Address); err != nil {
				x.logger.Errorf("leader=%s failed to remove stale peer=%s state on skipped rebalance: %v (hint: check cluster store)", x.String(), nodeLeft.Address, err)
			}
			return
		}

		// register the relocation job; a duplicate NodeLeft for an in-flight
		// relocation is ignored, while a node address that departs again after
		// a completed relocation is rebalanced again
		if !x.beginRelocation(nodeLeft.Address, peerState) {
			x.logger.Debugf("leader=%s found relocation already in flight for node=%s", x.String(), nodeLeft.Address)
			return
		}

		// announce the relocation on the events stream; a graceful-shutdown
		// snapshot is complete, so this is not best-effort. Published after
		// beginRelocation so a duplicate NodeLeft never emits a second event
		// for an in-flight relocation.
		x.publishRelocationStarted(nodeLeft.Address, peerState, false)

		// dispatch to the relocator; its mailbox is the queue, so a burst of
		// node departures never blocks the cluster events loop
		if err := x.systemGuardian.Tell(ctx, x.relocator, &internalpb.Rebalance{PeerState: peerState}); err != nil {
			x.logger.Errorf("failed to send rebalance to relocator: %v (hint: check relocator state)", err)
			x.endRelocation(nodeLeft.Address)
		}
		return
	}

	// clean up the peer state of the node that left from the cluster store
	x.logger.Debugf("node=%s not leader; cleaning up node=%s left from state cache", x.String(), nodeLeft.Address)

	if err := x.clusterStore.DeletePeerState(ctx, nodeLeft.Address); err != nil {
		x.logger.Errorf("node=%s failed to remove left node=%s from cluster store: %v (hint: check cluster store)", x.String(), nodeLeft.Address, err)
	}

	x.logger.Debugf("node=%s cleaned up node=%s left from state cache", x.String(), nodeLeft.Address)
}

// gateCrashRecovery derives a crashed node's relocation set and dispatches its
// rebalance once olric's post-departure partition repair has gone quiet. A
// SIGKILL leaves olric promoting backups and moving fragments for a while
// after the NodeLeft epoch completes; scanning the registry or respawning
// during that window reads inconsistent replicas (the scan misses records and
// the respawn's duplicate check sees stale ones), turning recoverable actors
// into failures. It runs on its own goroutine so the wait never blocks the
// cluster events loop; the wait is bounded so recovery still proceeds when the
// repair signal never settles.
//
// The whole quiesce-then-derive cycle is retried a bounded number of times:
// recovery runs precisely while the cluster is churning (a replacement node may
// be joining, crash-looping, or flapping), so the derivation's registry scan
// can transiently fail on an unreachable member. Giving up on the first error
// would permanently skip the rebalance and silently lose every actor of the
// crashed node.
func (x *actorSystem) gateCrashRecovery(peerAddress string) {
	// this goroutine owns the departed node's remoting-port cache entry (see
	// handleNodeLeftEvent): prune it only once recovery no longer needs it
	defer x.forgetPeerRemotingPort(peerAddress)

	ctx := context.Background()

	var peerState *internalpb.PeerState

	for attempt := 1; ; attempt++ {
		if !x.awaitRelocationQuiescence(peerAddress) {
			return
		}

		var ok bool
		if peerState, ok = x.deriveRelocationSetFromRegistry(ctx, peerAddress); ok {
			break
		}

		if attempt >= relocationDeriveMaxAttempts {
			x.logger.Errorf("leader=%s could not derive relocation set for node=%s after %d attempts; skipping rebalance (hint: its actors are not recovered; check cluster health)", x.String(), peerAddress, attempt)
			return
		}

		x.logger.Warnf("leader=%s could not derive relocation set for node=%s (attempt %d/%d); retrying in %s", x.String(), peerAddress, attempt, relocationDeriveMaxAttempts, relocationDeriveRetryBackoff)
		pause.For(relocationDeriveRetryBackoff)

		if x.isStopping() {
			return
		}
	}

	// the retries above can keep this goroutine alive across a shutdown: never
	// publish or dispatch on a system that is stopping
	if x.isStopping() {
		return
	}

	// surface the best-effort nature of registry-derived recovery on the events
	// stream: records lost with the crashed node's partitions cannot be listed,
	// so subscribers holding an external record of placements can diff against
	// the derived set to detect silent losses. Published even when the set is
	// empty, which on a crash is suspicious rather than benign.
	x.publishRelocationStarted(peerAddress, peerState, true)

	x.dispatchDerivedRebalance(ctx, peerAddress, peerState)
}

// awaitRelocationQuiescence blocks until no olric rebalance event has been
// observed for relocationQuiescenceWindow, polling at
// relocationQuiescencePoll and never waiting longer than
// relocationQuiescenceMaxWait before proceeding anyway. It reports false when
// the system started stopping, in which case recovery must be abandoned.
func (x *actorSystem) awaitRelocationQuiescence(peerAddress string) bool {
	deadline := time.Now().Add(relocationQuiescenceMaxWait)

	for time.Since(x.cluster.LastRebalanceEvent()) < relocationQuiescenceWindow {
		if x.isStopping() {
			return false
		}

		if time.Now().After(deadline) {
			x.logger.Warnf("leader=%s proceeding with crash recovery for node=%s before partition repair went quiet (hint: recovery may be partial; affected items appear in RelocationFailed)", x.String(), peerAddress)
			break
		}

		pause.For(relocationQuiescencePoll)
	}

	return true
}

// dispatchDerivedRebalance hands a derived relocation set to the relocator,
// applying the same gating as the snapshot path.
func (x *actorSystem) dispatchDerivedRebalance(ctx context.Context, peerAddress string, peerState *internalpb.PeerState) {
	if !x.shouldRebalance(peerState) {
		x.logger.Debugf("leader=%s found no node=%s state to rebalance", x.String(), peerAddress)
		return
	}

	if !x.beginRelocation(peerAddress, peerState) {
		x.logger.Debugf("leader=%s found relocation already in flight for node=%s", x.String(), peerAddress)
		return
	}

	if err := x.systemGuardian.Tell(ctx, x.relocator, &internalpb.Rebalance{PeerState: peerState}); err != nil {
		x.logger.Errorf("failed to send rebalance to relocator: %v (hint: check relocator state)", err)
		x.endRelocation(peerAddress)
	}
}

// deriveRelocationSetFromRegistry reconstructs a departed node's relocation set
// from the replicated cluster registry when no graceful-shutdown snapshot is
// available (the node crashed). It resolves the departed node's remoting address
// from the peers-address -> remoting-port cache, then scans the registry for
// every actor and grain still hosted on that address.
//
// The returned PeerState mirrors the graceful-shutdown snapshot: Host/PeersPort
// match the NodeLeft event's peers address so relocation bookkeeping stays keyed
// on it, RemotingPort carries the resolved remoting port so the recreate gating
// matches stale registry entries, and only relocatable actors are included
// (non-relocatable actors are lost with the node by design). All matching grains
// are included; the relocation worker filters and splits them by their own
// relocation flags.
//
// It returns ok=false when the remoting port cannot be resolved (the node was
// never observed alive by this leader) or the registry scan fails, so the caller
// skips the rebalance rather than acting on an incomplete set. Registry lookups
// are a full scan here; a per-host index is a later optimization.
func (x *actorSystem) deriveRelocationSetFromRegistry(ctx context.Context, peerAddress string) (*internalpb.PeerState, bool) {
	host, peersPortStr, err := net.SplitHostPort(peerAddress)
	if err != nil {
		x.logger.Errorf("node=%s cannot parse departed peer address=%s: %v", x.String(), peerAddress, err)
		return nil, false
	}

	peersPort, err := strconv.Atoi(peersPortStr)
	if err != nil {
		x.logger.Errorf("node=%s cannot parse departed peer port from address=%s: %v", x.String(), peerAddress, err)
		return nil, false
	}

	remotingPort, ok := x.peerRemotingPort(peerAddress)
	if !ok {
		x.logger.Warnf("node=%s has no cached remoting port for departed node=%s (never observed alive); cannot derive its registry records", x.String(), peerAddress)
		return nil, false
	}

	// Bounds-checked conversions keep an out-of-range port from silently
	// truncating into the derived snapshot's wire record.
	peersPort32, err := strconvx.Int2Int32(peersPort)
	if err != nil {
		x.logger.Errorf("node=%s derived an invalid peers port from address=%s: %v", x.String(), peerAddress, err)
		return nil, false
	}

	remotingPort32, err := strconvx.Int2Int32(remotingPort)
	if err != nil {
		x.logger.Errorf("node=%s derived an invalid remoting port for departed node=%s: %v", x.String(), peerAddress, err)
		return nil, false
	}

	// recovery-sized budget: the user-configured read timeout is honored when
	// it exceeds the floor, but a lookup-sized timeout must never abandon the
	// crash-recovery scan (a timed-out scan skips the whole rebalance)
	timeout := max(x.clusterReadTimeout(time.Second), relocationDeriveScanTimeout)

	// Filter to the departed host during the scan so a crash recovery never
	// materializes the whole cluster registry just to keep one node's records;
	// the streaming ActorsByHost/GrainsByHost return only the matching subset.
	registryActors, err := x.cluster.ActorsByHost(ctx, host, remotingPort, timeout)
	if err != nil {
		x.logger.Errorf("node=%s failed to scan cluster actors while deriving relocation set for node=%s: %v", x.String(), peerAddress, err)
		return nil, false
	}

	registryGrains, err := x.cluster.GrainsByHost(ctx, host, remotingPort, timeout)
	if err != nil {
		x.logger.Errorf("node=%s failed to scan cluster grains while deriving relocation set for node=%s: %v", x.String(), peerAddress, err)
		return nil, false
	}

	// nil maps until the first match: most departures own a small fraction of
	// the registry, so the common case allocates nothing here.
	var wireActors map[string]*internalpb.Actor

	for _, actor := range registryActors {
		// only relocatable actors are recovered; the rest are lost with the node
		// by design
		if !actor.GetRelocatable() {
			continue
		}

		addr, perr := address.Parse(actor.GetAddress())
		if perr != nil {
			continue
		}

		if isSystemName(addr.Name()) {
			continue
		}

		if wireActors == nil {
			wireActors = make(map[string]*internalpb.Actor)
		}

		wireActors[addr.Name()] = actor
	}

	var wireGrains map[string]*internalpb.Grain
	for _, grain := range registryGrains {
		if isSystemName(grain.GetGrainId().GetName()) {
			continue
		}

		if wireGrains == nil {
			wireGrains = make(map[string]*internalpb.Grain)
		}

		wireGrains[grain.GetGrainId().GetValue()] = grain
	}

	// An empty set on a crash is suspicious rather than benign: with a cluster
	// replica count of 1 the registry partitions owned by the crashed node are
	// lost with it, so its records may be unrecoverable. Surface it loudly
	// instead of silently skipping the rebalance downstream.
	if len(wireActors) == 0 && len(wireGrains) == 0 {
		x.logger.Warnf("leader=%s derived an empty relocation set for crashed node=%s (remoting=%s:%d); its registry records may have been lost with it (raise the cluster replica count above 1 to make crash recovery reliable)",
			x.String(), peerAddress, host, remotingPort)
	} else {
		x.logger.Infof("leader=%s derived relocation set for crashed node=%s (remoting=%s:%d): actors=%d grains=%d",
			x.String(), peerAddress, host, remotingPort, len(wireActors), len(wireGrains))
	}

	return &internalpb.PeerState{
		Host:         host,
		PeersPort:    peersPort32,
		RemotingPort: remotingPort32,
		Actors:       wireActors,
		Grains:       wireGrains,
	}, true
}

// publishRelocationStarted emits a RelocationStarted event for a departed node
// whose items are about to be relocated, listing the actor names and grain IDs
// in the relocation set. bestEffort is false for a graceful-shutdown snapshot
// and true for a set reconstructed from the cluster registry after a crash;
// see RelocationStarted for the contract.
func (x *actorSystem) publishRelocationStarted(peerAddress string, peerState *internalpb.PeerState, bestEffort bool) {
	if x.eventsStream == nil {
		return
	}

	actors := make([]string, 0, len(peerState.GetActors()))

	for name := range peerState.GetActors() {
		actors = append(actors, name)
	}

	grains := make([]string, 0, len(peerState.GetGrains()))
	for id := range peerState.GetGrains() {
		grains = append(grains, id)
	}

	x.eventsStream.Publish(eventsTopic, NewRelocationStarted(peerAddress, time.Now().UTC(), actors, grains, bestEffort))
}

// pruneRemoteWatchesForHost cleans up the remote watch registry after a
// cluster node has left. Watcher-side entries (remote actors that were
// watching local pids on the departed host) are dropped silently — the
// watcher is unreachable, no further notification is possible. Watchee-side
// entries (local pids that were watching remote actors on the departed host)
// receive a synthesized Terminated message delivered to the local watcher so
// the application observes the same lifecycle event it would on a clean
// remote shutdown.
//
// The synthesized Terminated carries deathTime — typically the timestamp
// reported by the cluster membership layer when the departure was detected —
// rather than the current wall clock, so watchers using TerminatedAt for
// ordering or auditing see the accurate event time.
//
// The host is matched as the leading portion of nodeAddress up to the first
// colon. This assumes the cluster and remoting layers share the same host
// identifier (typically true since both derive it from the same listen-address
// configuration). When nodeAddress does not parse as host:port it is used
// as-is so misconfigured callers still get a registry sweep.
func (x *actorSystem) pruneRemoteWatchesForHost(ctx context.Context, nodeAddress string, deathTime time.Time) {
	host, _, err := net.SplitHostPort(nodeAddress)
	if err != nil {
		host = nodeAddress
	}

	entries := x.remoteWatches.dropHost(host)
	if len(entries.Watchees) == 0 {
		return
	}

	sender := x.deathWatch
	if sender == nil {
		return
	}

	for _, watchee := range entries.Watchees {
		node, ok := x.actors.node(watchee.LocalID)
		if !ok {
			continue
		}

		watcher := node.value()
		if watcher == nil || !watcher.IsRunning() {
			continue
		}

		terminated := &Terminated{
			actorPath:    newPath(watchee.RemoteAddress),
			terminatedAt: deathTime.UTC(),
		}

		if err := sender.Tell(ctx, watcher, terminated); err != nil {
			x.logger.Debugf("node-left: failed to deliver synthesized Terminated for %s to %s: %v",
				watchee.RemoteAddress, watcher.Name(), err)
		}
	}
}

// resyncAfterClusterEvent handles resyncing actors and grains after a cluster event.
//
// The full re-put exists to repair registry entries whose owning partition was
// lost with the departed node. With a single replica (replicaCount <= 1) every
// departure can lose entries, so the repair always runs. With a higher replica
// count olric normally holds backups for every partition and promotes them on
// the membership change, so a single departure survives and re-putting the whole
// local set would be redundant write amplification; the repair is skipped in
// that case.
//
// That skip is only safe while losses stay within olric's tolerance. Olric
// tolerates at most replicaCount-1 simultaneous failures: once that many nodes
// depart within the correlated-departure window (an AZ or rack outage, a rolling
// restart gone wrong), a partition can lose every replica, so registry entries
// for actors that are still alive on survivors would be dropped permanently. To
// stay correct under correlated failures the repair still runs once the number
// of nodes that departed together reaches replicaCount.
func (x *actorSystem) resyncAfterClusterEvent(eventType cluster.EventType, nodeAddress string) {
	replicaCount := uint32(1)
	if x.clusterConfig != nil {
		replicaCount = x.clusterConfig.replicaCount
	}

	if replicaCount > 1 {
		concurrent := x.recordDeparture(nodeAddress)
		if concurrent < int(replicaCount) {
			x.logger.Debugf("node=%s skipping registry resync after event=%s node=%s: replicaCount=%d lets olric re-replicate the departed partitions (%d concurrent departure(s))",
				x.String(), eventType, nodeAddress, replicaCount, concurrent)
			return
		}
		x.logger.Warnf("node=%s running registry resync after event=%s node=%s: %d concurrent departures reached replicaCount=%d, so olric backups may not have survived (hint: repairing registry entries for actors alive on survivors)",
			x.String(), eventType, nodeAddress, concurrent, replicaCount)
	}

	x.logger.Debugf("node=%s resyncing actors after event=%s node=%s", x.String(), eventType, nodeAddress)

	if err := x.resyncActors(); err != nil {
		x.logger.Errorf("node=%s failed to resync actors after event=%s: %v (hint: check cluster connectivity)", x.String(), eventType, err)
	}

	x.logger.Debugf("node=%s resynced actors after event=%s", x.String(), eventType)

	if x.grains.Len() > 0 {
		x.logger.Debugf("node=%s resyncing grains after event=%s node=%s", x.String(), eventType, nodeAddress)

		if err := x.resyncGrains(); err != nil {
			x.logger.Errorf("node=%s failed to resync grains after event=%s: %v (hint: check cluster connectivity)", x.String(), eventType, err)
		}

		x.logger.Debugf("node=%s resynced grains after event=%s", x.String(), eventType)
	}
}

// recordDeparture records nodeAddress as a recent departure and returns how many
// distinct nodes have departed within the correlated-departure window (including
// this one). It is the correlated-failure signal for resyncAfterClusterEvent.
// It tolerates a nil tracker (some test doubles construct the system directly)
// by treating the current departure as the only one.
func (x *actorSystem) recordDeparture(nodeAddress string) int {
	if x.recentDepartures == nil {
		return 1
	}

	x.recentDepartures.Set(nodeAddress, types.Unit{})
	return x.recentDepartures.ActiveLen()
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

// getRemoteWatches returns the remote watch registry.
func (x *actorSystem) getRemoteWatches() *remoteWatchRegistry {
	return x.remoteWatches
}

// getRemoteWatchTimeout returns the deadline applied to remote
// PID.Watch / PID.UnWatch RPCs.
func (x *actorSystem) getRemoteWatchTimeout() time.Duration {
	return x.remoteWatchTimeout
}

// getCluster returns the cluster engine
func (x *actorSystem) getCluster() cluster.Cluster {
	x.locker.RLock()
	cluster := x.cluster
	x.locker.RUnlock()
	return cluster
}

// getNodeRoles returns the roles advertised by the local node. It returns nil
// when clustering is disabled or no roles were configured, in which case the
// node is treated as eligible for any role-less relocation target.
func (x *actorSystem) getNodeRoles() []string {
	if x.clusterNode == nil {
		return nil
	}

	return x.clusterNode.Roles
}

// clusterReadTimeout returns the user-configured cluster read timeout, or
// fallback when clustering is not configured or the value is unset.
func (x *actorSystem) clusterReadTimeout(fallback time.Duration) time.Duration {
	if x.clusterConfig != nil && x.clusterConfig.readTimeout > 0 {
		return x.clusterConfig.readTimeout
	}

	return fallback
}

// actorAddress returns the actor path provided the actor name
func (x *actorSystem) actorAddress(name string) *address.Address {
	return address.New(name, x.name, x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
}

// spawnRootGuardian creates the rootGuardian guardian
func (x *actorSystem) spawnRootGuardian(ctx context.Context) error {
	actorName := reservedName(rootGuardianType)
	x.rootGuardian, _ = x.configPID(ctx, actorName, newRootGuardian(), asSystem(), WithLongLived())
	// rootGuardian is the rootGuardian node of the actors tree
	return x.actors.addRootNode(x.rootGuardian)
}

// spawnSystemGuardian creates the system guardian
func (x *actorSystem) spawnSystemGuardian(ctx context.Context) error {
	actorName := reservedName(systemGuardianType)
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
	actorName := reservedName(userGuardianType)
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
	actorName := reservedName(deathWatchType)
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
		actorName := reservedName(rebalancerType)

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
	actorName := reservedName(deadletterType)
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
			kind := types.Name(actor)
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
func (x *actorSystem) cleanupCluster(ctx context.Context, pids []*PID) error {
	eg, ctx := errgroup.WithContext(ctx)

	// Remove singleton actors from the cluster.
	// We check pid.singletonSpec != nil rather than pid.IsSingleton() because
	// pid.reset() clears singletonState during actor shutdown. By the time
	// cleanupCluster runs the actors have already been stopped, so IsSingleton()
	// would always return false and RemoveKind would never be called, leaving
	// the kind registered in the cluster and blocking relocation on the new leader.
	if x.cluster.IsLeader(ctx) {
		for _, pid := range pids {
			if pid.singletonSpec != nil {
				pid := pid
				eg.Go(func() error {
					kind := pid.Kind()
					if err := x.cluster.RemoveKind(ctx, kind); err != nil {
						x.logger.Errorf("failed to remove kind=%s from cluster: %v (hint: check cluster connectivity)", kind, err)
						return err
					}
					x.logger.Debugf("kind=%s removed from cluster", kind)
					return nil
				})
			}
		}
	}

	// Remove all actors from the cluster
	for _, pid := range pids {
		eg.Go(func() error {
			actorName := pid.Name()
			if err := x.cluster.RemoveActor(ctx, actorName); err != nil {
				x.logger.Errorf("failed to remove actor=%s from cluster: %v (hint: check cluster connectivity)", actorName, err)
				return err
			}
			x.logger.Debugf("actor=%s removed from cluster", actorName)
			return nil
		})
	}

	// Remove all grains from the cluster if exists
	if x.grains.Len() > 0 {
		for _, grain := range x.grains.Values() {
			eg.Go(func() error {
				if err := x.cluster.RemoveGrain(ctx, grain.identity.String()); err != nil {
					x.logger.Errorf("failed to remove grain=%s from cluster: %v (hint: check cluster connectivity)", grain.identity.String(), err)
					return err
				}
				x.logger.Debugf("grain=%s removed from cluster", grain.identity.String())
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
		message = new(commands.DeadlettersCountRequest)
	)
	if to.IsRunning() {
		// ask the deadletter actor for the count
		// using the default ask timeout
		// note: no need to check for error because this call is internal
		reply, _ := from.Ask(ctx, to, message, DefaultAskTimeout)
		// Be defensive: if the actor is shutting down or a call was timed out and a late
		// response got dropped, reply can be nil or an unexpected type.
		if deadlettersCount, ok := reply.(*commands.DeadlettersCountResponse); ok && deadlettersCount != nil {
			x.deadlettersCounter.Store(uint64(deadlettersCount.TotalCount))
		}
	}
}

func (x *actorSystem) shutdownCluster(ctx context.Context, actors []*PID, peerState *internalpb.PeerState) error {
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
				AddContextRunnerIf(x.clusterStore != nil, func(_ context.Context) error { return x.clusterStore.Close() }).
				Run(); err != nil {
				x.logger.Errorf("failed to shutdown cleanly: %v (hint: check actor PostStop)", err)
				return err
			}
		}

		// actorsQueue and grainsQueue are deliberately not closed here:
		// producers select against shutdownSignal (closed earlier in
		// shutdown / startupCleanup), and drainers exit on the same
		// signal after draining buffered items. Not closing avoids the
		// send-on-closed-channel race entirely.
		x.clusterEnabled.Store(false)
		x.pubsubEnabled.Store(false)

		// drop any in-flight relocation jobs; their workers are stopped with
		// the actor tree and the cluster store is already closed
		x.relocationJobsLocker.Lock()
		x.relocationJobs = make(map[string]*internalpb.PeerState)
		x.relocationJobsLocker.Unlock()

		x.peerRemotingPorts.Reset()
		x.relocatingEndpoints.Reset()
		x.recentDepartures.Reset()
	}
	return nil
}

func (x *actorSystem) shutdownRemoting(ctx context.Context) error {
	if x.remotingEnabled.Load() {
		if x.remoting != nil {
			x.remoting.Close()
		}

		// Close the fan-out queue after the coalescer has finished its final
		// flushes so late errors still get a chance to be recorded. The drain
		// goroutine itself is best-effort: the dead-letter actor may already
		// be stopped by this point (its Shutdown runs earlier in the
		// coordinated sequence), in which case deadLetterRemoteMessage bails
		// out cleanly.
		x.stopCoalescedFailureDrain()

		if x.remoteServer != nil {
			timeout, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			// Extract deadline from context as duration
			deadline, _ := timeout.Deadline()
			shutdownTimeout := time.Until(deadline)

			if err := x.stopRemoteServer(shutdownTimeout); err != nil {
				x.logger.Errorf("failed to shutdown remote server: %v (hint: check remoting bind port, connections)", err)
				return err
			}
			x.remotingEnabled.Store(false)
			x.remoteServer = nil
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
	x.logger.Info("start the system wide eviction loop")
	x.logger.Debugf("system eviction policy=%s", x.evictionStrategy.String())
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
	x.logger.Info("system wide eviction loop stopped")
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
			x.logger.Errorf("failed to shutdown actor=%s: %v (hint: check PostStop)", actor.Name(), err)
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

	actors := x.localActors()
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

	actors := x.localActors()
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

	actors := x.localActors()
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

// getDispatcher returns the shared worker pool that drives mailbox
// processing for both actors and grains. The dispatcher is created in
// NewActorSystem and started in Start; callers must not assume it is
// running outside of that lifecycle window.
func (x *actorSystem) getDispatcher() *dispatcher {
	return x.dispatcher
}

func (x *actorSystem) registerMetrics() error {
	if x.metricProvider != nil && x.metricProvider.Meter() != nil {
		meter := x.metricProvider.Meter()
		metrics, err := metric.NewActorSystemMetric(meter)
		if err != nil {
			return err
		}

		if x.relocationMetric, err = metric.NewRelocationMetric(meter); err != nil {
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

// recordRelocationMetrics records the outcome of a single departed node's
// relocation: the wall-clock duration plus how many actors and grains were
// relocated versus failed. It is a no-op when OpenTelemetry metrics are not
// enabled, so the relocation worker can call it unconditionally.
func (x *actorSystem) recordRelocationMetrics(ctx context.Context, departed string, duration time.Duration, relocated, failed int) {
	if x.relocationMetric == nil {
		return
	}

	attrs := otelmetric.WithAttributes(
		attribute.String("actor.system", x.Name()),
		attribute.String("relocation.node", departed),
	)

	// An aborted relocation carries no meaningful wall-clock duration (nothing
	// ran); recording its zero would skew the histogram toward instant
	// rebalances, so only genuine durations are sampled.
	if duration > 0 {
		x.relocationMetric.Duration().Record(ctx, duration.Milliseconds(), attrs)
	}

	x.relocationMetric.Relocated().Add(ctx, int64(relocated), attrs)
	x.relocationMetric.Failed().Add(ctx, int64(failed), attrs)
}

// reportAbortedRelocation performs the shared accounting for a relocation that
// cannot run to completion: the leader could not enumerate peers, the worker
// failed to spawn, or the worker died abnormally. It applies the same rule as
// the normal per-item path so the same outage produces the same failure count
// regardless of where it aborts:
//
//   - every relocatable actor is a failure (none were recreated);
//   - relocation-disabled grains are lost with the node by design and are
//     never reported;
//   - eager grains are failures (none were reactivated);
//   - lazy grains have their stale directory entry released locally so the next
//     TellGrain/AskGrain re-activates them on a survivor; only a lazy grain
//     whose release fails is a failure, because its entry still points at the
//     dead node and the fast path does not self-heal a stale owner.
//
// It publishes the RelocationFailed event and records the relocation metrics.
// Deleting the peer-state snapshot and releasing the relocation job stay with
// the caller, since those steps differ between the abort sites.
func (x *actorSystem) reportAbortedRelocation(ctx context.Context, pid *PID, peersAddress string, peerState *internalpb.PeerState, elapsed time.Duration, cause error) {
	departedNode := address.FormatHostPort(peerState.GetHost(), int(peerState.GetRemotingPort()))

	// The abort runs precisely when the cluster is already unhealthy (peers
	// unreachable or the worker dead), and it executes inside the relocator's
	// message handler, so the directory cleanup is bounded and fanned out
	// rather than issued as unbounded sequential round trips: a grain whose
	// release cannot complete within the budget is reported as failed instead
	// of stalling the relocator's mailbox.
	rctx, cancel := context.WithTimeout(ctx, abortedRelocationCleanupTimeout)
	defer cancel()

	var mu sync.Mutex
	failedGrains := make(map[string]*internalpb.Grain)

	eg := new(errgroup.Group)
	eg.SetLimit(defaultRelocationConcurrency)

	for id, grain := range peerState.GetGrains() {
		if grain.GetDisableRelocation() {
			continue
		}

		if grain.GetEagerRelocation() {
			failedGrains[id] = grain
			continue
		}

		eg.Go(func() error {
			if err := x.releaseGrainForLazyRelocation(rctx, grain, departedNode); err != nil {
				x.logger.Errorf("node=%s failed to release lazy grain=%s directory entry on aborted relocation: %v (hint: grain may be unreachable until re-registered; check cluster quorum)", x.String(), grain.GetGrainId().GetValue(), err)

				mu.Lock()
				failedGrains[id] = grain
				mu.Unlock()
			}
			return nil
		})
	}

	_ = eg.Wait()

	publishRelocationFailed(pid, peersAddress, peerState.GetActors(), failedGrains, cause)
	x.recordRelocationMetrics(ctx, peersAddress, elapsed, 0, len(peerState.GetActors())+len(failedGrains))
}

// recordRelocationHandoff records that a name-based send was buffered and
// retried while its target was being relocated onto a surviving node. It is a
// no-op when OpenTelemetry metrics are not enabled, so the send path can call
// it unconditionally.
func (x *actorSystem) recordRelocationHandoff(ctx context.Context) {
	if x.relocationMetric == nil {
		return
	}

	x.relocationMetric.Buffered().Add(ctx, 1, otelmetric.WithAttributes(
		attribute.String("actor.system", x.Name()),
	))
}

// preShutdown builds the peer state snapshot for cluster persistence.
// This snapshot includes all actors and grains currently active on this node.
// The actual persistence to remote peers happens later in shutdownCluster
// to ensure proper ordering: persist state before leaving membership.
func (x *actorSystem) preShutdown() (*internalpb.PeerState, error) {
	if !x.relocationEnabled.Load() {
		x.logger.Debugf("relocation disabled; skipping peer state build node=%s", x.PeersAddress())
		return nil, nil
	}

	if !x.clusterEnabled.Load() || x.cluster == nil {
		x.logger.Debugf("node=%s not in cluster; skipping peer state build", x.PeersAddress())
		return nil, nil
	}

	actors := x.localActors()
	grains := x.grains.Values()

	wireActors := make(map[string]*internalpb.Actor, len(actors))
	for _, actor := range actors {
		if !actor.IsRelocatable() {
			continue // actor is not relocatable, skip it
		}

		wireActor, err := actor.toSerialize()
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
		x.logger.Errorf("node=%s failed to get cluster peers: %v (hint: check cluster connectivity)", x.PeersAddress(), err)
		return err
	}

	if len(peers) == 0 {
		x.logger.Debugf("node=%s found no cluster peers to persist state", x.PeersAddress())
		return nil
	}

	peerAddr := x.PeersAddress()
	totalPeers := len(peers)
	x.logger.Debugf("node=%s replicating state to peers=%d", peerAddr, totalPeers)

	// Create a cancellable context for early termination after quorum
	rpcCtx, cancelRPCs := context.WithCancel(ctx)
	defer cancelRPCs()

	// Create a custom remoting client for replication with compression enabled
	remotingOpts := []remoteclient.ClientOption{
		remoteclient.WithClientCompression(x.remoteConfig.Compression()),
		remoteclient.WithClientContextPropagator(x.remoteConfig.ContextPropagator()),
	}

	if x.tlsInfo != nil {
		remotingOpts = append(remotingOpts, remoteclient.WithClientTLS(x.tlsInfo.ClientConfig))
	}

	remoting := remoteclient.NewClient(remotingOpts...)
	defer remoting.Close()

	// Channel to collect results from all goroutines
	// Each goroutine sends exactly one result (nil for success, error for failure)
	results := make(chan error, totalPeers)

	// Launch parallel RPCs to all selected peers
	for _, peer := range peers {
		go func() {
			// Get pooled proto TCP client
			client := remoting.NetClient(peer.Host, peer.RemotingPort)
			request := &internalpb.PersistPeerStateRequest{
				PeerState: peerState,
			}

			// Send request using proto TCP
			resp, err := client.SendProto(rpcCtx, request)
			if err != nil {
				x.logger.Errorf("node=%s failed to persist peer state to peer=%s:%d: %v (hint: check peer reachability)",
					peerAddr, peer.Host, peer.RemotingPort, err)
				results <- err
				return
			}

			// Check for proto errors
			if errResp, ok := resp.(*internalpb.Error); ok {
				err := fmt.Errorf("proto error: code=%s, msg=%s", errResp.GetCode(), errResp.GetMessage())
				x.logger.Errorf("node=%s failed to persist peer state to peer=%s:%d: %v (hint: check remote handler)",
					peerAddr, peer.Host, peer.RemotingPort, err)
				results <- err
				return
			}

			x.logger.Debugf("node=%s persisted peer state to peer=%s:%d", peerAddr, peer.Host, peer.RemotingPort)
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
					x.logger.Debugf("node=%s replication quorum reached peers=%d/%d", peerAddr, successCount, totalPeers)
					return nil
				}
			} else if !errors.Is(err, context.Canceled) {
				// Don't count context cancellation as a real failure
				lastErr = err
			}
		case <-ctx.Done():
			// Parent context cancelled (e.g., shutdown timeout)
			x.logger.Warnf("node=%s replication interrupted: %v (successes=%d/%d)", peerAddr, ctx.Err(), successCount, totalPeers)
			if successCount > 0 {
				return nil // Partial success is acceptable
			}
			return fmt.Errorf("replication interrupted: %w", ctx.Err())
		}
	}

	// All RPCs completed without reaching quorum
	if successCount > 0 {
		x.logger.Warnf("node=%s partial replication: peers=%d/%d acknowledged", peerAddr, successCount, totalPeers)
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
		x.logger.Debug("no peers available, skipping state replication")
		return nil, nil
	}

	// Edge case: Fewer peers than k
	if n < k {
		x.logger.Debugf("only %d peers available, replicating to all", n)
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
// maxInt returns the maximum of two integers. Helper for calculation.
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// reservedName returns the reserved actor's name
func reservedName(nameType nameType) string {
	return reservedNames[nameType]
}
