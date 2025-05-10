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

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/types"
)

func TestActorRef(t *testing.T) {
	t.Run("With Equals", func(t *testing.T) {
		addr := address.New("name", "system", "host", 1234)
		actorRef := fromActorRef(&internalpb.Actor{
			Address: addr.Address,
			Type:    "kind",
		})

		newActorRef := fromActorRef(&internalpb.Actor{
			Address: addr.Address,
			Type:    "kind",
		})

		require.Equal(t, "name", actorRef.Name())
		require.Equal(t, "kind", actorRef.Kind())
		require.True(t, addr.Equals(actorRef.Address()))
		require.True(t, newActorRef.Equals(actorRef))
	})
	t.Run("From PID", func(t *testing.T) {
		addr := address.New("name", "system", "host", 1234)
		actor := newMockActor()
		pid := &PID{
			address:      addr,
			actor:        actor,
			fieldsLocker: &sync.RWMutex{},
		}
		actorRef := fromPID(pid)
		require.Equal(t, "name", actorRef.Name())
		require.Equal(t, types.Name(actor), actorRef.Kind())
	})
}
