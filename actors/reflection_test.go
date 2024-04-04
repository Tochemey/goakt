/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tochemey/goakt/goaktpb"
)

func TestReflection(t *testing.T) {
	t.Run("With ActorFrom", func(t *testing.T) {
		t.Run("With happy path", func(t *testing.T) {
			newRegistry := newRegistry()
			// create an instance of an actor
			actor := newTestActor()
			// register the actor into the types registry
			newRegistry.Register(actor)

			// create an instance of reflection
			reflection := newReflection(newRegistry)

			// create an instance of test actor from the string testActor
			actual, err := reflection.ActorFrom("testActor")
			assert.NoError(t, err)
			assert.NotNil(t, actual)
			assert.IsType(t, new(testActor), actual)
		})
		t.Run("With actor not found", func(t *testing.T) {
			newRegistry := newRegistry()
			// create an instance of reflection
			reflection := newReflection(newRegistry)
			actual, err := reflection.ActorFrom("fakeActor")
			assert.Error(t, err)
			assert.Nil(t, actual)
		})
		t.Run("With actor interface not implemented", func(t *testing.T) {
			tl := newRegistry()
			nonActor := new(goaktpb.Address)
			// register the actor into the types registry
			tl.Register(nonActor)
			// create an instance of reflection
			reflection := newReflection(tl)
			actual, err := reflection.ActorFrom("fakeActor")
			assert.Error(t, err)
			assert.Nil(t, actual)
		})
	})

	t.Run("With ActorOf", func(t *testing.T) {
		t.Run("With happy path", func(t *testing.T) {
			newRegistry := newRegistry()
			// create an instance of an actor
			actor := newTestActor()
			// register the actor into the types registry
			newRegistry.Register(actor)

			// create an instance of reflection
			reflection := newReflection(newRegistry)

			rtype, ok := newRegistry.GetType(actor)
			assert.True(t, ok)
			assert.NotNil(t, rtype)

			// create an instance of test actor from the string testActor
			actual, err := reflection.ActorOf(rtype)

			// perform some assertions
			assert.NoError(t, err)
			assert.NotNil(t, actual)
			_, ok = actual.(*testActor)
			assert.True(t, ok)
			assert.IsType(t, new(testActor), actual)
		})
		t.Run("With unhappy path", func(t *testing.T) {
			newRegistry := newRegistry()
			// create an instance of reflection
			reflection := newReflection(newRegistry)
			nonActor := new(goaktpb.Address)
			// register the actor into the types registry
			newRegistry.Register(nonActor)

			rtype, ok := newRegistry.GetType(nonActor)
			assert.True(t, ok)
			assert.NotNil(t, rtype)
			actual, err := reflection.ActorOf(rtype)
			assert.Error(t, err)
			assert.Nil(t, actual)
		})
	})
}
