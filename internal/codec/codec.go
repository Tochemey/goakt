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
	"reflect"
	"sort"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v4/crdt"
	"github.com/tochemey/goakt/v4/datacenter"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/passivation"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/remote"
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

// DecodeDependencies decodes protobuf dependencies into extension.Dependency instances
// using the provided registry to resolve types by name.
//
// Parameters:
//   - registry: Type registry containing registered dependency implementations.
//   - dependencies: Protobuf dependency messages from a remote response.
//
// Returns:
//   - A slice of decoded extension.Dependency instances.
//
// Errors:
//   - When registry is nil.
//   - When a dependency type is not registered in the registry.
//   - When UnmarshalBinary fails for a dependency.
func DecodeDependencies(registry types.Registry, dependencies ...*internalpb.Dependency) ([]extension.Dependency, error) {
	if registry == nil {
		return nil, fmt.Errorf("registry required to decode dependencies")
	}
	deps := make([]extension.Dependency, 0, len(dependencies))
	for _, dep := range dependencies {
		if dep == nil {
			continue
		}
		dependency, err := decodeDependencyFromBytes(registry, dep.GetTypeName(), dep.GetBytea())
		if err != nil {
			return nil, err
		}
		deps = append(deps, dependency)
	}
	return deps, nil
}

func decodeDependencyFromBytes(registry types.Registry, typeName string, bytea []byte) (extension.Dependency, error) {
	dept, ok := registry.TypeOf(typeName)
	if !ok {
		return nil, fmt.Errorf("dependency type %q not registered", typeName)
	}
	elem := reflect.TypeOf((*extension.Dependency)(nil)).Elem()
	if !dept.Implements(elem) && !reflect.PointerTo(dept).Implements(elem) {
		return nil, fmt.Errorf("type %q does not implement extension.Dependency", typeName)
	}
	instance := reflect.New(dept)
	if dependency, ok := instance.Interface().(extension.Dependency); ok {
		if err := dependency.UnmarshalBinary(bytea); err != nil {
			return nil, err
		}
		return dependency, nil
	}
	return nil, fmt.Errorf("failed to instantiate dependency %q", typeName)
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

const stringTypeURL = "type.goakt.dev/string"

func stringToAny(s string) *anypb.Any {
	return &anypb.Any{
		TypeUrl: stringTypeURL,
		Value:   []byte(s),
	}
}

func anyToString(a *anypb.Any) string {
	if a == nil {
		return ""
	}
	return string(a.GetValue())
}

// EncodeActorState converts remote.ActorState to internalpb.State for wire transmission.
// The remote.ActorState values align 1:1 with the proto enum (0-5).
func EncodeActorState(state remote.ActorState) internalpb.State {
	return internalpb.State(state)
}

// ORSetStringEncoder is a type assertion target for ORSet[string] encoding.
// Since ORSet is generic, we match on this interface rather than a concrete type.
type ORSetStringEncoder interface {
	RawState() ([]crdt.Entry[string], map[string]uint64)
}

// EncodeCRDTData converts a crdt.ReplicatedData to its protobuf representation.
func EncodeCRDTData(data crdt.ReplicatedData) (*internalpb.CRDTData, error) {
	switch v := data.(type) {
	case *crdt.GCounter:
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_GCounter{
				GCounter: encodeGCounter(v),
			},
		}, nil
	case *crdt.PNCounter:
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_PnCounter{
				PnCounter: encodePNCounter(v),
			},
		}, nil
	case ORSetStringEncoder:
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_OrSet{
				OrSet: encodeORSetString(v),
			},
		}, nil
	case *crdt.Flag:
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_Flag{
				Flag: encodeFlag(v),
			},
		}, nil
	case LWWRegisterStringEncoder:
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_LwwRegister{
				LwwRegister: encodeLWWRegisterString(v),
			},
		}, nil
	case MVRegisterStringEncoder:
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_MvRegister{
				MvRegister: encodeMVRegisterString(v),
			},
		}, nil
	case ORMapStringGCounterEncoder:
		orMapData, err := encodeORMapStringGCounter(v)
		if err != nil {
			return nil, err
		}
		return &internalpb.CRDTData{
			Type: &internalpb.CRDTData_OrMap{
				OrMap: orMapData,
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported CRDT type: %T", data)
	}
}

// DecodeCRDTData converts a protobuf CRDTData back to a crdt.ReplicatedData.
func DecodeCRDTData(pb *internalpb.CRDTData) (crdt.ReplicatedData, error) {
	if pb == nil {
		return nil, fmt.Errorf("nil CRDTData")
	}
	switch v := pb.Type.(type) {
	case *internalpb.CRDTData_GCounter:
		return decodeGCounter(v.GCounter), nil
	case *internalpb.CRDTData_PnCounter:
		return decodePNCounter(v.PnCounter), nil
	case *internalpb.CRDTData_OrSet:
		return decodeORSetString(v.OrSet), nil
	case *internalpb.CRDTData_Flag:
		return decodeFlag(v.Flag), nil
	case *internalpb.CRDTData_LwwRegister:
		return decodeLWWRegisterString(v.LwwRegister), nil
	case *internalpb.CRDTData_MvRegister:
		return decodeMVRegisterString(v.MvRegister), nil
	case *internalpb.CRDTData_OrMap:
		return decodeORMapStringGCounter(v.OrMap)
	default:
		return nil, fmt.Errorf("unsupported CRDTData type: %T", pb.Type)
	}
}

// EncodeCRDTKey converts a CRDT key ID and data type to its protobuf representation.
func EncodeCRDTKey(keyID string, dataType crdt.DataType) *internalpb.CRDTKey {
	return &internalpb.CRDTKey{
		Id:       keyID,
		DataType: internalpb.CRDTDataType(dataType + 1), // +1 because proto enum 0 is UNSPECIFIED
	}
}

// DecodeCRDTKey extracts key ID and data type from a protobuf CRDTKey.
func DecodeCRDTKey(pb *internalpb.CRDTKey) (keyID string, dataType crdt.DataType, err error) {
	if pb == nil {
		return "", 0, fmt.Errorf("nil CRDTKey")
	}
	dt := pb.GetDataType()
	if dt == internalpb.CRDTDataType_CRDT_DATA_TYPE_UNSPECIFIED {
		return "", 0, fmt.Errorf("unspecified CRDT data type for key=%s", pb.GetId())
	}
	if dt < internalpb.CRDTDataType_CRDT_DATA_TYPE_G_COUNTER || dt > internalpb.CRDTDataType_CRDT_DATA_TYPE_MV_REGISTER {
		return "", 0, fmt.Errorf("unknown CRDT data type %d for key=%s", dt, pb.GetId())
	}
	return pb.GetId(), crdt.DataType(dt - 1), nil
}

func encodeGCounter(c *crdt.GCounter) *internalpb.GCounterData {
	return &internalpb.GCounterData{
		State: c.State(),
	}
}

func decodeGCounter(pb *internalpb.GCounterData) *crdt.GCounter {
	return crdt.GCounterFromState(pb.GetState())
}

func encodePNCounter(c *crdt.PNCounter) *internalpb.PNCounterData {
	inc, dec := c.State()
	return &internalpb.PNCounterData{
		Increments: &internalpb.GCounterData{State: inc},
		Decrements: &internalpb.GCounterData{State: dec},
	}
}

func decodePNCounter(pb *internalpb.PNCounterData) *crdt.PNCounter {
	return crdt.PNCounterFromState(
		pb.GetIncrements().GetState(),
		pb.GetDecrements().GetState(),
	)
}

func encodeORSetString(s ORSetStringEncoder) *internalpb.ORSetData {
	entries, clock := s.RawState()
	return encodeORSetEntries(entries, clock)
}

func decodeORSetString(pb *internalpb.ORSetData) *crdt.ORSet[string] {
	entries := decodeORSetEntries(pb)
	return crdt.ORSetFromRawState(entries, pb.GetClock())
}

func encodeFlag(f *crdt.Flag) *internalpb.FlagData {
	return &internalpb.FlagData{
		Enabled: f.Enabled(),
	}
}

func decodeFlag(pb *internalpb.FlagData) *crdt.Flag {
	if pb.GetEnabled() {
		return crdt.NewFlag().Enable()
	}
	return crdt.NewFlag()
}

// LWWRegisterStringEncoder is a type assertion target for LWWRegister[string] encoding.
type LWWRegisterStringEncoder interface {
	Value() string
	Timestamp() int64
	NodeID() string
}

func encodeLWWRegisterString(r LWWRegisterStringEncoder) *internalpb.LWWRegisterData {
	return &internalpb.LWWRegisterData{
		Value:          stringToAny(r.Value()),
		TimestampNanos: r.Timestamp(),
		NodeId:         r.NodeID(),
	}
}

func decodeLWWRegisterString(pb *internalpb.LWWRegisterData) *crdt.LWWRegister[string] {
	return crdt.LWWRegisterFromState(
		anyToString(pb.GetValue()),
		pb.GetTimestampNanos(),
		pb.GetNodeId(),
	)
}

// MVRegisterStringEncoder is a type assertion target for MVRegister[string] encoding.
type MVRegisterStringEncoder interface {
	RawState() ([]crdt.MVEntry[string], map[string]uint64)
}

func encodeMVRegisterString(r MVRegisterStringEncoder) *internalpb.MVRegisterData {
	entries, clock := r.RawState()
	pbEntries := make([]*internalpb.MVRegisterData_MVRegisterEntry, 0, len(entries))
	for _, e := range entries {
		pbEntries = append(pbEntries, &internalpb.MVRegisterData_MVRegisterEntry{
			Value:   stringToAny(e.Value),
			NodeId:  e.Dot.NodeID,
			Counter: e.Dot.Counter,
		})
	}
	return &internalpb.MVRegisterData{
		Entries: pbEntries,
		Clock:   clock,
	}
}

func decodeMVRegisterString(pb *internalpb.MVRegisterData) *crdt.MVRegister[string] {
	entries := make([]crdt.MVEntry[string], 0, len(pb.GetEntries()))
	for _, e := range pb.GetEntries() {
		entries = append(entries, crdt.MVEntry[string]{
			Value: anyToString(e.GetValue()),
			Dot: crdt.Dot{
				NodeID:  e.GetNodeId(),
				Counter: e.GetCounter(),
			},
		})
	}
	return crdt.MVRegisterFromRawState(entries, pb.GetClock())
}

// ORMapStringGCounterEncoder is a type assertion target for ORMap[string, *GCounter] encoding.
type ORMapStringGCounterEncoder interface {
	RawState() crdt.ORMapRawState[string, *crdt.GCounter]
}

func encodeORMapStringGCounter(m ORMapStringGCounterEncoder) (*internalpb.ORMapData, error) {
	state := m.RawState()

	// encode key set as ORSetData
	keySet := encodeORSetEntries(state.KeyEntries, state.KeyClock)

	// encode values
	pbEntries := make([]*internalpb.ORMapData_ORMapEntry, 0, len(state.Values))
	for k, v := range state.Values {
		valData, err := EncodeCRDTData(v)
		if err != nil {
			return nil, fmt.Errorf("failed to encode ORMap value for key=%s: %w", k, err)
		}
		pbEntries = append(pbEntries, &internalpb.ORMapData_ORMapEntry{
			Key:   stringToAny(k),
			Value: valData,
		})
	}

	return &internalpb.ORMapData{
		Entries: pbEntries,
		KeySet:  keySet,
	}, nil
}

func decodeORMapStringGCounter(pb *internalpb.ORMapData) (*crdt.ORMap[string, *crdt.GCounter], error) {
	// decode key set
	keyEntries := decodeORSetEntries(pb.GetKeySet())
	keyClock := pb.GetKeySet().GetClock()

	// decode values
	values := make(map[string]*crdt.GCounter, len(pb.GetEntries()))
	for _, e := range pb.GetEntries() {
		key := anyToString(e.GetKey())
		data, err := DecodeCRDTData(e.GetValue())
		if err != nil {
			return nil, fmt.Errorf("failed to decode ORMap value for key=%s: %w", key, err)
		}
		gc, ok := data.(*crdt.GCounter)
		if !ok {
			return nil, fmt.Errorf("ORMap value for key=%s is %T, expected *GCounter", key, data)
		}
		values[key] = gc
	}

	rawState := crdt.ORMapRawState[string, *crdt.GCounter]{
		KeyEntries: keyEntries,
		KeyClock:   keyClock,
		Values:     values,
	}
	return crdt.ORMapFromRawState(rawState), nil
}

// encodeORSetEntries encodes ORSet entries and clock to protobuf.
func encodeORSetEntries(entries []crdt.Entry[string], clock map[string]uint64) *internalpb.ORSetData {
	pbEntries := make([]*internalpb.ORSetData_ORSetEntry, 0, len(entries))
	for _, e := range entries {
		pbDots := make([]*internalpb.ORSetData_ORSetDot, len(e.Dots))
		for i, d := range e.Dots {
			pbDots[i] = &internalpb.ORSetData_ORSetDot{
				NodeId:  d.NodeID,
				Counter: d.Counter,
			}
		}
		pbEntries = append(pbEntries, &internalpb.ORSetData_ORSetEntry{
			Element: stringToAny(e.Element),
			Dots:    pbDots,
		})
	}
	return &internalpb.ORSetData{
		Entries: pbEntries,
		Clock:   clock,
	}
}

// decodeORSetEntries decodes ORSet entries from protobuf.
func decodeORSetEntries(pb *internalpb.ORSetData) []crdt.Entry[string] {
	entries := make([]crdt.Entry[string], 0, len(pb.GetEntries()))
	for _, e := range pb.GetEntries() {
		dots := make([]crdt.Dot, len(e.GetDots()))
		for i, d := range e.GetDots() {
			dots[i] = crdt.Dot{
				NodeID:  d.GetNodeId(),
				Counter: d.GetCounter(),
			}
		}
		entries = append(entries, crdt.Entry[string]{
			Element: anyToString(e.GetElement()),
			Dots:    dots,
		})
	}
	return entries
}
