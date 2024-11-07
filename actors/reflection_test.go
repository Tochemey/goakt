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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/internal/types"
)

func TestReflection(t *testing.T) {
	t.Run("With ActorFrom happy path", func(t *testing.T) {
		newRegistry := types.NewRegistry()
		actor := newTestActor()
		newRegistry.Register(actor)
		reflection := newReflection(newRegistry)
		actual, err := reflection.ActorFrom("actors.testActor")
		assert.NoError(t, err)
		assert.NotNil(t, actual)
		assert.IsType(t, new(testActor), actual)
	})
	t.Run("With ActorFrom actor not found", func(t *testing.T) {
		newRegistry := types.NewRegistry()
		reflection := newReflection(newRegistry)
		actual, err := reflection.ActorFrom("actors.fakeActor")
		assert.Error(t, err)
		assert.Nil(t, actual)
	})
	t.Run("With unregistered actor", func(t *testing.T) {
		tl := types.NewRegistry()
		nonActor := new(goaktpb.Address)
		tl.Register(nonActor)
		reflection := newReflection(tl)
		actual, err := reflection.ActorFrom("actors.fakeActor")
		assert.Error(t, err)
		assert.EqualError(t, err, ErrTypeNotRegistered.Error())
		assert.Nil(t, actual)
	})
	t.Run("With ActorFrom actor interface not implemented", func(t *testing.T) {
		newRegistry := types.NewRegistry()
		type normalStruct struct{}
		newRegistry.Register(new(normalStruct))
		reflection := newReflection(newRegistry)
		actual, err := reflection.ActorFrom("actors.normalStruct")
		assert.Error(t, err)
		assert.EqualError(t, err, ErrInstanceNotAnActor.Error())
		assert.Nil(t, actual)
	})
}
