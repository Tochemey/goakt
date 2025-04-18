/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package testkit

import (
	"context"
	"errors"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/internal/collection/syncmap"
)

type StateReadWriter struct {
	db *syncmap.Map[string, proto.Message]
}

// enforce a compilation error
var _ actor.StateReadWriter = (*StateReadWriter)(nil)

// NewStateReadWriter creates an instance of StateReadWriter
func NewStateReadWriter() actor.StateReadWriter {
	return &StateReadWriter{
		db: syncmap.New[string, proto.Message](),
	}
}

// WriteState persists the latest actor state for the given persistence ID.
//
// The implementation may overwrite the existing snapshot or retain history
// based on the chosen retention strategy. The state must be stored in a
// durable and retrievable form.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control.
//   - persistenceID: Globally unique identifier for the actor.
//   - state: The actor's current state to persist.
func (s *StateReadWriter) WriteState(ctx context.Context, persistenceID string, state proto.Message) error {
	_, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	s.db.Set(persistenceID, state)
	return nil
}

// GetState retrieves the most recently persisted state for the given persistence ID.
//
// If no state is found, the returned proto.Message will be nil.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control.
//   - persistenceID: Unique identifier for the actor.
//
// Returns:
//   - state: The last persisted actor state, or nil if none exists.
//   - err: An error if retrieval fails.
func (s *StateReadWriter) GetState(ctx context.Context, persistenceID string) (proto.Message, error) {
	_, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if val, ok := s.db.Get(persistenceID); ok {
		return val, nil
	}
	return nil, errors.New("state not found")
}
