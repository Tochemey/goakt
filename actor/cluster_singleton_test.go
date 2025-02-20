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

package actors

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/internal/util"
)

func TestSingletonActor(t *testing.T) {
	t.Run("With Singleton Actor", func(t *testing.T) {
		// create a context
		ctx := context.TODO()
		// start the NATS server
		srv := startNatsServer(t)

		cl1, sd1 := startClusterSystem(t, srv.Addr().String())
		require.NotNil(t, cl1)
		require.NotNil(t, sd1)

		cl2, sd2 := startClusterSystem(t, srv.Addr().String())
		require.NotNil(t, cl2)
		require.NotNil(t, sd2)

		cl3, sd3 := startClusterSystem(t, srv.Addr().String())
		require.NotNil(t, cl3)
		require.NotNil(t, sd3)

		util.Pause(time.Second)

		// create a singleton actor
		actor := newMockActor()
		actorName := "actorID"
		// create a singleton actor
		err := cl1.SpawnSingleton(ctx, actorName, actor)
		require.NoError(t, err)

		// attempt to create another singleton actor with the same actor
		err = cl2.SpawnSingleton(ctx, "actorName", actor)
		require.Error(t, err)
		require.Contains(t, err.Error(), ErrSingletonAlreadyExists.Error())

		// free resources
		require.NoError(t, cl3.Stop(ctx))
		require.NoError(t, sd3.Close())
		require.NoError(t, cl1.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, cl2.Stop(ctx))
		require.NoError(t, sd2.Close())
		// shutdown the nats server gracefully
		srv.Shutdown()
	})
}
