/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package supervisor

import (
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v3/errors"
)

type valueError struct{}

func (valueError) Error() string { return "value error" }

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
	directive, ok := supervisor.Directive(new(gerrors.AnyError))
	require.True(t, ok)
	require.Exactly(t, RestartDirective, directive)
}

func TestSupervisorWithDirective(t *testing.T) {
	supervisor := NewSupervisor(WithDirective(&gerrors.InternalError{}, RestartDirective))
	directive, ok := supervisor.Directive(&gerrors.InternalError{})
	require.True(t, ok)
	require.Exactly(t, RestartDirective, directive)
}

func TestStrategyString(t *testing.T) {
	require.Equal(t, "OneForOne", OneForOneStrategy.String())
	require.Equal(t, "OneForAll", OneForAllStrategy.String())
	require.Equal(t, "", Strategy(42).String())
}

func TestDirectiveString(t *testing.T) {
	require.Equal(t, "Stop", StopDirective.String())
	require.Equal(t, "Resume", ResumeDirective.String())
	require.Equal(t, "Restart", RestartDirective.String())
	require.Equal(t, "Escalate", EscalateDirective.String())
	require.Equal(t, "", Directive(42).String())
}

func TestNewSupervisorDefaults(t *testing.T) {
	supervisor := NewSupervisor()

	require.Equal(t, OneForOneStrategy, supervisor.Strategy())
	require.EqualValues(t, 0, supervisor.MaxRetries())
	require.Equal(t, time.Duration(-1), supervisor.Timeout())

	directive, ok := supervisor.Directive(&gerrors.PanicError{})
	require.True(t, ok)
	require.Equal(t, StopDirective, directive)

	directive, ok = supervisor.Directive(&runtime.PanicNilError{})
	require.True(t, ok)
	require.Equal(t, RestartDirective, directive)

	_, ok = supervisor.AnyErrorDirective()
	require.False(t, ok)
}

func TestNewSupervisorAnyErrorOverrides(t *testing.T) {
	supervisor := NewSupervisor(
		WithDirective(&gerrors.InternalError{}, RestartDirective),
		WithAnyErrorDirective(ResumeDirective),
	)

	directive, ok := supervisor.AnyErrorDirective()
	require.True(t, ok)
	require.Equal(t, ResumeDirective, directive)

	_, ok = supervisor.Directive(&gerrors.InternalError{})
	require.False(t, ok)

	_, ok = supervisor.Directive(&gerrors.PanicError{})
	require.False(t, ok)

	rules := supervisor.Rules()
	require.Len(t, rules, 1)
	require.Equal(t, errorType(new(gerrors.AnyError)), rules[0].ErrorType)
	require.Equal(t, ResumeDirective, rules[0].Directive)
}

func TestResetAndRules(t *testing.T) {
	supervisor := NewSupervisor(
		WithRetry(3, 2*time.Second),
		WithDirective(&gerrors.InternalError{}, ResumeDirective),
	)

	rules := supervisor.Rules()
	require.NotEmpty(t, rules)

	ruleMap := map[string]Directive{}
	for _, rule := range rules {
		ruleMap[rule.ErrorType] = rule.Directive
	}

	require.Equal(t, StopDirective, ruleMap[errorType(&gerrors.PanicError{})])
	require.Equal(t, RestartDirective, ruleMap[errorType(&runtime.PanicNilError{})])
	require.Equal(t, ResumeDirective, ruleMap[errorType(&gerrors.InternalError{})])

	supervisor.Reset()

	require.Equal(t, OneForAllStrategy, supervisor.Strategy())
	require.EqualValues(t, 3, supervisor.MaxRetries())
	require.Equal(t, 2*time.Second, supervisor.Timeout())
	require.Nil(t, supervisor.Rules())

	_, ok := supervisor.Directive(&gerrors.PanicError{})
	require.False(t, ok)
}

func TestRulesWithNilDirectives(t *testing.T) {
	supervisor := &Supervisor{}
	require.Nil(t, supervisor.Rules())
}

func TestDirectiveWithNilDirectives(t *testing.T) {
	supervisor := &Supervisor{}
	directive, ok := supervisor.Directive(errors.New("nil-map"))
	require.False(t, ok)
	require.Equal(t, Directive(0), directive)
}

func TestAnyErrorDirectiveWithNilDirectives(t *testing.T) {
	supervisor := &Supervisor{}
	directive, ok := supervisor.AnyErrorDirective()
	require.False(t, ok)
	require.Equal(t, Directive(0), directive)
}

func TestSetDirectiveByType(t *testing.T) {
	supervisor := &Supervisor{}
	supervisor.SetDirectiveByType("", ResumeDirective)
	require.Nil(t, supervisor.directives)

	err := valueError{}
	supervisor.SetDirectiveByType(errorType(err), ResumeDirective)
	directive, ok := supervisor.Directive(err)
	require.True(t, ok)
	require.Equal(t, ResumeDirective, directive)
}

func TestErrorType(t *testing.T) {
	require.Equal(t, "nil", errorType(nil))

	valueType := errorType(valueError{})
	require.Equal(t, valueType, errorType(&valueError{}))
}
