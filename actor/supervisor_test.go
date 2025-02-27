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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSupervisorOption(t *testing.T) {
	testCases := []struct {
		name     string
		option   SupervisorOption
		expected *Supervisor
	}{
		{
			name:     "WithStrategy",
			option:   WithStrategy(OneForAllStrategy),
			expected: &Supervisor{strategy: OneForAllStrategy},
		},
		{
			name:     "WithRetry",
			option:   WithRetry(2, time.Second),
			expected: &Supervisor{timeout: time.Second, maxRetries: 2},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			supervisor := &Supervisor{}
			tc.option(supervisor)
			assert.Equal(t, tc.expected, supervisor)
		})
	}
}

func TestSupervisorWithAnyError(t *testing.T) {
	supervisor := NewSupervisor(WithAnyErrorDirective(RestartDirective))
	directive, ok := supervisor.Directive(new(anyError))
	require.True(t, ok)
	require.Exactly(t, RestartDirective, directive)
}

func TestSupervisorWithDirective(t *testing.T) {
	supervisor := NewSupervisor(WithDirective(&InternalError{}, RestartDirective))
	directive, ok := supervisor.Directive(&InternalError{})
	require.True(t, ok)
	require.Exactly(t, RestartDirective, directive)
}
