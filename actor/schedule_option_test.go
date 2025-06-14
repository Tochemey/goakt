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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/internal/util"
	"github.com/tochemey/goakt/v3/log"
)

func TestScheduleOption(t *testing.T) {
	// create the context
	ctx := context.TODO()
	// define the logger to use
	logger := log.DiscardLogger
	// create the actor system
	newActorSystem, err := NewActorSystem(
		"test",
		WithLogger(logger),
	)
	// assert there are no error
	require.NoError(t, err)

	// start the actor system
	err = newActorSystem.Start(ctx)
	assert.NoError(t, err)

	util.Pause(time.Second)

	// create a test actor
	actorName := "test"
	actor := newMockActor()
	actorRef, err := newActorSystem.Spawn(ctx, actorName, actor)
	require.NoError(t, err)
	assert.NotNil(t, actorRef)

	util.Pause(time.Second)

	referenceID := "reference"
	opts := []ScheduleOption{
		WithSender(actorRef),
		WithSenderAddress(actorRef.Address()),
		WithReference(referenceID),
	}

	config := newScheduleConfig(opts...)
	require.Equal(t, referenceID, config.Reference())
	require.True(t, actorRef.Address().Equals(config.SenderAddr()))
	require.True(t, actorRef.Equals(config.Sender()))

	// stop the actor
	err = newActorSystem.Stop(ctx)
	assert.NoError(t, err)
}
