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
	"fmt"
	"math"
	"sort"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v4/datacenter"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/supervisor"
)

// EncodeDependencies transforms a list of dependencies into their serialized protobuf representations.
// Returns a slice of internalpb.Dependency or an error if serialization fails.
func EncodeDependencies(dependencies ...extension.Dependency) ([]*internalpb.Dependency, error) {
	var output []*internalpb.Dependency
	for _, dependency := range dependencies {
		bytea, err := dependency.MarshalBinary()
		if err != nil {
			return nil, err
		}

		output = append(output, &internalpb.Dependency{
			Id:       dependency.ID(),
			TypeName: types.Name(dependency),
			Bytea:    bytea,
		})
	}
	return output, nil
}

// EncodePassivationStrategy encodes a passivation strategy into its protobuf representation.
// Returns a pointer to internalpb.PassivationStrategy or nil if the strategy is not recognized.
func EncodePassivationStrategy(strategy passivation.Strategy) *internalpb.PassivationStrategy {
	switch s := strategy.(type) {
	case *passivation.TimeBasedStrategy:
		return &internalpb.PassivationStrategy{
			Strategy: &internalpb.PassivationStrategy_TimeBased{
				TimeBased: &internalpb.TimeBasedPassivation{
					PassivateAfter: durationpb.New(s.Timeout()),
				},
			},
		}
	case *passivation.MessagesCountBasedStrategy:
		return &internalpb.PassivationStrategy{
			Strategy: &internalpb.PassivationStrategy_MessagesCountBased{
				MessagesCountBased: &internalpb.MessagesCountBasedPassivation{
					MaxMessages: int64(s.MaxMessages()),
				},
			},
		}
	case *passivation.LongLivedStrategy:
		return &internalpb.PassivationStrategy{
			Strategy: &internalpb.PassivationStrategy_LongLived{
				LongLived: new(internalpb.LongLivedPassivation),
			},
		}
	default:
		return nil
	}
}

// DecodePassivationStrategy decodes a protobuf representation of a passivation strategy into its corresponding passivation.Strategy.
// Returns a passivation.Strategy or nil if the strategy is not recognized.
func DecodePassivationStrategy(proto *internalpb.PassivationStrategy) passivation.Strategy {
	if proto == nil {
		return nil
	}

	switch s := proto.Strategy.(type) {
	case *internalpb.PassivationStrategy_TimeBased:
		return passivation.NewTimeBasedStrategy(s.TimeBased.GetPassivateAfter().AsDuration())
	case *internalpb.PassivationStrategy_MessagesCountBased:
		return passivation.NewMessageCountBasedStrategy(int(s.MessagesCountBased.GetMaxMessages()))
	case *internalpb.PassivationStrategy_LongLived:
		return passivation.NewLongLivedStrategy()
	default:
		return nil
	}
}

// EncodeSupervisor converts a supervisor instance to its internal protobuf representation.
func EncodeSupervisor(supervisor *supervisor.Supervisor) *internalpb.SupervisorSpec {
	if supervisor == nil {
		return nil
	}

	spec := &internalpb.SupervisorSpec{
		Strategy:   encodeSupervisorStrategy(supervisor.Strategy()),
		MaxRetries: supervisor.MaxRetries(),
		Timeout:    durationpb.New(supervisor.Timeout()),
	}

	if directive, ok := supervisor.AnyErrorDirective(); ok {
		encoded := encodeSupervisorDirective(directive)
		spec.AnyErrorDirective = &encoded
		return spec
	}

	rules := supervisor.Rules()
	if len(rules) == 0 {
		return spec
	}

	directives := make([]*internalpb.SupervisorDirectiveRule, 0, len(rules))
	for _, rule := range rules {
		if rule.ErrorType == "" {
			continue
		}
		directives = append(directives, &internalpb.SupervisorDirectiveRule{
			ErrorType: rule.ErrorType,
			Directive: encodeSupervisorDirective(rule.Directive),
		})
	}

	if len(directives) == 0 {
		return spec
	}

	sort.Slice(directives, func(i, j int) bool {
		return directives[i].GetErrorType() < directives[j].GetErrorType()
	})
	spec.Directives = directives
	return spec
}

// DecodeSupervisor converts an internal protobuf representation into a supervisor instance.
func DecodeSupervisor(spec *internalpb.SupervisorSpec) *supervisor.Supervisor {
	if spec == nil {
		return nil
	}

	opts := []supervisor.SupervisorOption{
		supervisor.WithStrategy(decodeSupervisorStrategy(spec.GetStrategy())),
	}

	timeoutSet := spec.GetTimeout() != nil
	var timeout time.Duration
	if timeoutSet {
		timeout = spec.GetTimeout().AsDuration()
	}
	if timeoutSet || spec.GetMaxRetries() != 0 {
		opts = append(opts, supervisor.WithRetry(spec.GetMaxRetries(), timeout))
	}

	if spec.AnyErrorDirective != nil {
		opts = append(opts, supervisor.WithAnyErrorDirective(decodeSupervisorDirective(spec.GetAnyErrorDirective())))
		return supervisor.NewSupervisor(opts...)
	}

	decoded := supervisor.NewSupervisor(opts...)
	for _, rule := range spec.GetDirectives() {
		if rule == nil {
			continue
		}
		errType := rule.GetErrorType()
		if errType == "" {
			continue
		}
		decoded.SetDirectiveByType(errType, decodeSupervisorDirective(rule.GetDirective()))
	}

	return decoded
}

// EncodeReentrancy encodes a Reentrancy configuration into its protobuf representation.
func EncodeReentrancy(reentrancy *reentrancy.Reentrancy) *internalpb.ReentrancyConfig {
	if reentrancy == nil {
		return nil
	}
	maxInFlight := reentrancy.MaxInFlight()
	if maxInFlight < 0 {
		maxInFlight = 0
	}
	var limit uint32
	switch {
	case maxInFlight <= 0:
		limit = 0
	case uint64(maxInFlight) > math.MaxUint32:
		limit = math.MaxUint32
	default:
		limit = uint32(maxInFlight)
	}
	return &internalpb.ReentrancyConfig{
		Mode:        toInternalReentrancyMode(reentrancy.Mode()),
		MaxInFlight: limit,
	}
}

// DecodeReentrancy decodes a protobuf representation of a Reentrancy configuration into its corresponding Reentrancy instance.
func DecodeReentrancy(config *internalpb.ReentrancyConfig) *reentrancy.Reentrancy {
	if config == nil {
		return nil
	}
	mode := fromInternalReentrancyMode(config.GetMode())
	maxInFlight := int(config.GetMaxInFlight())
	return reentrancy.New(
		reentrancy.WithMode(mode),
		reentrancy.WithMaxInFlight(maxInFlight),
	)
}

// toInternalReentrancyMode maps local modes to protobuf enums.
func toInternalReentrancyMode(mode reentrancy.Mode) internalpb.ReentrancyMode {
	switch mode {
	case reentrancy.AllowAll:
		return internalpb.ReentrancyMode_REENTRANCY_MODE_ALLOW_ALL
	case reentrancy.StashNonReentrant:
		return internalpb.ReentrancyMode_REENTRANCY_MODE_STASH_NON_REENTRANT
	default:
		return internalpb.ReentrancyMode_REENTRANCY_MODE_OFF
	}
}

// fromInternalReentrancyMode maps protobuf enums to local modes.
func fromInternalReentrancyMode(mode internalpb.ReentrancyMode) reentrancy.Mode {
	switch mode {
	case internalpb.ReentrancyMode_REENTRANCY_MODE_ALLOW_ALL:
		return reentrancy.AllowAll
	case internalpb.ReentrancyMode_REENTRANCY_MODE_STASH_NON_REENTRANT:
		return reentrancy.StashNonReentrant
	default:
		return reentrancy.Off
	}
}

func encodeSupervisorStrategy(strategy supervisor.Strategy) internalpb.SupervisorStrategy {
	switch strategy {
	case supervisor.OneForAllStrategy:
		return internalpb.SupervisorStrategy_SUPERVISOR_STRATEGY_ONE_FOR_ALL
	default:
		return internalpb.SupervisorStrategy_SUPERVISOR_STRATEGY_ONE_FOR_ONE
	}
}

func decodeSupervisorStrategy(strategy internalpb.SupervisorStrategy) supervisor.Strategy {
	switch strategy {
	case internalpb.SupervisorStrategy_SUPERVISOR_STRATEGY_ONE_FOR_ALL:
		return supervisor.OneForAllStrategy
	default:
		return supervisor.OneForOneStrategy
	}
}

func encodeSupervisorDirective(directive supervisor.Directive) internalpb.SupervisorDirective {
	switch directive {
	case supervisor.ResumeDirective:
		return internalpb.SupervisorDirective_SUPERVISOR_DIRECTIVE_RESUME
	case supervisor.RestartDirective:
		return internalpb.SupervisorDirective_SUPERVISOR_DIRECTIVE_RESTART
	case supervisor.EscalateDirective:
		return internalpb.SupervisorDirective_SUPERVISOR_DIRECTIVE_ESCALATE
	default:
		return internalpb.SupervisorDirective_SUPERVISOR_DIRECTIVE_STOP
	}
}

func decodeSupervisorDirective(directive internalpb.SupervisorDirective) supervisor.Directive {
	switch directive {
	case internalpb.SupervisorDirective_SUPERVISOR_DIRECTIVE_RESUME:
		return supervisor.ResumeDirective
	case internalpb.SupervisorDirective_SUPERVISOR_DIRECTIVE_RESTART:
		return supervisor.RestartDirective
	case internalpb.SupervisorDirective_SUPERVISOR_DIRECTIVE_ESCALATE:
		return supervisor.EscalateDirective
	default:
		return supervisor.StopDirective
	}
}

func EncodeDataCenterRecord(record datacenter.DataCenterRecord) ([]byte, error) {
	pbRecord, err := toProto(record)
	if err != nil {
		return nil, err
	}
	return proto.Marshal(pbRecord)
}

func DecodeDataCenterRecord(payload []byte) (datacenter.DataCenterRecord, error) {
	pbRecord := &internalpb.DataCenterRecord{}
	if err := proto.Unmarshal(payload, pbRecord); err != nil {
		return datacenter.DataCenterRecord{}, fmt.Errorf("multidc/etcd: failed to decode record: %w", err)
	}
	return fromProto(pbRecord), nil
}

func toProto(record datacenter.DataCenterRecord) (*internalpb.DataCenterRecord, error) {
	state, err := toProtoState(record.State)
	if err != nil {
		return nil, err
	}

	pbRecord := &internalpb.DataCenterRecord{
		Id: record.ID,
		DataCenter: &internalpb.DataCenter{
			Name:   record.DataCenter.Name,
			Region: record.DataCenter.Region,
			Zone:   record.DataCenter.Zone,
			Labels: record.DataCenter.Labels,
		},
		Endpoints: record.Endpoints,
		State:     state,
		Version:   record.Version,
	}

	if !record.LeaseExpiry.IsZero() {
		pbRecord.LeaseExpiry = timestamppb.New(record.LeaseExpiry)
	}

	return pbRecord, nil
}

func fromProto(record *internalpb.DataCenterRecord) datacenter.DataCenterRecord {
	var dataCenter datacenter.DataCenter
	if record.GetDataCenter() != nil {
		dataCenter = datacenter.DataCenter{
			Name:   record.GetDataCenter().GetName(),
			Region: record.GetDataCenter().GetRegion(),
			Zone:   record.GetDataCenter().GetZone(),
			Labels: record.GetDataCenter().GetLabels(),
		}
	}

	mapped := datacenter.DataCenterRecord{
		ID:         record.GetId(),
		DataCenter: dataCenter,
		Endpoints:  record.GetEndpoints(),
		State:      fromProtoState(record.GetState()),
		Version:    record.GetVersion(),
	}

	if record.LeaseExpiry != nil {
		mapped.LeaseExpiry = record.LeaseExpiry.AsTime()
	}

	return mapped
}

func toProtoState(state datacenter.DataCenterState) (internalpb.DataCenterState, error) {
	switch state {
	case datacenter.DataCenterRegistered:
		return internalpb.DataCenterState_DATA_CENTER_STATE_REGISTERED, nil
	case datacenter.DataCenterActive:
		return internalpb.DataCenterState_DATA_CENTER_STATE_ACTIVE, nil
	case datacenter.DataCenterDraining:
		return internalpb.DataCenterState_DATA_CENTER_STATE_DRAINING, nil
	case datacenter.DataCenterInactive:
		return internalpb.DataCenterState_DATA_CENTER_STATE_INACTIVE, nil
	case "":
		return internalpb.DataCenterState_DATA_CENTER_STATE_UNSPECIFIED, nil
	default:
		return internalpb.DataCenterState_DATA_CENTER_STATE_UNSPECIFIED, fmt.Errorf("unsupported state %q", state)
	}
}

func fromProtoState(state internalpb.DataCenterState) datacenter.DataCenterState {
	switch state {
	case internalpb.DataCenterState_DATA_CENTER_STATE_REGISTERED:
		return datacenter.DataCenterRegistered
	case internalpb.DataCenterState_DATA_CENTER_STATE_ACTIVE:
		return datacenter.DataCenterActive
	case internalpb.DataCenterState_DATA_CENTER_STATE_DRAINING:
		return datacenter.DataCenterDraining
	case internalpb.DataCenterState_DATA_CENTER_STATE_INACTIVE:
		return datacenter.DataCenterInactive
	default:
		return ""
	}
}
