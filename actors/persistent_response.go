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

// PersistentResponse defines the expected response when
// handling command
type PersistentResponse struct {
	state     *PersistentState
	forwardTo *string
	err       error
}

// NewPersistentResponse creates an instance of PersistentResponse
func NewPersistentResponse() *PersistentResponse {
	return new(PersistentResponse)
}

// WithState sets the new actor state with a new version number.
func (r *PersistentResponse) WithState(state *PersistentState) *PersistentResponse {
	r.state = state
	return r
}

// WithForwardTo sets the actor that will receive the current state of the
// stateful actor as a message.
func (r *PersistentResponse) WithForwardTo(actorName string) *PersistentResponse {
	r.forwardTo = &actorName
	return r
}

// WithError sets the error when handling the given command.
// One need to defines beforehand the appropriate supervisor directive
// required to handle such an error. Failure to do so will put the stateful actor
// in a suspended state. Refer to the spawn option to configure such directives.
func (r *PersistentResponse) WithError(err error) *PersistentResponse {
	r.err = err
	return r
}

// State returns the actor state with a newer version.
// When the new state version is valid then it will be persisted into the state store.
// On the contrary the stateful actor will be in suspended state.
func (r *PersistentResponse) State() *PersistentState {
	return r.state
}

// ForwardTo returns the potential actor to receive as message
// the actor state. The sender of the message will be the stateful actor.
// If this value is nil no message is sent.
func (r *PersistentResponse) ForwardTo() *string {
	return r.forwardTo
}

// Error returns the handler error message.
// This will put the stateful actor is the suspended state
func (r *PersistentResponse) Error() error {
	return r.err
}
