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
	"math"
	"reflect"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/datacenter"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/registry"
	"github.com/tochemey/goakt/v4/internal/xsync"
	mocks "github.com/tochemey/goakt/v4/mocks/extension"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/supervisor"
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

func TestEncodeReentrancy(t *testing.T) {
	tests := []struct {
		name      string
		value     *reentrancy.Reentrancy
		wantMode  internalpb.ReentrancyMode
		wantLimit uint32
	}{
		{
			name: "allow all",
			value: reentrancy.New(
				reentrancy.WithMode(reentrancy.AllowAll),
				reentrancy.WithMaxInFlight(5),
			),
			wantMode:  internalpb.ReentrancyMode_REENTRANCY_MODE_ALLOW_ALL,
			wantLimit: 5,
		},
		{
			name: "stash non reentrant",
			value: reentrancy.New(
				reentrancy.WithMode(reentrancy.StashNonReentrant),
				reentrancy.WithMaxInFlight(2),
			),
			wantMode:  internalpb.ReentrancyMode_REENTRANCY_MODE_STASH_NON_REENTRANT,
			wantLimit: 2,
		},
		{
			name: "off defaults",
			value: reentrancy.New(
				reentrancy.WithMode(reentrancy.Off),
				reentrancy.WithMaxInFlight(0),
			),
			wantMode:  internalpb.ReentrancyMode_REENTRANCY_MODE_OFF,
			wantLimit: 0,
		},
		{
			name: "unknown mode defaults to off",
			value: reentrancy.New(
				reentrancy.WithMode(reentrancy.Mode(99)),
				reentrancy.WithMaxInFlight(3),
			),
			wantMode:  internalpb.ReentrancyMode_REENTRANCY_MODE_OFF,
			wantLimit: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := EncodeReentrancy(tt.value)
			require.NotNil(t, encoded)
			require.Equal(t, tt.wantMode, encoded.GetMode())
			require.Equal(t, tt.wantLimit, encoded.GetMaxInFlight())
		})
	}
}

func TestEncodeReentrancyNegativeMaxInFlightClamps(t *testing.T) {
	cfg := reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))
	setReentrancyMaxInFlight(cfg, -5)

	encoded := EncodeReentrancy(cfg)
	require.NotNil(t, encoded)
	require.Equal(t, uint32(0), encoded.GetMaxInFlight())
}

func TestEncodeReentrancyLargeMaxInFlightClamps(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("int size too small to exceed MaxUint32")
	}

	cfg := reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))
	setReentrancyMaxInFlight(cfg, int(uint64(math.MaxUint32)+1))

	encoded := EncodeReentrancy(cfg)
	require.NotNil(t, encoded)
	require.Equal(t, uint32(math.MaxUint32), encoded.GetMaxInFlight())
}

func TestEncodeReentrancyNil(t *testing.T) {
	require.Nil(t, EncodeReentrancy(nil))
}

func TestDecodeReentrancy(t *testing.T) {
	tests := []struct {
		name      string
		value     *internalpb.ReentrancyConfig
		wantMode  reentrancy.Mode
		wantLimit int
	}{
		{
			name: "allow all",
			value: &internalpb.ReentrancyConfig{
				Mode:        internalpb.ReentrancyMode_REENTRANCY_MODE_ALLOW_ALL,
				MaxInFlight: 5,
			},
			wantMode:  reentrancy.AllowAll,
			wantLimit: 5,
		},
		{
			name: "stash non reentrant",
			value: &internalpb.ReentrancyConfig{
				Mode:        internalpb.ReentrancyMode_REENTRANCY_MODE_STASH_NON_REENTRANT,
				MaxInFlight: 2,
			},
			wantMode:  reentrancy.StashNonReentrant,
			wantLimit: 2,
		},
		{
			name: "off",
			value: &internalpb.ReentrancyConfig{
				Mode:        internalpb.ReentrancyMode_REENTRANCY_MODE_OFF,
				MaxInFlight: 0,
			},
			wantMode:  reentrancy.Off,
			wantLimit: 0,
		},
		{
			name: "unknown defaults to off",
			value: &internalpb.ReentrancyConfig{
				Mode:        internalpb.ReentrancyMode(99),
				MaxInFlight: 3,
			},
			wantMode:  reentrancy.Off,
			wantLimit: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decoded := DecodeReentrancy(tt.value)
			require.NotNil(t, decoded)
			require.Equal(t, tt.wantMode, decoded.Mode())
			require.Equal(t, tt.wantLimit, decoded.MaxInFlight())
		})
	}
}

func TestDecodeReentrancyNil(t *testing.T) {
	require.Nil(t, DecodeReentrancy(nil))
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
	directives := xsync.NewMap[string, supervisor.Directive]()
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

func setSupervisorDirectives(sup *supervisor.Supervisor, directives *xsync.Map[string, supervisor.Directive]) {
	field := reflect.ValueOf(sup).Elem().FieldByName("directives")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(directives))
}

func setReentrancyMaxInFlight(cfg *reentrancy.Reentrancy, maxInFlight int) {
	if cfg == nil {
		return
	}
	field := reflect.ValueOf(cfg).Elem().FieldByName("maxInFlight")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().SetInt(int64(maxInFlight))
}

func TestEncodeDecodeDataCenterRecordRoundTrip(t *testing.T) {
	leaseExpiry := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	record := datacenter.DataCenterRecord{
		ID: "dc-1",
		DataCenter: datacenter.DataCenter{
			Name:   "dc-1",
			Region: "us-east",
			Zone:   "us-east-1a",
			Labels: map[string]string{
				"tier": "primary",
			},
		},
		Endpoints:   []string{"10.0.0.1:9000", "10.0.0.2:9000"},
		State:       datacenter.DataCenterActive,
		LeaseExpiry: leaseExpiry,
		Version:     42,
	}

	payload, err := EncodeDataCenterRecord(record)
	require.NoError(t, err)

	decoded, err := DecodeDataCenterRecord(payload)
	require.NoError(t, err)
	require.Equal(t, record.ID, decoded.ID)
	require.Equal(t, record.DataCenter, decoded.DataCenter)
	require.Equal(t, record.Endpoints, decoded.Endpoints)
	require.Equal(t, record.State, decoded.State)
	require.Equal(t, record.Version, decoded.Version)
	require.True(t, decoded.LeaseExpiry.Equal(record.LeaseExpiry))
}

func TestEncodeDecodeDataCenterRecordStateVariants(t *testing.T) {
	tests := []struct {
		name  string
		state datacenter.DataCenterState
	}{
		{
			name:  "registered",
			state: datacenter.DataCenterRegistered,
		},
		{
			name:  "draining",
			state: datacenter.DataCenterDraining,
		},
		{
			name:  "inactive",
			state: datacenter.DataCenterInactive,
		},
		{
			name:  "empty",
			state: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := datacenter.DataCenterRecord{
				ID:    "dc-state",
				State: tt.state,
				DataCenter: datacenter.DataCenter{
					Name: "dc-state",
				},
			}

			payload, err := EncodeDataCenterRecord(record)
			require.NoError(t, err)

			decoded, err := DecodeDataCenterRecord(payload)
			require.NoError(t, err)
			require.Equal(t, tt.state, decoded.State)
			require.True(t, decoded.LeaseExpiry.IsZero())
		})
	}
}

func TestEncodeDataCenterRecordUnsupportedState(t *testing.T) {
	record := datacenter.DataCenterRecord{
		ID:    "dc-bad-state",
		State: datacenter.DataCenterState("BAD"),
	}

	payload, err := EncodeDataCenterRecord(record)
	require.Error(t, err)
	require.Nil(t, payload)
}

func TestDecodeDataCenterRecordInvalidPayload(t *testing.T) {
	_, err := DecodeDataCenterRecord([]byte{0xff, 0xff, 0xff})
	require.Error(t, err)
}

func TestDecodeDataCenterRecordMissingFields(t *testing.T) {
	pbRecord := &internalpb.DataCenterRecord{
		Id:        "dc-missing",
		Endpoints: []string{"127.0.0.1:8080"},
		Version:   7,
		State:     internalpb.DataCenterState_DATA_CENTER_STATE_UNSPECIFIED,
	}
	payload, err := proto.Marshal(pbRecord)
	require.NoError(t, err)

	decoded, err := DecodeDataCenterRecord(payload)
	require.NoError(t, err)
	require.Equal(t, "dc-missing", decoded.ID)
	require.Equal(t, datacenter.DataCenter{}, decoded.DataCenter)
	require.Equal(t, []string{"127.0.0.1:8080"}, decoded.Endpoints)
	require.Equal(t, datacenter.DataCenterState(""), decoded.State)
	require.True(t, decoded.LeaseExpiry.IsZero())
	require.Equal(t, uint64(7), decoded.Version)
}
