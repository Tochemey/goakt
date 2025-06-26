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

package actor

// GrainProps defines the properties of a grain in the goakt actor system.
type GrainProps struct {
	identity    *GrainIdentity
	actorSystem ActorSystem
}

// newGrainProps creates a new instance of GrainProps with the specified identity and actor system.
func newGrainProps(identity *GrainIdentity, actorSystem ActorSystem) *GrainProps {
	return &GrainProps{
		identity:    identity,
		actorSystem: actorSystem,
	}
}

// Identity returns the unique identity of the grain.
// This identity is used to uniquely identify the grain within the actor system.
// It is typically a string that represents the grain's type and instance.
func (p *GrainProps) Identity() *GrainIdentity {
	return p.identity
}

// ActorSystem returns the actor system associated with the grain properties.
func (p *GrainProps) ActorSystem() ActorSystem {
	return p.actorSystem
}
