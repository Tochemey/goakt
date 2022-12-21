// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        (unknown)
// source: actors/v1/persistence.proto

package actorsv1

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	anypb "google.golang.org/protobuf/types/known/anypb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type Journal struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the persistence unique identifier
	PersistenceId string `protobuf:"bytes,1,opt,name=persistence_id,json=persistenceId,proto3" json:"persistence_id,omitempty"`
	// Specifies the sequence number
	SequenceNumber uint64 `protobuf:"varint,2,opt,name=sequence_number,json=sequenceNumber,proto3" json:"sequence_number,omitempty"`
	// Specifies the deletion state
	IsDeleted bool `protobuf:"varint,3,opt,name=is_deleted,json=isDeleted,proto3" json:"is_deleted,omitempty"`
	// Specifies the payload manifest
	PayloadManifest string `protobuf:"bytes,4,opt,name=payload_manifest,json=payloadManifest,proto3" json:"payload_manifest,omitempty"`
	// Specifies the payload to persist
	Payload []byte `protobuf:"bytes,5,opt,name=payload,proto3" json:"payload,omitempty"`
	// Specifies the timestamp
	Timestamp *timestamppb.Timestamp `protobuf:"bytes,6,opt,name=timestamp,proto3" json:"timestamp,omitempty"`
	// Specifies the writer ID
	// Often times persistence envelopes will be written to the data store asynchronously
	// TODO need to confirm this feature
	WriterId string `protobuf:"bytes,7,opt,name=writer_id,json=writerId,proto3" json:"writer_id,omitempty"`
}

func (x *Journal) Reset() {
	*x = Journal{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_persistence_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Journal) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Journal) ProtoMessage() {}

func (x *Journal) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_persistence_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Journal.ProtoReflect.Descriptor instead.
func (*Journal) Descriptor() ([]byte, []int) {
	return file_actors_v1_persistence_proto_rawDescGZIP(), []int{0}
}

func (x *Journal) GetPersistenceId() string {
	if x != nil {
		return x.PersistenceId
	}
	return ""
}

func (x *Journal) GetSequenceNumber() uint64 {
	if x != nil {
		return x.SequenceNumber
	}
	return 0
}

func (x *Journal) GetIsDeleted() bool {
	if x != nil {
		return x.IsDeleted
	}
	return false
}

func (x *Journal) GetPayloadManifest() string {
	if x != nil {
		return x.PayloadManifest
	}
	return ""
}

func (x *Journal) GetPayload() []byte {
	if x != nil {
		return x.Payload
	}
	return nil
}

func (x *Journal) GetTimestamp() *timestamppb.Timestamp {
	if x != nil {
		return x.Timestamp
	}
	return nil
}

func (x *Journal) GetWriterId() string {
	if x != nil {
		return x.WriterId
	}
	return ""
}

type Snapshot struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the persistence unique identifier
	PersistenceId string `protobuf:"bytes,1,opt,name=persistence_id,json=persistenceId,proto3" json:"persistence_id,omitempty"`
	// Specifies the sequence number
	SequenceNumber uint64 `protobuf:"varint,2,opt,name=sequence_number,json=sequenceNumber,proto3" json:"sequence_number,omitempty"`
	// Specifies the payload manifest
	PayloadManifest string `protobuf:"bytes,4,opt,name=payload_manifest,json=payloadManifest,proto3" json:"payload_manifest,omitempty"`
	// Specifies the payload to persist
	Payload []byte `protobuf:"bytes,5,opt,name=payload,proto3" json:"payload,omitempty"`
	// Specifies the timestamp
	Timestamp *timestamppb.Timestamp `protobuf:"bytes,6,opt,name=timestamp,proto3" json:"timestamp,omitempty"`
}

func (x *Snapshot) Reset() {
	*x = Snapshot{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_persistence_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Snapshot) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Snapshot) ProtoMessage() {}

func (x *Snapshot) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_persistence_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Snapshot.ProtoReflect.Descriptor instead.
func (*Snapshot) Descriptor() ([]byte, []int) {
	return file_actors_v1_persistence_proto_rawDescGZIP(), []int{1}
}

func (x *Snapshot) GetPersistenceId() string {
	if x != nil {
		return x.PersistenceId
	}
	return ""
}

func (x *Snapshot) GetSequenceNumber() uint64 {
	if x != nil {
		return x.SequenceNumber
	}
	return 0
}

func (x *Snapshot) GetPayloadManifest() string {
	if x != nil {
		return x.PayloadManifest
	}
	return ""
}

func (x *Snapshot) GetPayload() []byte {
	if x != nil {
		return x.Payload
	}
	return nil
}

func (x *Snapshot) GetTimestamp() *timestamppb.Timestamp {
	if x != nil {
		return x.Timestamp
	}
	return nil
}

// SnapshotCriteria helps load/delete snapshots
type SnapshotCriteria struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the upper bound for a selected snapshot's sequence number. Default is no upper bound, i.e the math.MaxUint64.
	MaxSequenceNumber uint64 `protobuf:"varint,1,opt,name=max_sequence_number,json=maxSequenceNumber,proto3" json:"max_sequence_number,omitempty"`
	// Specifies the upper bound for a selected snapshot's timestamp. Default is no upper bound, i.e the math.MaxUint64.
	MaxTimestamp uint64 `protobuf:"varint,2,opt,name=max_timestamp,json=maxTimestamp,proto3" json:"max_timestamp,omitempty"`
	// Specifies the lower bound for a selected snapshot's sequence number. Default is no lower bound, i.e 0L
	MinSequenceNumber uint64 `protobuf:"varint,3,opt,name=min_sequence_number,json=minSequenceNumber,proto3" json:"min_sequence_number,omitempty"`
	// Specifies the lower bound for a selected snapshot's timestamp. Default is no lower bound, i.e 0L
	MinTimestamp uint64 `protobuf:"varint,4,opt,name=min_timestamp,json=minTimestamp,proto3" json:"min_timestamp,omitempty"`
}

func (x *SnapshotCriteria) Reset() {
	*x = SnapshotCriteria{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_persistence_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *SnapshotCriteria) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SnapshotCriteria) ProtoMessage() {}

func (x *SnapshotCriteria) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_persistence_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SnapshotCriteria.ProtoReflect.Descriptor instead.
func (*SnapshotCriteria) Descriptor() ([]byte, []int) {
	return file_actors_v1_persistence_proto_rawDescGZIP(), []int{2}
}

func (x *SnapshotCriteria) GetMaxSequenceNumber() uint64 {
	if x != nil {
		return x.MaxSequenceNumber
	}
	return 0
}

func (x *SnapshotCriteria) GetMaxTimestamp() uint64 {
	if x != nil {
		return x.MaxTimestamp
	}
	return 0
}

func (x *SnapshotCriteria) GetMinSequenceNumber() uint64 {
	if x != nil {
		return x.MinSequenceNumber
	}
	return 0
}

func (x *SnapshotCriteria) GetMinTimestamp() uint64 {
	if x != nil {
		return x.MinTimestamp
	}
	return 0
}

type ErrorReply struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the error message
	Message string `protobuf:"bytes,1,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *ErrorReply) Reset() {
	*x = ErrorReply{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_persistence_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *ErrorReply) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ErrorReply) ProtoMessage() {}

func (x *ErrorReply) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_persistence_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ErrorReply.ProtoReflect.Descriptor instead.
func (*ErrorReply) Descriptor() ([]byte, []int) {
	return file_actors_v1_persistence_proto_rawDescGZIP(), []int{3}
}

func (x *ErrorReply) GetMessage() string {
	if x != nil {
		return x.Message
	}
	return ""
}

type CommandReply struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// the actual command reply
	//
	// Types that are assignable to Reply:
	//
	//	*CommandReply_State
	//	*CommandReply_Error
	//	*CommandReply_NoReply
	Reply isCommandReply_Reply `protobuf_oneof:"reply"`
}

func (x *CommandReply) Reset() {
	*x = CommandReply{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_persistence_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *CommandReply) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*CommandReply) ProtoMessage() {}

func (x *CommandReply) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_persistence_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use CommandReply.ProtoReflect.Descriptor instead.
func (*CommandReply) Descriptor() ([]byte, []int) {
	return file_actors_v1_persistence_proto_rawDescGZIP(), []int{4}
}

func (m *CommandReply) GetReply() isCommandReply_Reply {
	if m != nil {
		return m.Reply
	}
	return nil
}

func (x *CommandReply) GetState() *State {
	if x, ok := x.GetReply().(*CommandReply_State); ok {
		return x.State
	}
	return nil
}

func (x *CommandReply) GetError() *ErrorReply {
	if x, ok := x.GetReply().(*CommandReply_Error); ok {
		return x.Error
	}
	return nil
}

func (x *CommandReply) GetNoReply() *NoReply {
	if x, ok := x.GetReply().(*CommandReply_NoReply); ok {
		return x.NoReply
	}
	return nil
}

type isCommandReply_Reply interface {
	isCommandReply_Reply()
}

type CommandReply_State struct {
	// actual state is wrapped with meta data
	State *State `protobuf:"bytes,1,opt,name=state,proto3,oneof"`
}

type CommandReply_Error struct {
	// gRPC failure
	Error *ErrorReply `protobuf:"bytes,2,opt,name=error,proto3,oneof"`
}

type CommandReply_NoReply struct {
	// NoReply
	NoReply *NoReply `protobuf:"bytes,3,opt,name=no_reply,json=noReply,proto3,oneof"`
}

func (*CommandReply_State) isCommandReply_Reply() {}

func (*CommandReply_Error) isCommandReply_Reply() {}

func (*CommandReply_NoReply) isCommandReply_Reply() {}

type NoReply struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *NoReply) Reset() {
	*x = NoReply{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_persistence_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *NoReply) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*NoReply) ProtoMessage() {}

func (x *NoReply) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_persistence_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use NoReply.ProtoReflect.Descriptor instead.
func (*NoReply) Descriptor() ([]byte, []int) {
	return file_actors_v1_persistence_proto_rawDescGZIP(), []int{5}
}

// GetStateCommand tells the PersistentActor
// to reply with its latest state
type GetStateCommand struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *GetStateCommand) Reset() {
	*x = GetStateCommand{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_persistence_proto_msgTypes[6]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GetStateCommand) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetStateCommand) ProtoMessage() {}

func (x *GetStateCommand) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_persistence_proto_msgTypes[6]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetStateCommand.ProtoReflect.Descriptor instead.
func (*GetStateCommand) Descriptor() ([]byte, []int) {
	return file_actors_v1_persistence_proto_rawDescGZIP(), []int{6}
}

// Wrap the aggregate state and the meta data.
type State struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// the entity state
	State *anypb.Any `protobuf:"bytes,1,opt,name=state,proto3" json:"state,omitempty"`
	// metadata from the event that made this state
	Meta *MetaData `protobuf:"bytes,3,opt,name=meta,proto3" json:"meta,omitempty"`
}

func (x *State) Reset() {
	*x = State{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_persistence_proto_msgTypes[7]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *State) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*State) ProtoMessage() {}

func (x *State) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_persistence_proto_msgTypes[7]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use State.ProtoReflect.Descriptor instead.
func (*State) Descriptor() ([]byte, []int) {
	return file_actors_v1_persistence_proto_rawDescGZIP(), []int{7}
}

func (x *State) GetState() *anypb.Any {
	if x != nil {
		return x.State
	}
	return nil
}

func (x *State) GetMeta() *MetaData {
	if x != nil {
		return x.Meta
	}
	return nil
}

// Event is an event wrapper that holds both the
// event and the corresponding aggregate root state.
type Event struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// the event emitted
	Event *anypb.Any `protobuf:"bytes,1,opt,name=event,proto3" json:"event,omitempty"`
	// the state obtained from processing the event
	ResultingState *anypb.Any `protobuf:"bytes,2,opt,name=resulting_state,json=resultingState,proto3" json:"resulting_state,omitempty"`
	// meta data
	Meta *MetaData `protobuf:"bytes,3,opt,name=meta,proto3" json:"meta,omitempty"`
}

func (x *Event) Reset() {
	*x = Event{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_persistence_proto_msgTypes[8]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Event) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Event) ProtoMessage() {}

func (x *Event) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_persistence_proto_msgTypes[8]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Event.ProtoReflect.Descriptor instead.
func (*Event) Descriptor() ([]byte, []int) {
	return file_actors_v1_persistence_proto_rawDescGZIP(), []int{8}
}

func (x *Event) GetEvent() *anypb.Any {
	if x != nil {
		return x.Event
	}
	return nil
}

func (x *Event) GetResultingState() *anypb.Any {
	if x != nil {
		return x.ResultingState
	}
	return nil
}

func (x *Event) GetMeta() *MetaData {
	if x != nil {
		return x.Meta
	}
	return nil
}

// MetaData are additional data the enrich the aggregate state.
// This can help track information like the revision number, the last time the
// aggregate state has been modified(a.k.a revision date), etc...
type MetaData struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the persistence id
	PersitenceId string `protobuf:"bytes,1,opt,name=persitence_id,json=persitenceId,proto3" json:"persitence_id,omitempty"`
	// the revision number for the entity, increases sequentially
	// this is very useful to handle optimistic lock
	RevisionNumber uint32 `protobuf:"varint,2,opt,name=revision_number,json=revisionNumber,proto3" json:"revision_number,omitempty"`
	// the time the state has been modified
	RevisionDate *timestamppb.Timestamp `protobuf:"bytes,3,opt,name=revision_date,json=revisionDate,proto3" json:"revision_date,omitempty"`
}

func (x *MetaData) Reset() {
	*x = MetaData{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_persistence_proto_msgTypes[9]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *MetaData) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*MetaData) ProtoMessage() {}

func (x *MetaData) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_persistence_proto_msgTypes[9]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use MetaData.ProtoReflect.Descriptor instead.
func (*MetaData) Descriptor() ([]byte, []int) {
	return file_actors_v1_persistence_proto_rawDescGZIP(), []int{9}
}

func (x *MetaData) GetPersitenceId() string {
	if x != nil {
		return x.PersitenceId
	}
	return ""
}

func (x *MetaData) GetRevisionNumber() uint32 {
	if x != nil {
		return x.RevisionNumber
	}
	return 0
}

func (x *MetaData) GetRevisionDate() *timestamppb.Timestamp {
	if x != nil {
		return x.RevisionDate
	}
	return nil
}

var File_actors_v1_persistence_proto protoreflect.FileDescriptor

var file_actors_v1_persistence_proto_rawDesc = []byte{
	0x0a, 0x1b, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x2f, 0x76, 0x31, 0x2f, 0x70, 0x65, 0x72, 0x73,
	0x69, 0x73, 0x74, 0x65, 0x6e, 0x63, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x09, 0x61,
	0x63, 0x74, 0x6f, 0x72, 0x73, 0x2e, 0x76, 0x31, 0x1a, 0x19, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65,
	0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x61, 0x6e, 0x79, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x1a, 0x1f, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x62, 0x75, 0x66, 0x2f, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x2e, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x22, 0x94, 0x02, 0x0a, 0x07, 0x4a, 0x6f, 0x75, 0x72, 0x6e, 0x61, 0x6c,
	0x12, 0x25, 0x0a, 0x0e, 0x70, 0x65, 0x72, 0x73, 0x69, 0x73, 0x74, 0x65, 0x6e, 0x63, 0x65, 0x5f,
	0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0d, 0x70, 0x65, 0x72, 0x73, 0x69, 0x73,
	0x74, 0x65, 0x6e, 0x63, 0x65, 0x49, 0x64, 0x12, 0x27, 0x0a, 0x0f, 0x73, 0x65, 0x71, 0x75, 0x65,
	0x6e, 0x63, 0x65, 0x5f, 0x6e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x18, 0x02, 0x20, 0x01, 0x28, 0x04,
	0x52, 0x0e, 0x73, 0x65, 0x71, 0x75, 0x65, 0x6e, 0x63, 0x65, 0x4e, 0x75, 0x6d, 0x62, 0x65, 0x72,
	0x12, 0x1d, 0x0a, 0x0a, 0x69, 0x73, 0x5f, 0x64, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x64, 0x18, 0x03,
	0x20, 0x01, 0x28, 0x08, 0x52, 0x09, 0x69, 0x73, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x64, 0x12,
	0x29, 0x0a, 0x10, 0x70, 0x61, 0x79, 0x6c, 0x6f, 0x61, 0x64, 0x5f, 0x6d, 0x61, 0x6e, 0x69, 0x66,
	0x65, 0x73, 0x74, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0f, 0x70, 0x61, 0x79, 0x6c, 0x6f,
	0x61, 0x64, 0x4d, 0x61, 0x6e, 0x69, 0x66, 0x65, 0x73, 0x74, 0x12, 0x18, 0x0a, 0x07, 0x70, 0x61,
	0x79, 0x6c, 0x6f, 0x61, 0x64, 0x18, 0x05, 0x20, 0x01, 0x28, 0x0c, 0x52, 0x07, 0x70, 0x61, 0x79,
	0x6c, 0x6f, 0x61, 0x64, 0x12, 0x38, 0x0a, 0x09, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d,
	0x70, 0x18, 0x06, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74,
	0x61, 0x6d, 0x70, 0x52, 0x09, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x12, 0x1b,
	0x0a, 0x09, 0x77, 0x72, 0x69, 0x74, 0x65, 0x72, 0x5f, 0x69, 0x64, 0x18, 0x07, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x08, 0x77, 0x72, 0x69, 0x74, 0x65, 0x72, 0x49, 0x64, 0x22, 0xd9, 0x01, 0x0a, 0x08,
	0x53, 0x6e, 0x61, 0x70, 0x73, 0x68, 0x6f, 0x74, 0x12, 0x25, 0x0a, 0x0e, 0x70, 0x65, 0x72, 0x73,
	0x69, 0x73, 0x74, 0x65, 0x6e, 0x63, 0x65, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x0d, 0x70, 0x65, 0x72, 0x73, 0x69, 0x73, 0x74, 0x65, 0x6e, 0x63, 0x65, 0x49, 0x64, 0x12,
	0x27, 0x0a, 0x0f, 0x73, 0x65, 0x71, 0x75, 0x65, 0x6e, 0x63, 0x65, 0x5f, 0x6e, 0x75, 0x6d, 0x62,
	0x65, 0x72, 0x18, 0x02, 0x20, 0x01, 0x28, 0x04, 0x52, 0x0e, 0x73, 0x65, 0x71, 0x75, 0x65, 0x6e,
	0x63, 0x65, 0x4e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x12, 0x29, 0x0a, 0x10, 0x70, 0x61, 0x79, 0x6c,
	0x6f, 0x61, 0x64, 0x5f, 0x6d, 0x61, 0x6e, 0x69, 0x66, 0x65, 0x73, 0x74, 0x18, 0x04, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x0f, 0x70, 0x61, 0x79, 0x6c, 0x6f, 0x61, 0x64, 0x4d, 0x61, 0x6e, 0x69, 0x66,
	0x65, 0x73, 0x74, 0x12, 0x18, 0x0a, 0x07, 0x70, 0x61, 0x79, 0x6c, 0x6f, 0x61, 0x64, 0x18, 0x05,
	0x20, 0x01, 0x28, 0x0c, 0x52, 0x07, 0x70, 0x61, 0x79, 0x6c, 0x6f, 0x61, 0x64, 0x12, 0x38, 0x0a,
	0x09, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x18, 0x06, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62,
	0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x09, 0x74, 0x69,
	0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x22, 0xbc, 0x01, 0x0a, 0x10, 0x53, 0x6e, 0x61, 0x70,
	0x73, 0x68, 0x6f, 0x74, 0x43, 0x72, 0x69, 0x74, 0x65, 0x72, 0x69, 0x61, 0x12, 0x2e, 0x0a, 0x13,
	0x6d, 0x61, 0x78, 0x5f, 0x73, 0x65, 0x71, 0x75, 0x65, 0x6e, 0x63, 0x65, 0x5f, 0x6e, 0x75, 0x6d,
	0x62, 0x65, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x04, 0x52, 0x11, 0x6d, 0x61, 0x78, 0x53, 0x65,
	0x71, 0x75, 0x65, 0x6e, 0x63, 0x65, 0x4e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x12, 0x23, 0x0a, 0x0d,
	0x6d, 0x61, 0x78, 0x5f, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x04, 0x52, 0x0c, 0x6d, 0x61, 0x78, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d,
	0x70, 0x12, 0x2e, 0x0a, 0x13, 0x6d, 0x69, 0x6e, 0x5f, 0x73, 0x65, 0x71, 0x75, 0x65, 0x6e, 0x63,
	0x65, 0x5f, 0x6e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x18, 0x03, 0x20, 0x01, 0x28, 0x04, 0x52, 0x11,
	0x6d, 0x69, 0x6e, 0x53, 0x65, 0x71, 0x75, 0x65, 0x6e, 0x63, 0x65, 0x4e, 0x75, 0x6d, 0x62, 0x65,
	0x72, 0x12, 0x23, 0x0a, 0x0d, 0x6d, 0x69, 0x6e, 0x5f, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61,
	0x6d, 0x70, 0x18, 0x04, 0x20, 0x01, 0x28, 0x04, 0x52, 0x0c, 0x6d, 0x69, 0x6e, 0x54, 0x69, 0x6d,
	0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x22, 0x26, 0x0a, 0x0a, 0x45, 0x72, 0x72, 0x6f, 0x72, 0x52,
	0x65, 0x70, 0x6c, 0x79, 0x12, 0x18, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x22, 0xa1,
	0x01, 0x0a, 0x0c, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64, 0x52, 0x65, 0x70, 0x6c, 0x79, 0x12,
	0x28, 0x0a, 0x05, 0x73, 0x74, 0x61, 0x74, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x10,
	0x2e, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x2e, 0x76, 0x31, 0x2e, 0x53, 0x74, 0x61, 0x74, 0x65,
	0x48, 0x00, 0x52, 0x05, 0x73, 0x74, 0x61, 0x74, 0x65, 0x12, 0x2d, 0x0a, 0x05, 0x65, 0x72, 0x72,
	0x6f, 0x72, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x15, 0x2e, 0x61, 0x63, 0x74, 0x6f, 0x72,
	0x73, 0x2e, 0x76, 0x31, 0x2e, 0x45, 0x72, 0x72, 0x6f, 0x72, 0x52, 0x65, 0x70, 0x6c, 0x79, 0x48,
	0x00, 0x52, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x12, 0x2f, 0x0a, 0x08, 0x6e, 0x6f, 0x5f, 0x72,
	0x65, 0x70, 0x6c, 0x79, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e, 0x61, 0x63, 0x74,
	0x6f, 0x72, 0x73, 0x2e, 0x76, 0x31, 0x2e, 0x4e, 0x6f, 0x52, 0x65, 0x70, 0x6c, 0x79, 0x48, 0x00,
	0x52, 0x07, 0x6e, 0x6f, 0x52, 0x65, 0x70, 0x6c, 0x79, 0x42, 0x07, 0x0a, 0x05, 0x72, 0x65, 0x70,
	0x6c, 0x79, 0x22, 0x09, 0x0a, 0x07, 0x4e, 0x6f, 0x52, 0x65, 0x70, 0x6c, 0x79, 0x22, 0x11, 0x0a,
	0x0f, 0x47, 0x65, 0x74, 0x53, 0x74, 0x61, 0x74, 0x65, 0x43, 0x6f, 0x6d, 0x6d, 0x61, 0x6e, 0x64,
	0x22, 0x5c, 0x0a, 0x05, 0x53, 0x74, 0x61, 0x74, 0x65, 0x12, 0x2a, 0x0a, 0x05, 0x73, 0x74, 0x61,
	0x74, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c,
	0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x41, 0x6e, 0x79, 0x52, 0x05,
	0x73, 0x74, 0x61, 0x74, 0x65, 0x12, 0x27, 0x0a, 0x04, 0x6d, 0x65, 0x74, 0x61, 0x18, 0x03, 0x20,
	0x01, 0x28, 0x0b, 0x32, 0x13, 0x2e, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x2e, 0x76, 0x31, 0x2e,
	0x4d, 0x65, 0x74, 0x61, 0x44, 0x61, 0x74, 0x61, 0x52, 0x04, 0x6d, 0x65, 0x74, 0x61, 0x22, 0x9b,
	0x01, 0x0a, 0x05, 0x45, 0x76, 0x65, 0x6e, 0x74, 0x12, 0x2a, 0x0a, 0x05, 0x65, 0x76, 0x65, 0x6e,
	0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x41, 0x6e, 0x79, 0x52, 0x05, 0x65,
	0x76, 0x65, 0x6e, 0x74, 0x12, 0x3d, 0x0a, 0x0f, 0x72, 0x65, 0x73, 0x75, 0x6c, 0x74, 0x69, 0x6e,
	0x67, 0x5f, 0x73, 0x74, 0x61, 0x74, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e,
	0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e,
	0x41, 0x6e, 0x79, 0x52, 0x0e, 0x72, 0x65, 0x73, 0x75, 0x6c, 0x74, 0x69, 0x6e, 0x67, 0x53, 0x74,
	0x61, 0x74, 0x65, 0x12, 0x27, 0x0a, 0x04, 0x6d, 0x65, 0x74, 0x61, 0x18, 0x03, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x13, 0x2e, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x2e, 0x76, 0x31, 0x2e, 0x4d, 0x65,
	0x74, 0x61, 0x44, 0x61, 0x74, 0x61, 0x52, 0x04, 0x6d, 0x65, 0x74, 0x61, 0x22, 0x99, 0x01, 0x0a,
	0x08, 0x4d, 0x65, 0x74, 0x61, 0x44, 0x61, 0x74, 0x61, 0x12, 0x23, 0x0a, 0x0d, 0x70, 0x65, 0x72,
	0x73, 0x69, 0x74, 0x65, 0x6e, 0x63, 0x65, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x0c, 0x70, 0x65, 0x72, 0x73, 0x69, 0x74, 0x65, 0x6e, 0x63, 0x65, 0x49, 0x64, 0x12, 0x27,
	0x0a, 0x0f, 0x72, 0x65, 0x76, 0x69, 0x73, 0x69, 0x6f, 0x6e, 0x5f, 0x6e, 0x75, 0x6d, 0x62, 0x65,
	0x72, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0d, 0x52, 0x0e, 0x72, 0x65, 0x76, 0x69, 0x73, 0x69, 0x6f,
	0x6e, 0x4e, 0x75, 0x6d, 0x62, 0x65, 0x72, 0x12, 0x3f, 0x0a, 0x0d, 0x72, 0x65, 0x76, 0x69, 0x73,
	0x69, 0x6f, 0x6e, 0x5f, 0x64, 0x61, 0x74, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a,
	0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66,
	0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x0c, 0x72, 0x65, 0x76, 0x69,
	0x73, 0x69, 0x6f, 0x6e, 0x44, 0x61, 0x74, 0x65, 0x42, 0x9a, 0x01, 0x0a, 0x0d, 0x63, 0x6f, 0x6d,
	0x2e, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x2e, 0x76, 0x31, 0x42, 0x10, 0x50, 0x65, 0x72, 0x73,
	0x69, 0x73, 0x74, 0x65, 0x6e, 0x63, 0x65, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x48, 0x02, 0x50, 0x01,
	0x5a, 0x30, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x74, 0x6f, 0x63,
	0x68, 0x65, 0x6d, 0x65, 0x79, 0x2f, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2f, 0x67, 0x65, 0x6e, 0x2f,
	0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x2f, 0x76, 0x31, 0x3b, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73,
	0x76, 0x31, 0xa2, 0x02, 0x03, 0x41, 0x58, 0x58, 0xaa, 0x02, 0x09, 0x41, 0x63, 0x74, 0x6f, 0x72,
	0x73, 0x2e, 0x56, 0x31, 0xca, 0x02, 0x09, 0x41, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x5c, 0x56, 0x31,
	0xe2, 0x02, 0x15, 0x41, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x5c, 0x56, 0x31, 0x5c, 0x47, 0x50, 0x42,
	0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x0a, 0x41, 0x63, 0x74, 0x6f, 0x72,
	0x73, 0x3a, 0x3a, 0x56, 0x31, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_actors_v1_persistence_proto_rawDescOnce sync.Once
	file_actors_v1_persistence_proto_rawDescData = file_actors_v1_persistence_proto_rawDesc
)

func file_actors_v1_persistence_proto_rawDescGZIP() []byte {
	file_actors_v1_persistence_proto_rawDescOnce.Do(func() {
		file_actors_v1_persistence_proto_rawDescData = protoimpl.X.CompressGZIP(file_actors_v1_persistence_proto_rawDescData)
	})
	return file_actors_v1_persistence_proto_rawDescData
}

var file_actors_v1_persistence_proto_msgTypes = make([]protoimpl.MessageInfo, 10)
var file_actors_v1_persistence_proto_goTypes = []interface{}{
	(*Journal)(nil),               // 0: actors.v1.Journal
	(*Snapshot)(nil),              // 1: actors.v1.Snapshot
	(*SnapshotCriteria)(nil),      // 2: actors.v1.SnapshotCriteria
	(*ErrorReply)(nil),            // 3: actors.v1.ErrorReply
	(*CommandReply)(nil),          // 4: actors.v1.CommandReply
	(*NoReply)(nil),               // 5: actors.v1.NoReply
	(*GetStateCommand)(nil),       // 6: actors.v1.GetStateCommand
	(*State)(nil),                 // 7: actors.v1.State
	(*Event)(nil),                 // 8: actors.v1.Event
	(*MetaData)(nil),              // 9: actors.v1.MetaData
	(*timestamppb.Timestamp)(nil), // 10: google.protobuf.Timestamp
	(*anypb.Any)(nil),             // 11: google.protobuf.Any
}
var file_actors_v1_persistence_proto_depIdxs = []int32{
	10, // 0: actors.v1.Journal.timestamp:type_name -> google.protobuf.Timestamp
	10, // 1: actors.v1.Snapshot.timestamp:type_name -> google.protobuf.Timestamp
	7,  // 2: actors.v1.CommandReply.state:type_name -> actors.v1.State
	3,  // 3: actors.v1.CommandReply.error:type_name -> actors.v1.ErrorReply
	5,  // 4: actors.v1.CommandReply.no_reply:type_name -> actors.v1.NoReply
	11, // 5: actors.v1.State.state:type_name -> google.protobuf.Any
	9,  // 6: actors.v1.State.meta:type_name -> actors.v1.MetaData
	11, // 7: actors.v1.Event.event:type_name -> google.protobuf.Any
	11, // 8: actors.v1.Event.resulting_state:type_name -> google.protobuf.Any
	9,  // 9: actors.v1.Event.meta:type_name -> actors.v1.MetaData
	10, // 10: actors.v1.MetaData.revision_date:type_name -> google.protobuf.Timestamp
	11, // [11:11] is the sub-list for method output_type
	11, // [11:11] is the sub-list for method input_type
	11, // [11:11] is the sub-list for extension type_name
	11, // [11:11] is the sub-list for extension extendee
	0,  // [0:11] is the sub-list for field type_name
}

func init() { file_actors_v1_persistence_proto_init() }
func file_actors_v1_persistence_proto_init() {
	if File_actors_v1_persistence_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_actors_v1_persistence_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Journal); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_actors_v1_persistence_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Snapshot); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_actors_v1_persistence_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*SnapshotCriteria); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_actors_v1_persistence_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*ErrorReply); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_actors_v1_persistence_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*CommandReply); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_actors_v1_persistence_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*NoReply); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_actors_v1_persistence_proto_msgTypes[6].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GetStateCommand); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_actors_v1_persistence_proto_msgTypes[7].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*State); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_actors_v1_persistence_proto_msgTypes[8].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Event); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_actors_v1_persistence_proto_msgTypes[9].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*MetaData); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	file_actors_v1_persistence_proto_msgTypes[4].OneofWrappers = []interface{}{
		(*CommandReply_State)(nil),
		(*CommandReply_Error)(nil),
		(*CommandReply_NoReply)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_actors_v1_persistence_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   10,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_actors_v1_persistence_proto_goTypes,
		DependencyIndexes: file_actors_v1_persistence_proto_depIdxs,
		MessageInfos:      file_actors_v1_persistence_proto_msgTypes,
	}.Build()
	File_actors_v1_persistence_proto = out.File
	file_actors_v1_persistence_proto_rawDesc = nil
	file_actors_v1_persistence_proto_goTypes = nil
	file_actors_v1_persistence_proto_depIdxs = nil
}
