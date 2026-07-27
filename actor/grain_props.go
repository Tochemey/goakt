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
	"time"

	"github.com/tochemey/goakt/v4/extension"
)

// GrainProps encapsulates configuration and metadata for a Grain (virtual actor) in the goakt actor system.
//
// It holds the unique identity of the Grain and a reference to the ActorSystem that manages it.
// GrainProps is used internally to track and manage Grain instances, ensuring each Grain is uniquely addressable
// and associated with the correct actor system.
//
// Fields:
//   - identity:    The unique identity of the Grain, used for addressing and routing.
//   - actorSystem: The ActorSystem instance that owns and manages the Grain.
type GrainProps struct {
	identity     *GrainIdentity
	actorSystem  ActorSystem
	dependencies []extension.Dependency
	pid          *grainPID
}

// newGrainProps creates and returns a new GrainProps instance for the specified identity and actor system.
//
// This function is used internally by the framework to construct the properties required to manage a Grain.
//
// Parameters:
//   - identity:    The unique identity of the Grain.
//   - actorSystem: The ActorSystem that will manage the Grain.
//   - dependencies: The dependencies injected into the Grain.
//   - pid:         The grain process backing the Grain; carries the timer registry.
//
// Returns:
//   - *GrainProps: A new instance containing the provided identity and actor system.
func newGrainProps(identity *GrainIdentity, actorSystem ActorSystem, dependencies []extension.Dependency, pid *grainPID) *GrainProps {
	return &GrainProps{
		identity:     identity,
		actorSystem:  actorSystem,
		dependencies: dependencies,
		pid:          pid,
	}
}

// Identity returns the unique identity of the Grain associated with these properties.
//
// The GrainIdentity is used to uniquely identify and address the Grain within the actor system.
// This is typically a composite of the Grain's type and instance key.
//
// Returns:
//   - *GrainIdentity: The identity object representing this Grain.
func (props *GrainProps) Identity() *GrainIdentity {
	return props.identity
}

// ActorSystem returns the ActorSystem instance associated with these Grain properties.
//
// This provides access to the actor system that manages the lifecycle, messaging, and clustering
// for the Grain represented by these properties.
//
// Returns:
//   - ActorSystem: The actor system managing this Grain.
func (props *GrainProps) ActorSystem() ActorSystem {
	return props.actorSystem
}

// Dependencies returns the dependencies registered with this GrainProps.
//
// Dependencies are external services, resources, or components that the Grain requires to operate.
// These are typically injected into the Grain at creation time, enabling loose coupling and easier testing.
//
// Returns:
//   - []extension.Dependency: A slice containing all dependencies associated with this GrainProps instance.
//
// Example usage:
//
//	deps := grainProps.Dependencies()
//	for _, dep := range deps {
//	    // Use dep as needed
//	}
func (props *GrainProps) Dependencies() []extension.Dependency {
	return props.dependencies
}

// ScheduleOnce registers a volatile timer that delivers message to this Grain
// exactly once after delay.
//
// Registering timers from OnActivate is the canonical way to start a Grain's
// periodic behavior: timers registered there stay dormant until activation
// completes and are discarded when activation fails. From OnDeactivate the
// registry is already stopped and registration returns ErrGrainTimersStopped.
//
// See GrainContext.ScheduleOnce for the full timer semantics.
func (props *GrainProps) ScheduleOnce(message any, delay time.Duration, opts ...GrainTimerOption) (string, error) {
	registry, err := props.pid.timerRegistry()
	if err != nil {
		return "", err
	}
	return registry.scheduleOnce(message, delay, opts...)
}

// Schedule registers a volatile timer that delivers message to this Grain
// repeatedly at the given fixed interval.
//
// Registering timers from OnActivate is the canonical way to start a Grain's
// periodic behavior: timers registered there stay dormant until activation
// completes and are discarded when activation fails. From OnDeactivate the
// registry is already stopped and registration returns ErrGrainTimersStopped.
//
// See GrainContext.Schedule for the full timer semantics.
func (props *GrainProps) Schedule(message any, interval time.Duration, opts ...GrainTimerOption) (string, error) {
	registry, err := props.pid.timerRegistry()
	if err != nil {
		return "", err
	}
	return registry.scheduleInterval(message, interval, opts...)
}

// ScheduleWithCron registers a volatile timer that delivers message to this
// Grain at the instants described by cronExpression.
//
// Registering timers from OnActivate is the canonical way to start a Grain's
// periodic behavior: timers registered there stay dormant until activation
// completes and are discarded when activation fails. From OnDeactivate the
// registry is already stopped and registration returns ErrGrainTimersStopped.
//
// See GrainContext.ScheduleWithCron for the full timer semantics.
func (props *GrainProps) ScheduleWithCron(message any, cronExpression string, opts ...GrainTimerOption) (string, error) {
	registry, err := props.pid.timerRegistry()
	if err != nil {
		return "", err
	}
	return registry.scheduleCron(message, cronExpression, opts...)
}

// CancelSchedule cancels the Grain timer registered under reference.
//
// See GrainContext.CancelSchedule for the full cancellation semantics.
func (props *GrainProps) CancelSchedule(reference string) error {
	registry, err := props.pid.timerRegistry()
	if err != nil {
		return err
	}
	return registry.cancel(reference)
}
