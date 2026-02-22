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

package errors

import (
	"errors"
	"fmt"

	"github.com/tochemey/goakt/v4/passivation"
)

var (
	// ErrInvalidActorSystemName is returned when the actor system name contains invalid characters.
	// A valid name must consist of only alphanumeric characters ([a-zA-Z0-9]), with optional
	// hyphens or underscores that are not leading.
	ErrInvalidActorSystemName = errors.New("invalid ActorSystem name, must contain only word characters (i.e. [a-zA-Z0-9] plus non-leading '-' or '_')")

	// ErrDead indicates that the actor is no longer alive or has been terminated.
	ErrDead = errors.New("actor is not alive")

	// ErrUnhandled is returned when an actor receives a message it cannot handle.
	ErrUnhandled = errors.New("unhandled message")

	// ErrClusterDisabled indicates an attempt to access cluster-specific features
	// when clustering is not enabled for the actor system.
	ErrClusterDisabled = errors.New("cluster is not enabled")

	// ErrUndefinedActor is returned when an actor reference is undefined or unknown in the system.
	ErrUndefinedActor = errors.New("actor is not defined")

	// ErrRequestTimeout indicates that an Ask message timed out while waiting for a response.
	ErrRequestTimeout = errors.New("request timed out")
	// ErrRequestCanceled indicates that an async request was canceled before completion.
	ErrRequestCanceled = errors.New("request canceled")
	// ErrReentrancyDisabled indicates async requests are disabled for the actor.
	ErrReentrancyDisabled = errors.New("reentrancy is disabled")
	// ErrInvalidReentrancyMode indicates a reentrancy mode is not supported.
	ErrInvalidReentrancyMode = errors.New("invalid reentrancy mode")
	// ErrReentrancyInFlightLimit indicates an actor has reached its async in-flight limit.
	ErrReentrancyInFlightLimit = errors.New("reentrancy in-flight limit reached")

	// ErrRemotingDisabled is returned when remote messaging is attempted but remoting is not enabled.
	ErrRemotingDisabled = errors.New("remoting is not enabled")

	// ErrAddressNotFound is returned when an actor's address cannot be resolved.
	ErrAddressNotFound = errors.New("address not found")

	// ErrRemoteSendFailure is returned when sending a remote message fails due to network or protocol issues.
	ErrRemoteSendFailure = errors.New("remote send failed")

	// ErrNameRequired is returned when an actor system name is required but not provided.
	ErrNameRequired = errors.New("actor system is required")

	// ErrInvalidInstance indicates a failure to create an actor instance due to an invalid type or configuration.
	ErrInvalidInstance = errors.New("failed to create instance. Reason: invalid instance")

	// ErrActorNotFound indicates that the specified actor could not be found in the system.
	ErrActorNotFound = errors.New("actor not found")

	// ErrMethodCallNotAllowed is returned when an RPC-style method call is attempted but not permitted.
	ErrMethodCallNotAllowed = errors.New("method call not allowed")

	// ErrInvalidRemoteMessage indicates that the message sent over the network is malformed or unsupported.
	ErrInvalidRemoteMessage = errors.New("invalid remote message")

	// ErrStashBufferNotSet is returned when an actor tries to stash a message but no stash buffer is configured.
	ErrStashBufferNotSet = errors.New("actor is not setup with a stash buffer")

	// ErrInitFailure is returned when the actor's preStart hook fails during initialization.
	ErrInitFailure = errors.New("preStart failed")

	// ErrActorSystemNotStarted indicates that an actor system has not been started before use.
	ErrActorSystemNotStarted = errors.New("actor system is not running")

	// ErrReservedName is returned when attempting to register an actor with a reserved name.
	ErrReservedName = errors.New("actor name is reserved")

	// ErrInstanceNotAnActor is returned when the instantiated type does not implement the Actor interface.
	ErrInstanceNotAnActor = errors.New("failed to create instance. Reason: instance does not implement the Actor interface")

	// ErrTypeNotRegistered is returned when attempting to use an unregistered actor type.
	ErrTypeNotRegistered = errors.New("actor type is not registered")

	// ErrPeerNotFound is returned when the specified peer in the cluster is not available.
	ErrPeerNotFound = errors.New("peer is not found")

	// ErrUndefinedTask is returned when piping a result to an undefined long-running task.
	ErrUndefinedTask = errors.New("task is not defined")

	// ErrInvalidHost is returned when the specified remote host is invalid or cannot be resolved.
	ErrInvalidHost = errors.New("invalid host")

	// ErrSchedulerNotStarted is returned when attempting to use the scheduler before it has started.
	ErrSchedulerNotStarted = errors.New("scheduler has not started")

	// ErrInvalidMessage indicates that a message is structurally or semantically invalid.
	ErrInvalidMessage = errors.New("invalid message")

	// ErrInvalidTimeout is returned when a timeout value is less than or equal to zero.
	ErrInvalidTimeout = errors.New("invalid timeout")

	// ErrActorAlreadyExists is returned when trying to create an actor with a name that already exists.
	ErrActorAlreadyExists = errors.New("actor already exists")

	// ErrInvalidTLSConfiguration is returned when TLS settings are missing or misconfigured.
	ErrInvalidTLSConfiguration = errors.New("TLS configuration is invalid")

	// ErrSingletonAlreadyExists is returned when a singleton actor type is already registered.
	ErrSingletonAlreadyExists = errors.New("singleton already exists")

	// ErrLeaderNotFound is returned when the cluster leader (oldest node) cannot be found.
	ErrLeaderNotFound = errors.New("leader is not found")
	// ErrWriteQuorum is returned when the cluster cannot reach the configured write quorum.
	ErrWriteQuorum = errors.New("write quorum cannot be reached")
	// ErrReadQuorum is returned when the cluster cannot reach the configured read quorum.
	ErrReadQuorum = errors.New("read quorum cannot be reached")
	// ErrClusterQuorum is returned when the cluster cannot reach the minimum member quorum.
	ErrClusterQuorum = errors.New("cluster quorum cannot be reached")

	// ErrDependencyTypeNotRegistered is returned when a cluster-aware dependency type is not registered.
	ErrDependencyTypeNotRegistered = errors.New("dependency type is not registered")

	// ErrInstanceNotDependency is returned when an instance does not implement the required Dependency interface.
	ErrInstanceNotDependency = errors.New("failed to create instance. Reason: instance does not implement the Dependency interface")

	// ErrActorSystemAlreadyStarted is returned when attempting to start an actor system that is already running.
	ErrActorSystemAlreadyStarted = errors.New("actor system has already started")

	// ErrScheduledReferenceNotFound is returned when a reference to a scheduled job cannot be found.
	ErrScheduledReferenceNotFound = errors.New("scheduled reference not found")

	// ErrGrainActivationFailure is returned when Grain activation failed
	ErrGrainActivationFailure = errors.New("grain activation failed")

	// ErrGrainDeactivationFailure is returned when Grain deactivation failed
	ErrGrainDeactivationFailure = errors.New("grain deactivation failed")

	// ErrGrainActivationBarrierTimeout is returned when the activation barrier times out.
	ErrGrainActivationBarrierTimeout = errors.New("grain activation barrier timeout")

	// ErrInvalidGrainIdentity is returned when a Grain identity is malformed or invalid.
	ErrInvalidGrainIdentity = errors.New("invalid graind identity")

	// ErrInstanceNotAnGrain is returned when the instantiated type does not implement the Grain interface.
	ErrInstanceNotAnGrain = errors.New("failed to create instance. Reason: instance does not implement the Grain interface")

	// ErrGrainNotRegistered is returned when attempting to use a Grain type that has not been registered.
	ErrGrainNotRegistered = errors.New("grain type is not registered")

	// ErrUnhanledMessage is returned when a message is received that the actor/grain does not know how to handle.
	ErrUnhanledMessage = errors.New("unhandled message")

	// ErrInvalidPassivationStrategy is returned when an invalid passivation strategy is specified.
	ErrInvalidPassivationStrategy = errors.New("invalid passivation strategy, must be one of: 'time-based', 'messages-count-based', or 'long-lived'")

	// ErrInvalidEvictionPolicy is returned when an invalid eviction policy is specified.
	ErrInvalidEvictionPolicy = errors.New("invalid eviction policy")

	// ErrShutdownForbidden is returned when an attempt is made to shutdown a system actor while the actor system is running.
	ErrShutdownForbidden = errors.New("shutdown forbidden for this actor")

	// ErrNotLocal is returned when a lifecycle or tree-navigation operation is called on a remote PID.
	// These operations require a live local actor mailbox and actor-system reference, which remote
	// PIDs do not carry. Use pid.IsLocal() to guard before calling such methods.
	ErrNotLocal = errors.New("operation requires a local actor PID")

	// ErrInvalidRouterPoolSize is returned when a router is configured with a pool size less than or equal to zero.
	ErrInvalidRouterPoolSize = errors.New("invalid router pool size, must be greater than zero")

	// ErrTailChopingRouterMisconfigured is returned when a tail-chopping router is misconfigured.
	ErrTailChopingRouterMisconfigured = errors.New("tail-chopping router misconfigured: interval and within must be greater than zero")

	// ErrScatterGatherFirstRouterMisconfigured is returned when a scatter-gather-first router is misconfigured.
	ErrScatterGatherFirstRouterMisconfigured = errors.New("scatter-gather router misconfigured: within must be greater than zero")

	// ErrMailboxFull is returned when a bounded mailbox has reached its capacity.
	ErrMailboxFull = errors.New("mailbox is full")

	// ErrMailboxDisposed is returned when operations are attempted on a disposed mailbox.
	ErrMailboxDisposed = errors.New("mailbox has been disposed")

	// ErrSystemShuttingDown is returned when a message is sent to an actor while the actor system is shutting down.
	ErrSystemShuttingDown = errors.New("actor system is shutting down")

	// ErrWatchNotSupported indicates the provider does not support watch in multi-DC control planes.
	ErrWatchNotSupported = errors.New("multidc: watch not supported")

	// ErrDataCenterNotFound indicates the requested datacenter is missing.
	ErrDataCenterRecordNotFound = errors.New("datacenter record not found")

	// ErrDataCenterRecordConflict indicates the record version does not match the current state.
	ErrDataCenterRecordConflict = errors.New("datacenter record version conflict")

	// ErrDataCenterNotReady indicates the datacenter is not ready to process requests.
	// The controller is considered ready when:
	//   - Multi-DC mode is enabled (cluster mode with a datacenter config)
	//   - The controller has started successfully
	//   - The cache has been refreshed at least once
	ErrDataCenterNotReady = errors.New("datacenter is not ready")

	// ErrDataCenterStaleRecords indicates the datacenter has stale records and cannot process requests.
	ErrDataCenterStaleRecords = errors.New("datacenter has stale records")
)

// NewErrInvalidPassivationStrategy formats an error with ErrInvalidPassivationStrategy
func NewErrInvalidPassivationStrategy(strategy passivation.Strategy) error {
	return fmt.Errorf("passivation strategy=(%s) %w", strategy.String(), ErrInvalidPassivationStrategy)
}

// NewErrUnhandledMessage wraps a base error with ErrUnhanledMessage to indicate an unhandled message.
func NewErrUnhandledMessage(err error) error {
	return errors.Join(ErrUnhanledMessage, err)
}

// NewErrGrainActivationFailure wraps a base error with ErrGrainActivationFailure to indicate a Grain activation failure
func NewErrGrainActivationFailure(err error) error {
	return errors.Join(ErrGrainActivationFailure, err)
}

// NewErrGrainDeactivationFailure wraps an error with ErrGrainDeactivationFailure to indicate a Grain deactivation failure
func NewErrGrainDeactivationFailure(err error) error {
	return errors.Join(ErrGrainDeactivationFailure, err)
}

// NewErrInvalidGrainIdentity wraps an error with ErrInvalidGrainIdentity to indicate a Grain identity issue.
func NewErrInvalidGrainIdentity(err error) error {
	return errors.Join(ErrInvalidGrainIdentity, err)
}

// NewErrReservedName formats an ErrReservedName with the given name.
func NewErrReservedName(name string) error {
	return fmt.Errorf("name=(%s) %w", name, ErrReservedName)
}

// NewErrActorNotFound formats an ErrActorNotFound with the given actor path.
func NewErrActorNotFound(actorPath string) error {
	return fmt.Errorf("(actor=%s) %w", actorPath, ErrActorNotFound)
}

// NewErrAddressNotFound formats an ErrAddressNotFound with the given actor address.
func NewErrAddressNotFound(addr string) error {
	return fmt.Errorf("(actor address=%s) %w", addr, ErrAddressNotFound)
}

// NewErrRemoteSendFailure wraps an error into an ErrRemoteSendFailure using internal server code.
func NewErrRemoteSendFailure(err error) error {
	return errors.Join(ErrRemoteSendFailure, err)
}

// NewErrActorAlreadyExists formats an ErrActorAlreadyExists for the given actor name.
func NewErrActorAlreadyExists(actorName string) error {
	return fmt.Errorf("actor=(%s) %w", actorName, ErrActorAlreadyExists)
}

// NewErrInvalidMessage wraps a base error with ErrInvalidMessage for additional context.
func NewErrInvalidMessage(err error) error {
	return errors.Join(ErrInvalidMessage, err)
}

// NewErrInvalidRemoteMessage wraps a base error with ErrInvalidRemoteMessage for additional context.
func NewErrInvalidRemoteMessage(err error) error {
	return errors.Join(ErrInvalidRemoteMessage, err)
}

// NewErrInitFailure wraps a base error with ErrInitFailure to indicate a startup failure.
func NewErrInitFailure(err error) error {
	return errors.Join(ErrInitFailure, err)
}

// PanicError defines the panic error
// wrapping the underlying error
type PanicError struct {
	err error
}

// enforce compilation error
var _ error = (*PanicError)(nil)

// NewPanicError creates an instance of PanicError
func NewPanicError(err error) *PanicError {
	return &PanicError{err}
}

// Error implements the standard error interface
func (e *PanicError) Error() string {
	return fmt.Sprintf("panic: %v", e.err)
}

func (e *PanicError) Unwrap() error {
	return e.err
}

// InternalError defines an error that is explicit to the application
type InternalError struct {
	err error
}

// enforce compilation error
var _ error = (*InternalError)(nil)

// NewInternalError returns an intance of InternalError
func NewInternalError(err error) *InternalError {
	return &InternalError{
		err: fmt.Errorf("internal error: %w", err),
	}
}

// Error implements the standard error interface
func (i *InternalError) Error() string {
	return i.err.Error()
}

func (i *InternalError) Unwrap() error {
	return i.err
}

// SpawnError defines an error when re/creating an actor
type SpawnError struct {
	err error
}

var _ error = (*SpawnError)(nil)

// NewSpawnError returns an instance of SpawnError
func NewSpawnError(err error) *SpawnError {
	return &SpawnError{
		err: fmt.Errorf("spawn error: %w", err),
	}
}

// Error implements the standard error interface
func (s *SpawnError) Error() string {
	return s.err.Error()
}

func (s *SpawnError) Unwrap() error {
	return s.err
}

type RebalancingError struct {
	err error
}

var _ error = (*RebalancingError)(nil)

// NewRebalancingError returns an instance of RebalancingError
func NewRebalancingError(err error) *RebalancingError {
	return &RebalancingError{err}
}

func (e *RebalancingError) Error() string {
	return fmt.Errorf("rebalancing: %w", e.err).Error()
}

func (e *RebalancingError) Unwrap() error {
	return e.err
}

// AnyError defines the any error type
// this is used to represent any error when handling the supervisor directive
type AnyError struct{}

// interface guard
var _ error = (*AnyError)(nil)

// Error implements error.
func (*AnyError) Error() string {
	return "*"
}
