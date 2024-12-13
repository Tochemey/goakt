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

package persistence

import (
	"context"

	"google.golang.org/protobuf/proto"
)

// DurableState represents the persistence durable state
type DurableState struct {
	// ActorID specifies the actor id
	ActorID string
	// State specifies the actor state
	State proto.Message
	// Version specifies the state version number
	Version uint64
	// TimestampMilli specifies the timestamp in millisecond
	TimestampMilli uint64
}

// DurableStateStore defines the API to interact with the durable state store
type DurableStateStore interface {
	// Ping verifies a connection to durable state store, establishing a connection if necessary.
	Ping(ctx context.Context) error
	// PersistDurableState persists the durable state onto the durable state store.
	// Only the latest state is persisted on to the data store. The implementor of this interface
	// need to make sure that the actorID is properly indexed depending upon the chosen storage for fast query.
	// The implementation of this method should be idempotent.
	PersistDurableState(ctx context.Context, durableState *DurableState) error
	// GetDurableState returns the persisted durable state for a given actor provided the actor id
	GetDurableState(ctx context.Context, actorID string) (*DurableState, error)
}
