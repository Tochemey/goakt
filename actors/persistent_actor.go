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
	"errors"
	"fmt"
	"time"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/errorschain"
	"github.com/tochemey/goakt/v2/log"
	"github.com/tochemey/goakt/v2/persistence"
)

// State defines the durable state
// sent when handling a given command.

type persistentActor struct {
	handler         PersistentActor
	persistentStore persistence.Store
	currentState    *PersistentState
	pid             *PID
	logger          log.Logger
	id              string
}

// enforce compilation error
var _ Actor = (*persistentActor)(nil)

// newPersistentActor creates an instance of durable stateful actor
func newPersistentActor(id string, persistentStore persistence.Store, handler PersistentActor) *persistentActor {
	return &persistentActor{
		handler:         handler,
		persistentStore: persistentStore,
		id:              id,
	}
}

// PreStart pre-starts the actor. On boot, the actor
// will try to recover from the durable state store
func (s *persistentActor) PreStart(ctx context.Context) error {
	return errorschain.
		New(errorschain.ReturnFirst()).
		AddError(s.persistentStore.Ping(ctx)).
		AddError(s.handler.PreStart(ctx)).
		AddError(s.restoreState(ctx)).
		Error()
}

// Receive processes any message dropped into the actor mailbox.
func (s *persistentActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		s.pid = ctx.Self()
		s.logger = ctx.Logger()
	default:
		command := ctx.Message()

		// prepare the context
		persistentContext := &PersistentContext{
			ctx:          ctx.Context(),
			command:      command,
			currentState: s.currentState,
			sender:       ctx.Sender(),
			self:         s.pid,
		}

		if ctx.RemoteSender() != nil {
			persistentContext.remoteSender = ctx.RemoteSender()
		}

		// pass it to the Receive handler
		response := s.handler.Receive(persistentContext)
		// send a suspension error back to the system
		if response == nil {
			ctx.Err(errSuspended(errors.New("failed to process command")))
			return
		}

		switch {
		case response.Error() != nil:
			s.logger.Errorf("%s failed to process command: %s: %v",
				s.pid.Name(),
				command.ProtoReflect().Descriptor().FullName(),
				response.Error())
			ctx.Err(response.Error())

		case response.State() != nil:
			chain := errorschain.
				New(errorschain.ReturnFirst()).
				AddError(s.checkAndSetPreconditions(response)).
				AddError(s.persistState(ctx.Context()))

			if err := chain.Error(); err != nil {
				s.logger.Errorf("%s failed handle PersistentResponse: %v", s.pid.ID(), err)
				ctx.Err(fmt.Errorf("%s failed handle PersistentResponse: %v", s.pid.ID(), err))
				return
			}

			// in case we have some forwardTo
			if response.ForwardTo() != nil {
				forwardTo := response.ForwardTo()
				if err := s.forwardTo(ctx.Context(), *forwardTo); err != nil {
					s.logger.Errorf("%s failed forward state to:%s : %v", s.pid.ID(), *forwardTo, err)
					ctx.Err(fmt.Errorf("%s failed forward state to:%s : %v", s.pid.ID(), *forwardTo, err))
				}
			}

		case response.ForwardTo() != nil:
			forwardTo := response.ForwardTo()
			if err := s.forwardTo(ctx.Context(), *forwardTo); err != nil {
				s.logger.Errorf("%s failed forward state to:%s : %v", s.pid.ID(), *forwardTo, err)
				ctx.Err(fmt.Errorf("%s failed forward state to:%s : %v", s.pid.ID(), *forwardTo, err))
			}

		default:
			// no-op
		}
	}
}

// PostStop prepares the actor to gracefully shutdown
func (s *persistentActor) PostStop(ctx context.Context) error {
	return errorschain.
		New(errorschain.ReturnFirst()).
		AddError(s.persistState(ctx)).
		AddError(s.handler.PostStop(ctx)).
		Error()
}

// restoreState restores the actor latest state
// this is vital when the actor is restarting.
func (s *persistentActor) restoreState(ctx context.Context) error {
	latestState, err := s.persistentStore.GetState(ctx, s.id)
	if err != nil {
		return fmt.Errorf("failed to recover the latest state from durable state store: %w", err)
	}

	// we do have the latest state just recover from it
	if latestState != nil {
		currentState := s.currentState.ActorState()
		currentStateType := currentState.ProtoReflect().Descriptor().FullName()
		latestStateType := latestState.ActorState.ProtoReflect().Descriptor().FullName()
		if currentStateType != latestStateType {
			return fmt.Errorf("mismatch state types: %s != %s", currentStateType, latestStateType)
		}

		s.currentState = NewPersistentState(currentState, latestState.Version)
		return nil
	}

	s.currentState = NewPersistentState(s.handler.EmptyState(), 0)
	return s.persistState(ctx)
}

// forwardTo forward the actor state to the provided actor name
func (s *persistentActor) forwardTo(ctx context.Context, actorName string) error {
	if actorName != "" {
		return s.pid.SendAsync(ctx, actorName, s.currentState.ActorState())
	}
	return nil
}

// persistState persist the actor durable state
func (s *persistentActor) persistState(ctx context.Context) error {
	return s.persistentStore.PersistState(ctx, &persistence.State{
		ActorID:        s.id,
		ActorState:     s.currentState.ActorState(),
		Version:        s.currentState.Version(),
		TimestampMilli: uint64(time.Now().UnixMilli()),
	})
}

// checkAndSetPreconditions validates the StateCommandResponse and set the current state
// of the state actor. This state is in-memory and yet to be persisted
func (s *persistentActor) checkAndSetPreconditions(commandResponse *PersistentResponse) error {
	durableState := commandResponse.State()
	// incoming version must be current version + 1
	isValid := (durableState.Version() - s.currentState.Version()) == 1
	if !isValid {
		return fmt.Errorf("%s received version=(%d) while current version is (%d)",
			s.pid.Name(),
			durableState.Version(),
			s.currentState.Version())
	}
	// set the current state
	s.currentState = commandResponse.State()
	return nil
}
