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

package extension

import (
	"github.com/tochemey/goakt/v3/persistence"
)

const (
	// PersistenceExtensionID is the name of the persistence store extension.
	PersistenceExtensionID = "persistenceExtension"
)

type PersistenceExtension struct {
	stateStore persistence.StateStore
}

// enforce interface compliance
var _ Extension = (*PersistenceExtension)(nil)

// NewPersistence creates a new instance of PersistenceExtension, which provides persistence capabilities to the actor system.
//
// This extension wraps a persistence.StateStore, enabling actors to persist and retrieve their state using a unified interface.
// By registering this extension with the actor system, you enable virtual actors and other stateful actors to maintain their state
// across activations, deactivations, and failures.
//
// The provided snapshotStore must implement the persistence.StateStore interface, supporting thread-safe, concurrent access
// from multiple actors. You can use any backend that satisfies this interface, such as in-memory stores, relational databases,
// NoSQL databases, or distributed caches.
//
// # Usage
//
//	Register the extension with the actor system using WithExtension:
//
//	    system := actor.NewActorSystem(
//	        actor.WithExtension(NewPersistence(mySnapshotStore)),
//	    )
//
//	Actors can then access the persistence store through their context to load and save snapshots of their state.
//
// # Parameters
//
//   - snapshotStore: The stateStore persistence.StateStore implementation to be used for actor state persistence.
//
// # Returns
//
//   - *PersistenceExtension: An instance of the persistence extension, ready to be registered with the actor system.
//
// The extension is uniquely identified by the PersistenceExtensionID constant, ensuring it can be referenced within the system's extension registry.
func NewPersistence(stateStore persistence.StateStore) *PersistenceExtension {
	return &PersistenceExtension{
		stateStore: stateStore,
	}
}

// ID implements extension.Extension.
func (v *PersistenceExtension) ID() string {
	return PersistenceExtensionID
}

// StateStore returns the extension state store
func (v *PersistenceExtension) StateStore() persistence.StateStore {
	return v.stateStore
}
