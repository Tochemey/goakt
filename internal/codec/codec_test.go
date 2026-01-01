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

package codec

import (
	"errors"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/durationpb"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/internal/ds"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/registry"
	mocks "github.com/tochemey/goakt/v3/mocks/extension"
	"github.com/tochemey/goakt/v3/passivation"
	"github.com/tochemey/goakt/v3/supervisor"
)

type decodeDirectiveError struct{}

func (decodeDirectiveError) Error() string { return "escalate" }

type unknownStrategy struct{}

func (unknownStrategy) String() string { return "unknown" }

func (unknownStrategy) Name() string { return "unknown" }

func TestEncodeDecodePassivationStrategy(t *testing.T) {
	t.Run("TimeBasedStrategy", func(t *testing.T) {
		duration := time.Second
		strategy := passivation.NewTimeBasedStrategy(duration)
		expected := &internalpb.PassivationStrategy{
			Strategy: &internalpb.PassivationStrategy_TimeBased{
				TimeBased: &internalpb.TimeBasedPassivation{
					PassivateAfter: durationpb.New(duration),
				},
			},
		}
		result := EncodePassivationStrategy(strategy)
		require.Equal(t, prototext.Format(expected), prototext.Format(result))

		unmarshalled := DecodePassivationStrategy(result)
		require.NotNil(t, unmarshalled)
		require.Equal(t, strategy.String(), unmarshalled.String())
	})
	t.Run("MessagesCountBasedStrategy", func(t *testing.T) {
		maxMessages := 10
		strategy := passivation.NewMessageCountBasedStrategy(maxMessages)
		expected := &internalpb.PassivationStrategy{
			Strategy: &internalpb.PassivationStrategy_MessagesCountBased{
				MessagesCountBased: &internalpb.MessagesCountBasedPassivation{
					MaxMessages: int64(maxMessages),
				},
			},
		}
		result := EncodePassivationStrategy(strategy)
		require.Equal(t, prototext.Format(expected), prototext.Format(result))
		unmarshalled := DecodePassivationStrategy(result)
		require.NotNil(t, unmarshalled)
		require.Equal(t, strategy.String(), unmarshalled.String())
	})
	t.Run("LongLivedStrategy", func(t *testing.T) {
		strategy := passivation.NewLongLivedStrategy()
		expected := &internalpb.PassivationStrategy{
			Strategy: &internalpb.PassivationStrategy_LongLived{
				LongLived: new(internalpb.LongLivedPassivation),
			},
		}
		result := EncodePassivationStrategy(strategy)
		require.Equal(t, prototext.Format(expected), prototext.Format(result))
		unmarshalled := DecodePassivationStrategy(result)
		require.NotNil(t, unmarshalled)
		require.Equal(t, strategy.String(), unmarshalled.String())
	})
}

func TestEncodePassivationStrategyUnknown(t *testing.T) {
	require.Nil(t, EncodePassivationStrategy(unknownStrategy{}))
}

func TestDecodePassivationStrategyNil(t *testing.T) {
	require.Nil(t, DecodePassivationStrategy(nil))
}

func TestDecodePassivationStrategyUnknown(t *testing.T) {
	require.Nil(t, DecodePassivationStrategy(&internalpb.PassivationStrategy{}))
}

func TestEncodeDependencies(t *testing.T) {
	t.Run("EncodeDependencies Happy Path", func(t *testing.T) {
		mockDependency := mocks.NewDependency(t)
		mockDependency.On("ID").Return("dep1")
		mockDependency.On("MarshalBinary").Return([]byte("mock data"), nil)

		dependencies := []extension.Dependency{mockDependency}
		expected := []*internalpb.Dependency{
			{Id: "dep1", TypeName: registry.Name(mockDependency), Bytea: []byte("mock data")},
		}
		result, err := EncodeDependencies(dependencies...)
		require.NoError(t, err)
		require.Equal(t, expected, result)
	})
	t.Run("EncodeDependencies Error", func(t *testing.T) {
		mockDependency := mocks.NewDependency(t)
		errMock := errors.New("mock error")
		mockDependency.On("MarshalBinary").Return(nil, errMock)

		dependencies := []extension.Dependency{mockDependency}
		result, err := EncodeDependencies(dependencies...)
		require.Error(t, err)
		require.Nil(t, result)
	})
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	sup := supervisor.NewSupervisor(
		supervisor.WithStrategy(supervisor.OneForAllStrategy),
		supervisor.WithRetry(3, 2*time.Second),
		supervisor.WithDirective(&gerrors.InternalError{}, supervisor.RestartDirective),
	)

	spec := EncodeSupervisor(sup)
	require.NotNil(t, spec)
	require.Equal(t, internalpb.SupervisorStrategy_SUPERVISOR_STRATEGY_ONE_FOR_ALL, spec.GetStrategy())
	require.EqualValues(t, 3, spec.GetMaxRetries())
	require.NotNil(t, spec.GetTimeout())
	require.Equal(t, 2*time.Second, spec.GetTimeout().AsDuration())

	decoded := DecodeSupervisor(spec)
	require.NotNil(t, decoded)
	require.Equal(t, supervisor.OneForAllStrategy, decoded.Strategy())
	require.EqualValues(t, 3, decoded.MaxRetries())
	require.Equal(t, 2*time.Second, decoded.Timeout())

	directive, ok := decoded.Directive(&gerrors.InternalError{})
	require.True(t, ok)
	require.Equal(t, supervisor.RestartDirective, directive)
}

func TestEncodeSupervisorAnyError(t *testing.T) {
	sup := supervisor.NewSupervisor(supervisor.WithAnyErrorDirective(supervisor.ResumeDirective))

	spec := EncodeSupervisor(sup)
	require.NotNil(t, spec)
	require.NotNil(t, spec.AnyErrorDirective)
	require.Equal(t, internalpb.SupervisorDirective_SUPERVISOR_DIRECTIVE_RESUME, spec.GetAnyErrorDirective())
	require.Len(t, spec.GetDirectives(), 0)

	decoded := DecodeSupervisor(spec)
	directive, ok := decoded.Directive(new(gerrors.AnyError))
	require.True(t, ok)
	require.Equal(t, supervisor.ResumeDirective, directive)

	_, ok = decoded.Directive(&gerrors.InternalError{})
	require.False(t, ok)
}

func TestEncodeNilSupervisor(t *testing.T) {
	require.Nil(t, EncodeSupervisor(nil))
}

func TestEncodeNilDirectives(t *testing.T) {
	spec := EncodeSupervisor(&supervisor.Supervisor{})
	require.NotNil(t, spec)
	require.Len(t, spec.GetDirectives(), 0)
	require.Nil(t, spec.AnyErrorDirective)
}

func TestEncodeEmptyDirectives(t *testing.T) {
	sup := supervisor.NewSupervisor()
	sup.Reset()

	spec := EncodeSupervisor(sup)
	require.NotNil(t, spec)
	require.Len(t, spec.GetDirectives(), 0)
	require.Nil(t, spec.AnyErrorDirective)
}

func TestEncodeSortsDirectives(t *testing.T) {
	sup := supervisor.NewSupervisor()
	sup.Reset()
	sup.SetDirectiveByType("errors.BError", supervisor.RestartDirective)
	sup.SetDirectiveByType("errors.AError", supervisor.ResumeDirective)

	spec := EncodeSupervisor(sup)
	require.Len(t, spec.GetDirectives(), 2)
	require.Equal(t, "errors.AError", spec.GetDirectives()[0].GetErrorType())
	require.Equal(t, "errors.BError", spec.GetDirectives()[1].GetErrorType())
}

func TestEncodeSupervisorSkipsEmptyErrorType(t *testing.T) {
	sup := &supervisor.Supervisor{}
	directives := ds.NewMap[string, supervisor.Directive]()
	directives.Set("", supervisor.ResumeDirective)
	setSupervisorDirectives(sup, directives)

	spec := EncodeSupervisor(sup)
	require.NotNil(t, spec)
	require.Len(t, spec.GetDirectives(), 0)
	require.Nil(t, spec.AnyErrorDirective)
}

func TestEncodeSupervisorDirectiveVariants(t *testing.T) {
	sup := supervisor.NewSupervisor()
	sup.Reset()
	sup.SetDirectiveByType("errors.AError", supervisor.EscalateDirective)
	sup.SetDirectiveByType("errors.BError", supervisor.StopDirective)

	spec := EncodeSupervisor(sup)
	require.Len(t, spec.GetDirectives(), 2)
	require.Equal(t, "errors.AError", spec.GetDirectives()[0].GetErrorType())
	require.Equal(t, internalpb.SupervisorDirective_SUPERVISOR_DIRECTIVE_ESCALATE, spec.GetDirectives()[0].GetDirective())
	require.Equal(t, "errors.BError", spec.GetDirectives()[1].GetErrorType())
	require.Equal(t, internalpb.SupervisorDirective_SUPERVISOR_DIRECTIVE_STOP, spec.GetDirectives()[1].GetDirective())
}

func TestDecodeNilSpec(t *testing.T) {
	require.Nil(t, DecodeSupervisor(nil))
}

func TestDecodeSkipsInvalidRules(t *testing.T) {
	errType := errorType(&decodeDirectiveError{})
	spec := &internalpb.SupervisorSpec{
		Strategy: internalpb.SupervisorStrategy_SUPERVISOR_STRATEGY_ONE_FOR_ONE,
		Directives: []*internalpb.SupervisorDirectiveRule{
			nil,
			{
				ErrorType: "",
				Directive: internalpb.SupervisorDirective_SUPERVISOR_DIRECTIVE_RESTART,
			},
			{
				ErrorType: errType,
				Directive: internalpb.SupervisorDirective_SUPERVISOR_DIRECTIVE_ESCALATE,
			},
		},
	}

	decoded := DecodeSupervisor(spec)
	require.NotNil(t, decoded)
	require.Equal(t, supervisor.OneForOneStrategy, decoded.Strategy())
	require.EqualValues(t, 0, decoded.MaxRetries())
	require.Equal(t, time.Duration(-1), decoded.Timeout())

	directive, ok := decoded.Directive(&decodeDirectiveError{})
	require.True(t, ok)
	require.Equal(t, supervisor.EscalateDirective, directive)
}

func TestDecodeWithMaxRetriesOnly(t *testing.T) {
	spec := &internalpb.SupervisorSpec{
		Strategy:   internalpb.SupervisorStrategy_SUPERVISOR_STRATEGY_ONE_FOR_ONE,
		MaxRetries: 5,
	}

	decoded := DecodeSupervisor(spec)
	require.NotNil(t, decoded)
	require.EqualValues(t, 5, decoded.MaxRetries())
	require.Equal(t, time.Duration(0), decoded.Timeout())
}

func errorType(err error) string {
	if err == nil {
		return "nil"
	}
	rtype := reflect.TypeOf(err)
	if rtype.Kind() == reflect.Ptr {
		rtype = rtype.Elem()
	}
	return rtype.String()
}

func setSupervisorDirectives(sup *supervisor.Supervisor, directives *ds.Map[string, supervisor.Directive]) {
	field := reflect.ValueOf(sup).Elem().FieldByName("directives")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(directives))
}
