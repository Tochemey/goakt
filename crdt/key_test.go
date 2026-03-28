// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package crdt

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestKey(t *testing.T) {
	t.Run("GCounterKey", func(t *testing.T) {
		k := GCounterKey("counter-1")
		assert.Equal(t, "counter-1", k.ID())
		assert.Equal(t, GCounterType, k.Type())
	})

	t.Run("PNCounterKey", func(t *testing.T) {
		k := PNCounterKey("requests")
		assert.Equal(t, "requests", k.ID())
		assert.Equal(t, PNCounterType, k.Type())
	})

	t.Run("LWWRegisterKey", func(t *testing.T) {
		k := LWWRegisterKey[string]("config-value")
		assert.Equal(t, "config-value", k.ID())
		assert.Equal(t, LWWRegisterType, k.Type())
	})

	t.Run("ORSetKey", func(t *testing.T) {
		k := ORSetKey[string]("sessions")
		assert.Equal(t, "sessions", k.ID())
		assert.Equal(t, ORSetType, k.Type())
	})

	t.Run("ORSetKey with int type", func(t *testing.T) {
		k := ORSetKey[int]("user-ids")
		assert.Equal(t, "user-ids", k.ID())
		assert.Equal(t, ORSetType, k.Type())
	})

	t.Run("ORMapKey", func(t *testing.T) {
		k := ORMapKey[string, *GCounter]("cart")
		assert.Equal(t, "cart", k.ID())
		assert.Equal(t, ORMapType, k.Type())
	})

	t.Run("FlagKey", func(t *testing.T) {
		k := FlagKey("feature-enabled")
		assert.Equal(t, "feature-enabled", k.ID())
		assert.Equal(t, FlagType, k.Type())
	})

	t.Run("MVRegisterKey", func(t *testing.T) {
		k := MVRegisterKey[string]("profile-name")
		assert.Equal(t, "profile-name", k.ID())
		assert.Equal(t, MVRegisterType, k.Type())
	})
}
