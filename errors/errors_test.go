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

package errors

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/passivation"
)

func TestErrors(t *testing.T) {
	err := errors.New("something went wrong")
	internalErr := NewInternalError(err)
	require.Error(t, internalErr)
	require.EqualError(t, internalErr, "internal error: something went wrong")
	assert.ErrorIs(t, internalErr.Unwrap(), err)

	err = errors.New("something went wrong")
	spawnErr := NewSpawnError(err)
	require.Error(t, spawnErr)
	require.EqualError(t, spawnErr, "spawn error: something went wrong")
	assert.ErrorIs(t, spawnErr.Unwrap(), err)

	err = errors.New("something went wrong")
	rebalancingErr := NewRebalancingError(err)
	require.Error(t, rebalancingErr)
	require.EqualError(t, rebalancingErr, "rebalancing: something went wrong")
	assert.ErrorIs(t, rebalancingErr.Unwrap(), err)

	anyError := &AnyError{}
	require.Equal(t, anyError.Error(), "*")
}

func TestPanicError(t *testing.T) {
	err := errors.New("something went wrong")
	panicErr := NewPanicError(err)
	require.Error(t, panicErr)
	require.EqualError(t, panicErr, "panic: something went wrong")
	assert.ErrorIs(t, panicErr.Unwrap(), err)
}

func TestNewErrInvalidTCPAddress(t *testing.T) {
	err := NewErrInvalidTCPAddress("127.0.0.1:0")
	require.Error(t, err)
	require.EqualError(t, err, "address=(127.0.0.1:0) invalid TCP address")
	assert.ErrorIs(t, err, ErrInvalidTCPAddress)
}

func TestNewErrInvalidPassivationStrategy(t *testing.T) {
	err := NewErrInvalidPassivationStrategy(passivation.NewLongLivedStrategy())
	require.Error(t, err)
	require.EqualError(t, err, "passivation strategy=(Long Lived) invalid passivation strategy, must be one of: 'time-based', 'messages-count-based', or 'long-lived'")
	assert.ErrorIs(t, err, ErrInvalidPassivationStrategy)
}

func TestNewErrUnhandledMessage(t *testing.T) {
	base := errors.New("boom")
	err := NewErrUnhandledMessage(base)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnhanledMessage)
	assert.ErrorIs(t, err, base)
}

func TestNewErrGrainActivationFailure(t *testing.T) {
	base := errors.New("boom")
	err := NewErrGrainActivationFailure(base)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGrainActivationFailure)
	assert.ErrorIs(t, err, base)
}

func TestNewErrGrainDeactivationFailure(t *testing.T) {
	base := errors.New("boom")
	err := NewErrGrainDeactivationFailure(base)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGrainDeactivationFailure)
	assert.ErrorIs(t, err, base)
}

func TestNewErrInvalidGrainIdentity(t *testing.T) {
	base := errors.New("boom")
	err := NewErrInvalidGrainIdentity(base)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidGrainIdentity)
	assert.ErrorIs(t, err, base)
}

func TestNewErrReservedName(t *testing.T) {
	err := NewErrReservedName("system")
	require.Error(t, err)
	require.EqualError(t, err, "name=(system) actor name is reserved")
	assert.ErrorIs(t, err, ErrReservedName)
}

func TestNewErrActorNotFound(t *testing.T) {
	err := NewErrActorNotFound("/user/actor")
	require.Error(t, err)
	require.EqualError(t, err, "(actor=/user/actor) actor not found")
	assert.ErrorIs(t, err, ErrActorNotFound)
}

func TestNewErrAddressNotFound(t *testing.T) {
	err := NewErrAddressNotFound("127.0.0.1:4000")
	require.Error(t, err)
	require.EqualError(t, err, "(actor address=127.0.0.1:4000) address not found")
	assert.ErrorIs(t, err, ErrAddressNotFound)
}

func TestNewErrRemoteSendFailure(t *testing.T) {
	base := errors.New("boom")
	err := NewErrRemoteSendFailure(base)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRemoteSendFailure)
	assert.ErrorIs(t, err, base)
}

func TestNewErrActorAlreadyExists(t *testing.T) {
	err := NewErrActorAlreadyExists("actorName")
	require.Error(t, err)
	require.EqualError(t, err, "actor=(actorName) actor already exists")
	assert.ErrorIs(t, err, ErrActorAlreadyExists)
}

func TestNewErrInvalidMessage(t *testing.T) {
	base := errors.New("boom")
	err := NewErrInvalidMessage(base)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidMessage)
	assert.ErrorIs(t, err, base)
}

func TestNewErrInvalidRemoteMessage(t *testing.T) {
	base := errors.New("boom")
	err := NewErrInvalidRemoteMessage(base)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidRemoteMessage)
	assert.ErrorIs(t, err, base)
}

func TestNewErrInitFailure(t *testing.T) {
	base := errors.New("boom")
	err := NewErrInitFailure(base)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInitFailure)
	assert.ErrorIs(t, err, base)
}
