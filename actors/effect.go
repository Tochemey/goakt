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

// Effect directive defines what state, if any, to persist
type Effect interface {
	isEffect()
}

// PersistEffect directive will persist the latest state.
// If it’s a new persistence id, the record will be inserted.
// In case of an existing persistence id, the record will be updated.
// Otherwise, persist will fail.
type PersistEffect struct {
	state     *DurableState
	actorName string
}

// NewPersistEffect creates a  persist effect.
// If it’s a new persistence id, the record will be inserted.
// In case of an existing persistence id, the record will be updated.
// Otherwise, persist will fail.
func NewPersistEffect(resultingState *DurableState) *PersistEffect {
	return &PersistEffect{
		state: resultingState,
	}
}

// ThenForward sets the recipient of the resulting state.
// The command handler needs to call this method to set a potential recipient of the new actor state.
// Only the actual state will be sent to the recipient without the latest version number
func (effect *PersistEffect) ThenForward(actorName string) *PersistEffect {
	effect.actorName = actorName
	return effect
}

// DurableState returns the durable state to persist
func (effect *PersistEffect) DurableState() *DurableState {
	return effect.state
}

// ActorName returns the recipient that will receive the resultingState after it has been persisted
// One need to set the pid using the ThenForward during the instantiation of the
// PersistEffect
func (effect *PersistEffect) ActorName() string {
	return effect.actorName
}

// implements Effect
func (effect *PersistEffect) isEffect() {}

// DeleteEffect directive will delete the state by setting it to the empty state
// and the revision number will be incremented by 1.
type DeleteEffect struct{}

// NewDeleteEffect creates an instance of delete effect
func NewDeleteEffect() Effect {
	return &DeleteEffect{}
}

// implements Effect
func (effect *DeleteEffect) isEffect() {}

// StopEffect directive stop the stateful statefulEngine
type StopEffect struct{}

// implements Effect
func (s *StopEffect) isEffect() {
}

// NewStopEffect creates an instance of the stop effect.
// Stop Effect will stop the statefull statefulEngine.
func NewStopEffect() Effect {
	return new(StopEffect)
}

// ReadOnlyEffect will not persist any state
type ReadOnlyEffect struct {
}

// NewReadOnlyEffect creates and returns a new Effect instance
// that enforces a read-only behavior, ensuring no state is persisted.
func NewReadOnlyEffect() Effect {
	return &ReadOnlyEffect{}
}

// implements Effect
func (effect *ReadOnlyEffect) isEffect() {}

type ForwardEffect struct {
	state     *DurableState
	actorName string
}

// implements Effect
func (r *ForwardEffect) isEffect() {}

// NewForwardEffect creates a new forwardEffect with the provided state and recipient.
// This effect will send the resulting state the recipient without persisting it.
func NewForwardEffect(state *DurableState, actorName string) Effect {
	return &ForwardEffect{
		state:     state,
		actorName: actorName,
	}
}

// ActorName returns the recipient of the resulting state
func (effect *ForwardEffect) ActorName() string {
	return effect.actorName
}

// DurableState returns the durable state to persist
func (effect *ForwardEffect) DurableState() *DurableState {
	return effect.state
}
