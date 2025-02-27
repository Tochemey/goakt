// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.5
// 	protoc        (unknown)
// source: goakt/goakt.proto

package goaktpb

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	anypb "google.golang.org/protobuf/types/known/anypb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	reflect "reflect"
	sync "sync"
	unsafe "unsafe"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// Address represents an actor address
type Address struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the remote host address
	Host string `protobuf:"bytes,1,opt,name=host,proto3" json:"host,omitempty"`
	// Specifies the remote port
	Port int32 `protobuf:"varint,2,opt,name=port,proto3" json:"port,omitempty"`
	// Specifies the actor name
	Name string `protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`
	// Specifies the actor unique id
	Id string `protobuf:"bytes,4,opt,name=id,proto3" json:"id,omitempty"`
	// Specifies the actor system
	System string `protobuf:"bytes,5,opt,name=system,proto3" json:"system,omitempty"`
	// Specifies the parent address
	Parent        *Address `protobuf:"bytes,6,opt,name=parent,proto3" json:"parent,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Address) Reset() {
	*x = Address{}
	mi := &file_goakt_goakt_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Address) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Address) ProtoMessage() {}

func (x *Address) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Address.ProtoReflect.Descriptor instead.
func (*Address) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{0}
}

func (x *Address) GetHost() string {
	if x != nil {
		return x.Host
	}
	return ""
}

func (x *Address) GetPort() int32 {
	if x != nil {
		return x.Port
	}
	return 0
}

func (x *Address) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Address) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *Address) GetSystem() string {
	if x != nil {
		return x.System
	}
	return ""
}

func (x *Address) GetParent() *Address {
	if x != nil {
		return x.Parent
	}
	return nil
}

// Deadletter defines the deadletter event
type Deadletter struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the sender's address
	Sender *Address `protobuf:"bytes,1,opt,name=sender,proto3" json:"sender,omitempty"`
	// Specifies the actor address
	Receiver *Address `protobuf:"bytes,2,opt,name=receiver,proto3" json:"receiver,omitempty"`
	// Specifies the message to send to the actor
	// Any proto message is allowed to be sent
	Message *anypb.Any `protobuf:"bytes,3,opt,name=message,proto3" json:"message,omitempty"`
	// Specifies the message send time
	SendTime *timestamppb.Timestamp `protobuf:"bytes,4,opt,name=send_time,json=sendTime,proto3" json:"send_time,omitempty"`
	// Specifies the reason why the deadletter
	Reason        string `protobuf:"bytes,5,opt,name=reason,proto3" json:"reason,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Deadletter) Reset() {
	*x = Deadletter{}
	mi := &file_goakt_goakt_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Deadletter) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Deadletter) ProtoMessage() {}

func (x *Deadletter) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Deadletter.ProtoReflect.Descriptor instead.
func (*Deadletter) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{1}
}

func (x *Deadletter) GetSender() *Address {
	if x != nil {
		return x.Sender
	}
	return nil
}

func (x *Deadletter) GetReceiver() *Address {
	if x != nil {
		return x.Receiver
	}
	return nil
}

func (x *Deadletter) GetMessage() *anypb.Any {
	if x != nil {
		return x.Message
	}
	return nil
}

func (x *Deadletter) GetSendTime() *timestamppb.Timestamp {
	if x != nil {
		return x.SendTime
	}
	return nil
}

func (x *Deadletter) GetReason() string {
	if x != nil {
		return x.Reason
	}
	return ""
}

// ActorStarted defines the actor started event
type ActorStarted struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the actor address
	Address *Address `protobuf:"bytes,1,opt,name=address,proto3" json:"address,omitempty"`
	// Specifies the started time
	StartedAt     *timestamppb.Timestamp `protobuf:"bytes,2,opt,name=started_at,json=startedAt,proto3" json:"started_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ActorStarted) Reset() {
	*x = ActorStarted{}
	mi := &file_goakt_goakt_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ActorStarted) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ActorStarted) ProtoMessage() {}

func (x *ActorStarted) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ActorStarted.ProtoReflect.Descriptor instead.
func (*ActorStarted) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{2}
}

func (x *ActorStarted) GetAddress() *Address {
	if x != nil {
		return x.Address
	}
	return nil
}

func (x *ActorStarted) GetStartedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.StartedAt
	}
	return nil
}

// ActorStopped defines the actor stopped event
type ActorStopped struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the actor address
	Address *Address `protobuf:"bytes,1,opt,name=address,proto3" json:"address,omitempty"`
	// Specifies the stop time
	StoppedAt     *timestamppb.Timestamp `protobuf:"bytes,2,opt,name=stopped_at,json=stoppedAt,proto3" json:"stopped_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ActorStopped) Reset() {
	*x = ActorStopped{}
	mi := &file_goakt_goakt_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ActorStopped) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ActorStopped) ProtoMessage() {}

func (x *ActorStopped) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ActorStopped.ProtoReflect.Descriptor instead.
func (*ActorStopped) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{3}
}

func (x *ActorStopped) GetAddress() *Address {
	if x != nil {
		return x.Address
	}
	return nil
}

func (x *ActorStopped) GetStoppedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.StoppedAt
	}
	return nil
}

// ActorPassivated define the actor passivated event
type ActorPassivated struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the actor address
	Address *Address `protobuf:"bytes,1,opt,name=address,proto3" json:"address,omitempty"`
	// Specifies the passivation time
	PassivatedAt  *timestamppb.Timestamp `protobuf:"bytes,2,opt,name=passivated_at,json=passivatedAt,proto3" json:"passivated_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ActorPassivated) Reset() {
	*x = ActorPassivated{}
	mi := &file_goakt_goakt_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ActorPassivated) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ActorPassivated) ProtoMessage() {}

func (x *ActorPassivated) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ActorPassivated.ProtoReflect.Descriptor instead.
func (*ActorPassivated) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{4}
}

func (x *ActorPassivated) GetAddress() *Address {
	if x != nil {
		return x.Address
	}
	return nil
}

func (x *ActorPassivated) GetPassivatedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.PassivatedAt
	}
	return nil
}

// ActorChildCreated defines the child actor created event
type ActorChildCreated struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the actor address
	Address *Address `protobuf:"bytes,1,opt,name=address,proto3" json:"address,omitempty"`
	// Specifies the parent address
	Parent *Address `protobuf:"bytes,2,opt,name=parent,proto3" json:"parent,omitempty"`
	// Specifies the started time
	CreatedAt     *timestamppb.Timestamp `protobuf:"bytes,3,opt,name=created_at,json=createdAt,proto3" json:"created_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ActorChildCreated) Reset() {
	*x = ActorChildCreated{}
	mi := &file_goakt_goakt_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ActorChildCreated) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ActorChildCreated) ProtoMessage() {}

func (x *ActorChildCreated) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ActorChildCreated.ProtoReflect.Descriptor instead.
func (*ActorChildCreated) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{5}
}

func (x *ActorChildCreated) GetAddress() *Address {
	if x != nil {
		return x.Address
	}
	return nil
}

func (x *ActorChildCreated) GetParent() *Address {
	if x != nil {
		return x.Parent
	}
	return nil
}

func (x *ActorChildCreated) GetCreatedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.CreatedAt
	}
	return nil
}

// ActorRestarted defines the actor restarted event
type ActorRestarted struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the actor address
	Address *Address `protobuf:"bytes,1,opt,name=address,proto3" json:"address,omitempty"`
	// Specifies the restarted time
	RestartedAt   *timestamppb.Timestamp `protobuf:"bytes,2,opt,name=restarted_at,json=restartedAt,proto3" json:"restarted_at,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ActorRestarted) Reset() {
	*x = ActorRestarted{}
	mi := &file_goakt_goakt_proto_msgTypes[6]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ActorRestarted) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ActorRestarted) ProtoMessage() {}

func (x *ActorRestarted) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[6]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ActorRestarted.ProtoReflect.Descriptor instead.
func (*ActorRestarted) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{6}
}

func (x *ActorRestarted) GetAddress() *Address {
	if x != nil {
		return x.Address
	}
	return nil
}

func (x *ActorRestarted) GetRestartedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.RestartedAt
	}
	return nil
}

// ActorSuspended defines the actor suspended event
type ActorSuspended struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the actor address
	Address *Address `protobuf:"bytes,1,opt,name=address,proto3" json:"address,omitempty"`
	// Specifies the suspended time
	SuspendedAt *timestamppb.Timestamp `protobuf:"bytes,2,opt,name=suspended_at,json=suspendedAt,proto3" json:"suspended_at,omitempty"`
	// Specifies the suspension reason
	Reason        string `protobuf:"bytes,3,opt,name=reason,proto3" json:"reason,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ActorSuspended) Reset() {
	*x = ActorSuspended{}
	mi := &file_goakt_goakt_proto_msgTypes[7]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ActorSuspended) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ActorSuspended) ProtoMessage() {}

func (x *ActorSuspended) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[7]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ActorSuspended.ProtoReflect.Descriptor instead.
func (*ActorSuspended) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{7}
}

func (x *ActorSuspended) GetAddress() *Address {
	if x != nil {
		return x.Address
	}
	return nil
}

func (x *ActorSuspended) GetSuspendedAt() *timestamppb.Timestamp {
	if x != nil {
		return x.SuspendedAt
	}
	return nil
}

func (x *ActorSuspended) GetReason() string {
	if x != nil {
		return x.Reason
	}
	return ""
}

// NodeJoined defines the node joined event
type NodeJoined struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the node address
	Address string `protobuf:"bytes,1,opt,name=address,proto3" json:"address,omitempty"`
	// Specifies the timestamp
	Timestamp     *timestamppb.Timestamp `protobuf:"bytes,2,opt,name=timestamp,proto3" json:"timestamp,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *NodeJoined) Reset() {
	*x = NodeJoined{}
	mi := &file_goakt_goakt_proto_msgTypes[8]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *NodeJoined) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*NodeJoined) ProtoMessage() {}

func (x *NodeJoined) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[8]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use NodeJoined.ProtoReflect.Descriptor instead.
func (*NodeJoined) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{8}
}

func (x *NodeJoined) GetAddress() string {
	if x != nil {
		return x.Address
	}
	return ""
}

func (x *NodeJoined) GetTimestamp() *timestamppb.Timestamp {
	if x != nil {
		return x.Timestamp
	}
	return nil
}

// NodeLeft defines the node left event
type NodeLeft struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the node address
	Address string `protobuf:"bytes,1,opt,name=address,proto3" json:"address,omitempty"`
	// Specifies the timestamp
	Timestamp     *timestamppb.Timestamp `protobuf:"bytes,2,opt,name=timestamp,proto3" json:"timestamp,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *NodeLeft) Reset() {
	*x = NodeLeft{}
	mi := &file_goakt_goakt_proto_msgTypes[9]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *NodeLeft) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*NodeLeft) ProtoMessage() {}

func (x *NodeLeft) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[9]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use NodeLeft.ProtoReflect.Descriptor instead.
func (*NodeLeft) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{9}
}

func (x *NodeLeft) GetAddress() string {
	if x != nil {
		return x.Address
	}
	return ""
}

func (x *NodeLeft) GetTimestamp() *timestamppb.Timestamp {
	if x != nil {
		return x.Timestamp
	}
	return nil
}

// Terminated is used to notify watching actors
// of the shutdown of its child actor.
type Terminated struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the terminated actor
	ActorId       string `protobuf:"bytes,1,opt,name=actor_id,json=actorId,proto3" json:"actor_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Terminated) Reset() {
	*x = Terminated{}
	mi := &file_goakt_goakt_proto_msgTypes[10]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Terminated) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Terminated) ProtoMessage() {}

func (x *Terminated) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[10]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Terminated.ProtoReflect.Descriptor instead.
func (*Terminated) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{10}
}

func (x *Terminated) GetActorId() string {
	if x != nil {
		return x.ActorId
	}
	return ""
}

// PoisonPill is sent the stop an actor.
// It is enqueued as ordinary messages.
// It will be handled after messages that were already queued in the mailbox.
type PoisonPill struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PoisonPill) Reset() {
	*x = PoisonPill{}
	mi := &file_goakt_goakt_proto_msgTypes[11]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PoisonPill) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PoisonPill) ProtoMessage() {}

func (x *PoisonPill) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[11]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PoisonPill.ProtoReflect.Descriptor instead.
func (*PoisonPill) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{11}
}

// PostStart is used when an actor has successfully started
type PostStart struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PostStart) Reset() {
	*x = PostStart{}
	mi := &file_goakt_goakt_proto_msgTypes[12]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PostStart) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PostStart) ProtoMessage() {}

func (x *PostStart) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[12]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PostStart.ProtoReflect.Descriptor instead.
func (*PostStart) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{12}
}

// Broadcast is used to send message to a router
type Broadcast struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the actual message
	Message       *anypb.Any `protobuf:"bytes,1,opt,name=message,proto3" json:"message,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Broadcast) Reset() {
	*x = Broadcast{}
	mi := &file_goakt_goakt_proto_msgTypes[13]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Broadcast) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Broadcast) ProtoMessage() {}

func (x *Broadcast) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_goakt_proto_msgTypes[13]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Broadcast.ProtoReflect.Descriptor instead.
func (*Broadcast) Descriptor() ([]byte, []int) {
	return file_goakt_goakt_proto_rawDescGZIP(), []int{13}
}

func (x *Broadcast) GetMessage() *anypb.Any {
	if x != nil {
		return x.Message
	}
	return nil
}

var File_goakt_goakt_proto protoreflect.FileDescriptor

var file_goakt_goakt_proto_rawDesc = string([]byte{
	0x0a, 0x11, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2f, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x12, 0x07, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x70, 0x62, 0x1a, 0x19, 0x67, 0x6f,
	0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x61, 0x6e,
	0x79, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x1f, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61,
	0x6d, 0x70, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x97, 0x01, 0x0a, 0x07, 0x41, 0x64, 0x64,
	0x72, 0x65, 0x73, 0x73, 0x12, 0x12, 0x0a, 0x04, 0x68, 0x6f, 0x73, 0x74, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x04, 0x68, 0x6f, 0x73, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x70, 0x6f, 0x72, 0x74,
	0x18, 0x02, 0x20, 0x01, 0x28, 0x05, 0x52, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x12, 0x12, 0x0a, 0x04,
	0x6e, 0x61, 0x6d, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65,
	0x12, 0x0e, 0x0a, 0x02, 0x69, 0x64, 0x18, 0x04, 0x20, 0x01, 0x28, 0x09, 0x52, 0x02, 0x69, 0x64,
	0x12, 0x16, 0x0a, 0x06, 0x73, 0x79, 0x73, 0x74, 0x65, 0x6d, 0x18, 0x05, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x06, 0x73, 0x79, 0x73, 0x74, 0x65, 0x6d, 0x12, 0x28, 0x0a, 0x06, 0x70, 0x61, 0x72, 0x65,
	0x6e, 0x74, 0x18, 0x06, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x10, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74,
	0x70, 0x62, 0x2e, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x52, 0x06, 0x70, 0x61, 0x72, 0x65,
	0x6e, 0x74, 0x22, 0xe5, 0x01, 0x0a, 0x0a, 0x44, 0x65, 0x61, 0x64, 0x6c, 0x65, 0x74, 0x74, 0x65,
	0x72, 0x12, 0x28, 0x0a, 0x06, 0x73, 0x65, 0x6e, 0x64, 0x65, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x10, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x70, 0x62, 0x2e, 0x41, 0x64, 0x64, 0x72,
	0x65, 0x73, 0x73, 0x52, 0x06, 0x73, 0x65, 0x6e, 0x64, 0x65, 0x72, 0x12, 0x2c, 0x0a, 0x08, 0x72,
	0x65, 0x63, 0x65, 0x69, 0x76, 0x65, 0x72, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x10, 0x2e,
	0x67, 0x6f, 0x61, 0x6b, 0x74, 0x70, 0x62, 0x2e, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x52,
	0x08, 0x72, 0x65, 0x63, 0x65, 0x69, 0x76, 0x65, 0x72, 0x12, 0x2e, 0x0a, 0x07, 0x6d, 0x65, 0x73,
	0x73, 0x61, 0x67, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x67, 0x6f, 0x6f,
	0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x41, 0x6e, 0x79,
	0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x12, 0x37, 0x0a, 0x09, 0x73, 0x65, 0x6e,
	0x64, 0x5f, 0x74, 0x69, 0x6d, 0x65, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67,
	0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54,
	0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x08, 0x73, 0x65, 0x6e, 0x64, 0x54, 0x69,
	0x6d, 0x65, 0x12, 0x16, 0x0a, 0x06, 0x72, 0x65, 0x61, 0x73, 0x6f, 0x6e, 0x18, 0x05, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x06, 0x72, 0x65, 0x61, 0x73, 0x6f, 0x6e, 0x22, 0x75, 0x0a, 0x0c, 0x41, 0x63,
	0x74, 0x6f, 0x72, 0x53, 0x74, 0x61, 0x72, 0x74, 0x65, 0x64, 0x12, 0x2a, 0x0a, 0x07, 0x61, 0x64,
	0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x10, 0x2e, 0x67, 0x6f,
	0x61, 0x6b, 0x74, 0x70, 0x62, 0x2e, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x52, 0x07, 0x61,
	0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x39, 0x0a, 0x0a, 0x73, 0x74, 0x61, 0x72, 0x74, 0x65,
	0x64, 0x5f, 0x61, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f,
	0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d,
	0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x09, 0x73, 0x74, 0x61, 0x72, 0x74, 0x65, 0x64, 0x41,
	0x74, 0x22, 0x75, 0x0a, 0x0c, 0x41, 0x63, 0x74, 0x6f, 0x72, 0x53, 0x74, 0x6f, 0x70, 0x70, 0x65,
	0x64, 0x12, 0x2a, 0x0a, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x10, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x70, 0x62, 0x2e, 0x41, 0x64, 0x64,
	0x72, 0x65, 0x73, 0x73, 0x52, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x39, 0x0a,
	0x0a, 0x73, 0x74, 0x6f, 0x70, 0x70, 0x65, 0x64, 0x5f, 0x61, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x62, 0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x09, 0x73,
	0x74, 0x6f, 0x70, 0x70, 0x65, 0x64, 0x41, 0x74, 0x22, 0x7e, 0x0a, 0x0f, 0x41, 0x63, 0x74, 0x6f,
	0x72, 0x50, 0x61, 0x73, 0x73, 0x69, 0x76, 0x61, 0x74, 0x65, 0x64, 0x12, 0x2a, 0x0a, 0x07, 0x61,
	0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x10, 0x2e, 0x67,
	0x6f, 0x61, 0x6b, 0x74, 0x70, 0x62, 0x2e, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x52, 0x07,
	0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x3f, 0x0a, 0x0d, 0x70, 0x61, 0x73, 0x73, 0x69,
	0x76, 0x61, 0x74, 0x65, 0x64, 0x5f, 0x61, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a,
	0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66,
	0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x0c, 0x70, 0x61, 0x73, 0x73,
	0x69, 0x76, 0x61, 0x74, 0x65, 0x64, 0x41, 0x74, 0x22, 0xa4, 0x01, 0x0a, 0x11, 0x41, 0x63, 0x74,
	0x6f, 0x72, 0x43, 0x68, 0x69, 0x6c, 0x64, 0x43, 0x72, 0x65, 0x61, 0x74, 0x65, 0x64, 0x12, 0x2a,
	0x0a, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32,
	0x10, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x70, 0x62, 0x2e, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73,
	0x73, 0x52, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x28, 0x0a, 0x06, 0x70, 0x61,
	0x72, 0x65, 0x6e, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x10, 0x2e, 0x67, 0x6f, 0x61,
	0x6b, 0x74, 0x70, 0x62, 0x2e, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x52, 0x06, 0x70, 0x61,
	0x72, 0x65, 0x6e, 0x74, 0x12, 0x39, 0x0a, 0x0a, 0x63, 0x72, 0x65, 0x61, 0x74, 0x65, 0x64, 0x5f,
	0x61, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c,
	0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73,
	0x74, 0x61, 0x6d, 0x70, 0x52, 0x09, 0x63, 0x72, 0x65, 0x61, 0x74, 0x65, 0x64, 0x41, 0x74, 0x22,
	0x7b, 0x0a, 0x0e, 0x41, 0x63, 0x74, 0x6f, 0x72, 0x52, 0x65, 0x73, 0x74, 0x61, 0x72, 0x74, 0x65,
	0x64, 0x12, 0x2a, 0x0a, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x10, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x70, 0x62, 0x2e, 0x41, 0x64, 0x64,
	0x72, 0x65, 0x73, 0x73, 0x52, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x3d, 0x0a,
	0x0c, 0x72, 0x65, 0x73, 0x74, 0x61, 0x72, 0x74, 0x65, 0x64, 0x5f, 0x61, 0x74, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52,
	0x0b, 0x72, 0x65, 0x73, 0x74, 0x61, 0x72, 0x74, 0x65, 0x64, 0x41, 0x74, 0x22, 0x93, 0x01, 0x0a,
	0x0e, 0x41, 0x63, 0x74, 0x6f, 0x72, 0x53, 0x75, 0x73, 0x70, 0x65, 0x6e, 0x64, 0x65, 0x64, 0x12,
	0x2a, 0x0a, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x10, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x70, 0x62, 0x2e, 0x41, 0x64, 0x64, 0x72, 0x65,
	0x73, 0x73, 0x52, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x3d, 0x0a, 0x0c, 0x73,
	0x75, 0x73, 0x70, 0x65, 0x6e, 0x64, 0x65, 0x64, 0x5f, 0x61, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x62, 0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x0b, 0x73,
	0x75, 0x73, 0x70, 0x65, 0x6e, 0x64, 0x65, 0x64, 0x41, 0x74, 0x12, 0x16, 0x0a, 0x06, 0x72, 0x65,
	0x61, 0x73, 0x6f, 0x6e, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x72, 0x65, 0x61, 0x73,
	0x6f, 0x6e, 0x22, 0x60, 0x0a, 0x0a, 0x4e, 0x6f, 0x64, 0x65, 0x4a, 0x6f, 0x69, 0x6e, 0x65, 0x64,
	0x12, 0x18, 0x0a, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x38, 0x0a, 0x09, 0x74, 0x69,
	0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e,
	0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e,
	0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x09, 0x74, 0x69, 0x6d, 0x65, 0x73,
	0x74, 0x61, 0x6d, 0x70, 0x22, 0x5e, 0x0a, 0x08, 0x4e, 0x6f, 0x64, 0x65, 0x4c, 0x65, 0x66, 0x74,
	0x12, 0x18, 0x0a, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x38, 0x0a, 0x09, 0x74, 0x69,
	0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e,
	0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e,
	0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52, 0x09, 0x74, 0x69, 0x6d, 0x65, 0x73,
	0x74, 0x61, 0x6d, 0x70, 0x22, 0x27, 0x0a, 0x0a, 0x54, 0x65, 0x72, 0x6d, 0x69, 0x6e, 0x61, 0x74,
	0x65, 0x64, 0x12, 0x19, 0x0a, 0x08, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x5f, 0x69, 0x64, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x49, 0x64, 0x22, 0x0c, 0x0a,
	0x0a, 0x50, 0x6f, 0x69, 0x73, 0x6f, 0x6e, 0x50, 0x69, 0x6c, 0x6c, 0x22, 0x0b, 0x0a, 0x09, 0x50,
	0x6f, 0x73, 0x74, 0x53, 0x74, 0x61, 0x72, 0x74, 0x22, 0x3b, 0x0a, 0x09, 0x42, 0x72, 0x6f, 0x61,
	0x64, 0x63, 0x61, 0x73, 0x74, 0x12, 0x2e, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x41, 0x6e, 0x79, 0x52, 0x07, 0x6d, 0x65,
	0x73, 0x73, 0x61, 0x67, 0x65, 0x42, 0x85, 0x01, 0x0a, 0x0b, 0x63, 0x6f, 0x6d, 0x2e, 0x67, 0x6f,
	0x61, 0x6b, 0x74, 0x70, 0x62, 0x42, 0x0a, 0x47, 0x6f, 0x61, 0x6b, 0x74, 0x50, 0x72, 0x6f, 0x74,
	0x6f, 0x48, 0x02, 0x50, 0x01, 0x5a, 0x2c, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f,
	0x6d, 0x2f, 0x74, 0x6f, 0x63, 0x68, 0x65, 0x6d, 0x65, 0x79, 0x2f, 0x67, 0x6f, 0x61, 0x6b, 0x74,
	0x2f, 0x76, 0x33, 0x2f, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x70, 0x62, 0x3b, 0x67, 0x6f, 0x61, 0x6b,
	0x74, 0x70, 0x62, 0xa2, 0x02, 0x03, 0x47, 0x58, 0x58, 0xaa, 0x02, 0x07, 0x47, 0x6f, 0x61, 0x6b,
	0x74, 0x70, 0x62, 0xca, 0x02, 0x07, 0x47, 0x6f, 0x61, 0x6b, 0x74, 0x70, 0x62, 0xe2, 0x02, 0x13,
	0x47, 0x6f, 0x61, 0x6b, 0x74, 0x70, 0x62, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64,
	0x61, 0x74, 0x61, 0xea, 0x02, 0x07, 0x47, 0x6f, 0x61, 0x6b, 0x74, 0x70, 0x62, 0x62, 0x06, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x33,
})

var (
	file_goakt_goakt_proto_rawDescOnce sync.Once
	file_goakt_goakt_proto_rawDescData []byte
)

func file_goakt_goakt_proto_rawDescGZIP() []byte {
	file_goakt_goakt_proto_rawDescOnce.Do(func() {
		file_goakt_goakt_proto_rawDescData = protoimpl.X.CompressGZIP(unsafe.Slice(unsafe.StringData(file_goakt_goakt_proto_rawDesc), len(file_goakt_goakt_proto_rawDesc)))
	})
	return file_goakt_goakt_proto_rawDescData
}

var file_goakt_goakt_proto_msgTypes = make([]protoimpl.MessageInfo, 14)
var file_goakt_goakt_proto_goTypes = []any{
	(*Address)(nil),               // 0: goaktpb.Address
	(*Deadletter)(nil),            // 1: goaktpb.Deadletter
	(*ActorStarted)(nil),          // 2: goaktpb.ActorStarted
	(*ActorStopped)(nil),          // 3: goaktpb.ActorStopped
	(*ActorPassivated)(nil),       // 4: goaktpb.ActorPassivated
	(*ActorChildCreated)(nil),     // 5: goaktpb.ActorChildCreated
	(*ActorRestarted)(nil),        // 6: goaktpb.ActorRestarted
	(*ActorSuspended)(nil),        // 7: goaktpb.ActorSuspended
	(*NodeJoined)(nil),            // 8: goaktpb.NodeJoined
	(*NodeLeft)(nil),              // 9: goaktpb.NodeLeft
	(*Terminated)(nil),            // 10: goaktpb.Terminated
	(*PoisonPill)(nil),            // 11: goaktpb.PoisonPill
	(*PostStart)(nil),             // 12: goaktpb.PostStart
	(*Broadcast)(nil),             // 13: goaktpb.Broadcast
	(*anypb.Any)(nil),             // 14: google.protobuf.Any
	(*timestamppb.Timestamp)(nil), // 15: google.protobuf.Timestamp
}
var file_goakt_goakt_proto_depIdxs = []int32{
	0,  // 0: goaktpb.Address.parent:type_name -> goaktpb.Address
	0,  // 1: goaktpb.Deadletter.sender:type_name -> goaktpb.Address
	0,  // 2: goaktpb.Deadletter.receiver:type_name -> goaktpb.Address
	14, // 3: goaktpb.Deadletter.message:type_name -> google.protobuf.Any
	15, // 4: goaktpb.Deadletter.send_time:type_name -> google.protobuf.Timestamp
	0,  // 5: goaktpb.ActorStarted.address:type_name -> goaktpb.Address
	15, // 6: goaktpb.ActorStarted.started_at:type_name -> google.protobuf.Timestamp
	0,  // 7: goaktpb.ActorStopped.address:type_name -> goaktpb.Address
	15, // 8: goaktpb.ActorStopped.stopped_at:type_name -> google.protobuf.Timestamp
	0,  // 9: goaktpb.ActorPassivated.address:type_name -> goaktpb.Address
	15, // 10: goaktpb.ActorPassivated.passivated_at:type_name -> google.protobuf.Timestamp
	0,  // 11: goaktpb.ActorChildCreated.address:type_name -> goaktpb.Address
	0,  // 12: goaktpb.ActorChildCreated.parent:type_name -> goaktpb.Address
	15, // 13: goaktpb.ActorChildCreated.created_at:type_name -> google.protobuf.Timestamp
	0,  // 14: goaktpb.ActorRestarted.address:type_name -> goaktpb.Address
	15, // 15: goaktpb.ActorRestarted.restarted_at:type_name -> google.protobuf.Timestamp
	0,  // 16: goaktpb.ActorSuspended.address:type_name -> goaktpb.Address
	15, // 17: goaktpb.ActorSuspended.suspended_at:type_name -> google.protobuf.Timestamp
	15, // 18: goaktpb.NodeJoined.timestamp:type_name -> google.protobuf.Timestamp
	15, // 19: goaktpb.NodeLeft.timestamp:type_name -> google.protobuf.Timestamp
	14, // 20: goaktpb.Broadcast.message:type_name -> google.protobuf.Any
	21, // [21:21] is the sub-list for method output_type
	21, // [21:21] is the sub-list for method input_type
	21, // [21:21] is the sub-list for extension type_name
	21, // [21:21] is the sub-list for extension extendee
	0,  // [0:21] is the sub-list for field type_name
}

func init() { file_goakt_goakt_proto_init() }
func file_goakt_goakt_proto_init() {
	if File_goakt_goakt_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: unsafe.Slice(unsafe.StringData(file_goakt_goakt_proto_rawDesc), len(file_goakt_goakt_proto_rawDesc)),
			NumEnums:      0,
			NumMessages:   14,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_goakt_goakt_proto_goTypes,
		DependencyIndexes: file_goakt_goakt_proto_depIdxs,
		MessageInfos:      file_goakt_goakt_proto_msgTypes,
	}.Build()
	File_goakt_goakt_proto = out.File
	file_goakt_goakt_proto_goTypes = nil
	file_goakt_goakt_proto_depIdxs = nil
}
