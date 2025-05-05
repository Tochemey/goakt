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
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/log"
)

func TestContext(t *testing.T) {
	ctx := context.Background()

	actorSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NotNil(t, actorSystem)

	c := newContext(ctx, "name", actorSystem)
	require.NotNil(t, c.ActorSystem())
	require.Equal(t, "testSys", c.ActorSystem().Name())
	require.Empty(t, c.ActorSystem().Actors())
	require.NotNil(t, c.ActorSystem().Logger())
	require.Empty(t, c.Extensions())
	require.Nil(t, c.Extension("extensionID"))
	require.Empty(t, c.Dependencies())

	dl, ok := c.Context().Deadline()
	require.False(t, ok)
	require.Zero(t, dl)
	require.Equal(t, "name", c.ActorName())

	done := c.Context().Done()
	require.Nil(t, done)
	require.NoError(t, c.Context().Err())
}
