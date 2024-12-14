/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actors

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/errorschain"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/persistence"
)

// CommandResponse defines the command response type
type CommandResponse interface {
	isResponse()
}

// StateCommandResponse defines the command response type
// with the durable state actor to persist
type StateCommandResponse struct {
	state     *DurableState
	actorName string
}

// NewStateCommandResponse creates an instance of StateCommandResponse.
func NewStateCommandResponse(resultingState *DurableState) *StateCommandResponse {
	return &StateCommandResponse{
		state: resultingState,
	}
}

// Forward sets the recipient of the resulting state.
// The command handler needs to call this method to set a potential recipient of the new actor state.
// Only the actual state will be sent to the recipient without the latest version number
func (r *StateCommandResponse) Forward(actorName string) *StateCommandResponse {
	r.actorName = actorName
	return r
}

// DurableState returns the durable state to persist
func (r *StateCommandResponse) DurableState() *DurableState {
	return r.state
}

// ActorName returns the recipient that will receive the resultingState after it has been persisted
// One need to set the pid using the Forward during the instantiation of the
// StateCommandResponse
func (r *StateCommandResponse) ActorName() string {
	return r.actorName
}

// implements CommandResponse
func (r *StateCommandResponse) isResponse() {}

// DeleteStateCommandResponse defines the delete command state response.
// With this response the current actor state is set to the actor
// empty state with the version incremented by 1.
type DeleteStateCommandResponse struct{}

// NewDeleteStateCommandResponse creates an instance of delete effect
func NewDeleteStateCommandResponse() CommandResponse {
	return &DeleteStateCommandResponse{}
}

// implements CommandResponse
func (r *DeleteStateCommandResponse) isResponse() {}

// StopCommandResponse defines the stop response command
// With this command response the durable state actor is stopped
type StopCommandResponse struct{}

// implements CommandResponse
func (s *StopCommandResponse) isResponse() {
}

// NewStopCommandResponse creates an instance of StopCommandResponse.
func NewStopCommandResponse() CommandResponse {
	return new(StopCommandResponse)
}

// NoStateCommandResponse will not persist any state
type NoStateCommandResponse struct {
}

// NewNoStateCommandResponse creates an instance of NoStateCommandResponse
// With this command response not state is persisted
func NewNoStateCommandResponse() CommandResponse {
	return &NoStateCommandResponse{}
}

// implements CommandResponse
func (r *NoStateCommandResponse) isResponse() {}

// ForwardStateCommandResponse is used to forward the
// actor state to the given actor
type ForwardStateCommandResponse struct {
	actorName string
}

// implements CommandResponse
func (r *ForwardStateCommandResponse) isResponse() {}

// NewForwardStateCommandResponse creates a new forwardEffect with the provided state and recipient.
// This effect will send the resulting state the recipient without persisting it.
func NewForwardStateCommandResponse(actorName string) CommandResponse {
	return &ForwardStateCommandResponse{
		actorName: actorName,
	}
}

// ActorName returns the recipient of the resulting state
func (r *ForwardStateCommandResponse) ActorName() string {
	return r.actorName
}

// ErrorCommandResponse is returned when the command processing
// has failed
type ErrorCommandResponse struct {
	err error
}

// NewErrorCommandResponse creates an instance of CommandResponse
func NewErrorCommandResponse(err error) CommandResponse {
	return &ErrorCommandResponse{err: err}
}

// Error returns the actual error
func (r *ErrorCommandResponse) Error() error {
	return r.err
}

// implements CommandResponse
func (r *ErrorCommandResponse) isResponse() {}

// DurableState defines the durable state
// sent when handling a given command.
type DurableState struct {
	// state specifies the durable state actor
	state ActorState
	// version specifies the prior version of the durable state actor.
	version uint64
}

// NewDurableState creates an instance of DurableState
func NewDurableState(state ActorState, version uint64) *DurableState {
	return &DurableState{
		state:   state,
		version: version,
	}
}

// Version returns the version of the durable state actor
func (s *DurableState) Version() uint64 {
	return s.version
}

// ActorState returns the underlying state of the actor
func (s *DurableState) ActorState() ActorState {
	return s.state
}

// Command is just an alias around proto.Message
type Command proto.Message

// ActorState defines the durable state actor state
type ActorState proto.Message

type durableStateActor struct {
	behavior     DurableStateBehavior
	stateStore   persistence.DurableStateStore
	currentState *DurableState
	pid          *PID
	logger       log.Logger
}

// enforce compilation error
var _ Actor = (*durableStateActor)(nil)

// newDurableStateActor creates an instance of durable state actor
func newDurableStateActor(behavior DurableStateBehavior) *durableStateActor {
	return &durableStateActor{
		behavior: behavior,
	}
}

// PreStart pre-starts the actor. On boot, the actor
// will try to recover from the durable state store
func (s *durableStateActor) PreStart(ctx context.Context) error {
	return errorschain.
		New(errorschain.ReturnFirst()).
		AddError(s.stateStore.Ping(ctx)).
		AddError(s.restoreState(ctx)).
		AddError(s.behavior.PreStartHook(ctx)).
		Error()
}

// Receive processes any message dropped into the actor mailbox.
func (s *durableStateActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		s.pid = ctx.Self()
		s.logger = ctx.Logger()
	default:
		command := Command(ctx.Message())
		commandResponse := s.behavior.Handle(ctx.Context(), command, s.currentState)
		switch response := commandResponse.(type) {
		case *StateCommandResponse:
			s.handleStateCommandResponse(ctx, response)
		case *NoStateCommandResponse:
			s.handleNoStateCommandResponse()
		case *DeleteStateCommandResponse:
			s.handleDeleteStateCommandResponse(ctx)
		case *ForwardStateCommandResponse:
			s.handleForwardStateCommandResponse(ctx, response)
		case *StopCommandResponse:
			s.handleStopCommandResponse(ctx)
		case *ErrorCommandResponse:
			s.handleErrorCommandResponse(ctx, command, response)
		default:
		}
	}
}

// PostStop prepares the actor to gracefully shutdown
func (s *durableStateActor) PostStop(ctx context.Context) error {
	return s.behavior.PostStopHook(ctx)
}

// restoreState restores the actor latest state
// this is vital when the actor is restarting.
func (s *durableStateActor) restoreState(ctx context.Context) error {
	latestState, err := s.stateStore.GetDurableState(ctx, s.pid.ID())
	if err != nil {
		return fmt.Errorf("failed to recover the latest state from durable state store: %w", err)
	}

	// we do have the latest state just recover from it
	if latestState != nil {
		currentState := s.behavior.EmptyState()
		currentStateType := currentState.ProtoReflect().Descriptor().FullName()
		latestStateType := latestState.State.ProtoReflect().Descriptor().FullName()
		if currentStateType != latestStateType {
			return fmt.Errorf("mismatch state types: %s != %s", currentStateType, latestStateType)
		}

		s.currentState = NewDurableState(currentState, latestState.Version)
		return nil
	}

	s.currentState = NewDurableState(s.behavior.EmptyState(), 0)
	return nil
}

// handleStateCommandResponse handles StateCommandResponse
func (s *durableStateActor) handleStateCommandResponse(ctx *ReceiveContext, commandResponse *StateCommandResponse) {
	s.currentState = commandResponse.DurableState()
	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(s.validateStateCommandResponse(commandResponse)).
		AddError(s.persistState(ctx.Context())).
		AddError(s.forwardTo(ctx.Context(), commandResponse.ActorName())).
		Error(); err != nil {
		s.logger.Errorf("%s failed handle StateCommandResponse: %v", s.pid.ID(), err)
		ctx.Err(fmt.Errorf("%s failed handle StateCommandResponse: %v", s.pid.ID(), err))
	}
}

// handleDeleteStateCommandResponse handles the DeleteStateCommandResponse
func (s *durableStateActor) handleDeleteStateCommandResponse(ctx *ReceiveContext) {
	s.currentState = NewDurableState(
		s.behavior.EmptyState(),
		s.currentState.Version()+1,
	)
	// persist the state onto the durable state store
	if err := s.persistState(ctx.Context()); err != nil {
		s.logger.Errorf("%s failed handle DeleteStateCommandResponse: %v", s.pid.ID(), err)
		ctx.Err(fmt.Errorf("%s failed handle DeleteStateCommandResponse: %v", s.pid.ID(), err))
		return
	}
}

// handleForwardStateCommandResponse handles ForwardStateCommandResponse
func (s *durableStateActor) handleForwardStateCommandResponse(ctx *ReceiveContext, commandResponse *ForwardStateCommandResponse) {
	if err := s.forwardTo(ctx.Context(), commandResponse.ActorName()); err != nil {
		s.logger.Errorf("%s failed handle ForwardStateCommandResponse: %v", s.pid.ID(), err)
		ctx.Err(fmt.Errorf("%s failed handle ForwardStateCommandResponse: %v", s.pid.ID(), err))
		return
	}
}

// handleStopCommandResponse handles  StopCommandResponse
func (s *durableStateActor) handleStopCommandResponse(ctx *ReceiveContext) {
	ctx.Shutdown()
}

// handleNoStateCommandResponse handles NoStateCommandResponse
func (s *durableStateActor) handleNoStateCommandResponse() {
	// pass
}

// handleErrorCommandResponse handles ErrorCommandResponse
func (s *durableStateActor) handleErrorCommandResponse(ctx *ReceiveContext, command Command, commandResponse *ErrorCommandResponse) {
	s.logger.Errorf("%s failed to process command: %s: %v",
		s.pid.Name(),
		command.ProtoReflect().Descriptor().FullName(),
		commandResponse.Error())

	ctx.Err(commandResponse.Error())
}

// forwardTo forward the actor state to the provided actor name
func (s *durableStateActor) forwardTo(ctx context.Context, actorName string) error {
	if actorName != "" {
		return s.pid.SendAsync(ctx, actorName, s.currentState.ActorState())
	}
	return nil
}

// persistState persist the actor durable state
func (s *durableStateActor) persistState(ctx context.Context) error {
	return s.stateStore.PersistDurableState(ctx, &persistence.DurableState{
		ActorID:        s.pid.ID(),
		State:          s.currentState.ActorState(),
		Version:        s.currentState.Version(),
		TimestampMilli: uint64(time.Now().UnixMilli()),
	})
}

// validateStateCommandResponse validates the StateCommandResponse
func (s *durableStateActor) validateStateCommandResponse(commandResponse *StateCommandResponse) error {
	durableState := commandResponse.DurableState()
	// incoming version must be current version + 1
	isValid := (durableState.Version() - s.currentState.Version()) == 1
	if !isValid {
		return fmt.Errorf("%s received version=(%d) while current version is (%d)",
			s.pid.Name(),
			durableState.Version(),
			s.currentState.Version())
	}
	return nil
}
