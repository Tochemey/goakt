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
 *
 */

package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegistry(t *testing.T) {
	t.Run("With new instance", func(t *testing.T) {
		newRegistry := NewRegistry()
		var p any = newRegistry
		_, ok := p.(Registry)
		assert.True(t, ok)
	})

	t.Run("With registration with name", func(t *testing.T) {
		newRegistry := NewRegistry()
		// create an instance of an object
		actor := newTestActor()
		// register that actor
		newRegistry.Register("testActor", actor)
		// assert the type of testActor
		_, ok := newRegistry.GetNamedType("testActor")
		assert.True(t, ok)
		_, ok = newRegistry.GetType(actor)
		assert.True(t, ok)
	})

	t.Run("With registration without name", func(t *testing.T) {
		newRegistry := NewRegistry()
		// create an instance of an object
		actor := newTestActor()
		// register that actor
		newRegistry.Register("", actor)
		// assert the type of testActor
		_, ok := newRegistry.GetNamedType("testActor")
		assert.False(t, ok)
		_, ok = newRegistry.GetType(actor)
		assert.True(t, ok)
	})
}
