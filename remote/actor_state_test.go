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

package remote

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActorState_String(t *testing.T) {
	tests := []struct {
		state    ActorState
		expected string
	}{
		{ActorStateUnknown, "unknown"},
		{ActorStateRunning, "running"},
		{ActorStateSuspended, "suspended"},
		{ActorStateStopping, "stopping"},
		{ActorStateRelocatable, "relocatable"},
		{ActorStateSingleton, "singleton"},
		{ActorState(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}

func TestActorState_ValuesMatchProto(t *testing.T) {
	// Ensure values align with internalpb.State enum (0-5)
	assert.Equal(t, uint32(0), uint32(ActorStateUnknown))
	assert.Equal(t, uint32(1), uint32(ActorStateRunning))
	assert.Equal(t, uint32(2), uint32(ActorStateSuspended))
	assert.Equal(t, uint32(3), uint32(ActorStateStopping))
	assert.Equal(t, uint32(4), uint32(ActorStateRelocatable))
	assert.Equal(t, uint32(5), uint32(ActorStateSingleton))
}
