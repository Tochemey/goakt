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

// DurableStateBehavior defines the interface of a durable state actor / entity behavior to store the full state after processing each command.
// The current state is always stored in the durable state store. Since only the latest state is stored, there is no history of changes.
// The durable state actor engine would read that state and store it in memory. After processing of the command is finished, the new state will be stored in the durable state store.
// The processing of the next command will not start until the state has been successfully stored in the durable state store.
type DurableStateBehavior interface {
	// EmptyState returns the durable state actor empty state
	EmptyState() ActorState
	// PreStartHook pre-starts the actor. This function can be used to set up some database connections
	// or some sort of initialization before the actor start processing messages
	// when the initialization failed the actor will not be started.
	// Use this function to set any fields that will be needed before the actor starts.
	// This hook helps set the default values needed by any fields of the actor.
	PreStartHook(ctx context.Context) error
	// PostStopHook is executed when the actor is shutting down.
	// The execution happens when every message that have not been processed yet will be processed before the actor shutdowns
	// This help free-up resources
	PostStopHook(ctx context.Context) error
	// Handle helps handle commands received by the durable state actor. The command handlers define how to handle each incoming command,
	// which validations must be applied, and finally, whether a resulting state will be persisted depending upon the StatefulEffect
	// They encode the business rules of your durable state actor and act as a guardian of the actor consistency.
	// The command handler must first validate that the incoming command can be applied to the current model state.
	//  Any decision should be solely based on the data passed in the command and the state of the Behavior.
	// In case of successful validation and processing , the new state will be stored in the durable store depending upon the StatefulEffect response
	Handle(ctx context.Context, command Command, priorState *DurableState) (Effect, error)
}

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
		logger := s.logger
		command := Command(ctx.Message())
		effect, err := s.behavior.Handle(ctx.Context(), command, s.currentState)
		if err != nil {
			logger.Errorf("%s failed to process command: %s: %v", s.pid.Name(), command.ProtoReflect().Descriptor().FullName(), err)
			ctx.Err(err)
			return
		}

		// TODO refactor the implementation
		switch ef := effect.(type) {
		case *PersistEffect:
			durableState := ef.DurableState()
			// incoming version must be current version + 1
			shouldPersist := (durableState.Version() - s.currentState.Version()) == 1
			if !shouldPersist {
				// TODO: fail the whole effect
			}

			// persist the state
			toPersist := &persistence.DurableState{
				ActorID:        s.pid.ID(),
				State:          durableState.ActorState(),
				Version:        durableState.Version(),
				TimestampMilli: uint64(time.Now().UnixMilli()),
			}

			// persist the state onto the durable state store
			if err := s.stateStore.PersistDurableState(ctx.Context(), toPersist); err != nil {
				// TODO: fail the whole effect
			}

			if ef.ActorName() != "" {
				goctx := context.WithoutCancel(ctx.Context())
				if err := s.pid.SendAsync(goctx, ef.ActorName(), durableState.ActorState()); err != nil {
					// TODO: handle the failure
				}
			}

		case *ReadOnlyEffect:
			// pass
		case *DeleteEffect:
			// persist the state
			toPersist := &persistence.DurableState{
				ActorID:        s.pid.ID(),
				State:          s.behavior.EmptyState(),
				Version:        s.currentState.Version() + 1,
				TimestampMilli: uint64(time.Now().UnixMilli()),
			}

			// persist the state onto the durable state store
			if err := s.stateStore.PersistDurableState(ctx.Context(), toPersist); err != nil {
				// TODO: fail the whole effect
			}

		case *ForwardEffect:
			// update the current state
			s.currentState = ef.DurableState()
			// then forward the actual state the recipient
			goctx := context.WithoutCancel(ctx.Context())
			if err := s.pid.SendAsync(goctx, ef.ActorName(), s.currentState.ActorState()); err != nil {
				// TODO: handle the failure
			}

		case *StopEffect:
			ctx.Shutdown()

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
