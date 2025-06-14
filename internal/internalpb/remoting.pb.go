// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.6
// 	protoc        (unknown)
// source: internal/remoting.proto

package internalpb

import (
	goaktpb "github.com/tochemey/goakt/v3/goaktpb"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	anypb "google.golang.org/protobuf/types/known/anypb"
	durationpb "google.golang.org/protobuf/types/known/durationpb"
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

// RemoteAsk is used to send a message to an actor remotely and expect a response
// immediately.
type RemoteAskRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the remote message to send
	RemoteMessages []*RemoteMessage `protobuf:"bytes,1,rep,name=remote_messages,json=remoteMessages,proto3" json:"remote_messages,omitempty"`
	// Specifies the timeout(how long to wait for a reply)
	Timeout       *durationpb.Duration `protobuf:"bytes,2,opt,name=timeout,proto3" json:"timeout,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteAskRequest) Reset() {
	*x = RemoteAskRequest{}
	mi := &file_internal_remoting_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteAskRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteAskRequest) ProtoMessage() {}

func (x *RemoteAskRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteAskRequest.ProtoReflect.Descriptor instead.
func (*RemoteAskRequest) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{0}
}

func (x *RemoteAskRequest) GetRemoteMessages() []*RemoteMessage {
	if x != nil {
		return x.RemoteMessages
	}
	return nil
}

func (x *RemoteAskRequest) GetTimeout() *durationpb.Duration {
	if x != nil {
		return x.Timeout
	}
	return nil
}

type RemoteAskResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the message to send to the actor
	// Any proto message is allowed to be sent
	Messages      []*anypb.Any `protobuf:"bytes,1,rep,name=messages,proto3" json:"messages,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteAskResponse) Reset() {
	*x = RemoteAskResponse{}
	mi := &file_internal_remoting_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteAskResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteAskResponse) ProtoMessage() {}

func (x *RemoteAskResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteAskResponse.ProtoReflect.Descriptor instead.
func (*RemoteAskResponse) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{1}
}

func (x *RemoteAskResponse) GetMessages() []*anypb.Any {
	if x != nil {
		return x.Messages
	}
	return nil
}

// RemoteTell is used to send a message to an actor remotely
type RemoteTellRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the remote message to send
	RemoteMessages []*RemoteMessage `protobuf:"bytes,1,rep,name=remote_messages,json=remoteMessages,proto3" json:"remote_messages,omitempty"`
	unknownFields  protoimpl.UnknownFields
	sizeCache      protoimpl.SizeCache
}

func (x *RemoteTellRequest) Reset() {
	*x = RemoteTellRequest{}
	mi := &file_internal_remoting_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteTellRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteTellRequest) ProtoMessage() {}

func (x *RemoteTellRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteTellRequest.ProtoReflect.Descriptor instead.
func (*RemoteTellRequest) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{2}
}

func (x *RemoteTellRequest) GetRemoteMessages() []*RemoteMessage {
	if x != nil {
		return x.RemoteMessages
	}
	return nil
}

type RemoteTellResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteTellResponse) Reset() {
	*x = RemoteTellResponse{}
	mi := &file_internal_remoting_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteTellResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteTellResponse) ProtoMessage() {}

func (x *RemoteTellResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteTellResponse.ProtoReflect.Descriptor instead.
func (*RemoteTellResponse) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{3}
}

// RemoteLookupRequest checks whether a given actor exists on a remote host
type RemoteLookupRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the remote host address
	Host string `protobuf:"bytes,1,opt,name=host,proto3" json:"host,omitempty"`
	// Specifies the remote port
	Port int32 `protobuf:"varint,2,opt,name=port,proto3" json:"port,omitempty"`
	// Specifies the actor name
	Name          string `protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteLookupRequest) Reset() {
	*x = RemoteLookupRequest{}
	mi := &file_internal_remoting_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteLookupRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteLookupRequest) ProtoMessage() {}

func (x *RemoteLookupRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteLookupRequest.ProtoReflect.Descriptor instead.
func (*RemoteLookupRequest) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{4}
}

func (x *RemoteLookupRequest) GetHost() string {
	if x != nil {
		return x.Host
	}
	return ""
}

func (x *RemoteLookupRequest) GetPort() int32 {
	if x != nil {
		return x.Port
	}
	return 0
}

func (x *RemoteLookupRequest) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

type RemoteLookupResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the actor address
	Address       *goaktpb.Address `protobuf:"bytes,1,opt,name=address,proto3" json:"address,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteLookupResponse) Reset() {
	*x = RemoteLookupResponse{}
	mi := &file_internal_remoting_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteLookupResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteLookupResponse) ProtoMessage() {}

func (x *RemoteLookupResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteLookupResponse.ProtoReflect.Descriptor instead.
func (*RemoteLookupResponse) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{5}
}

func (x *RemoteLookupResponse) GetAddress() *goaktpb.Address {
	if x != nil {
		return x.Address
	}
	return nil
}

// RemoteMessage will be used by Actors to communicate remotely
type RemoteMessage struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the sender' address
	Sender *goaktpb.Address `protobuf:"bytes,1,opt,name=sender,proto3" json:"sender,omitempty"`
	// Specifies the actor address
	Receiver *goaktpb.Address `protobuf:"bytes,2,opt,name=receiver,proto3" json:"receiver,omitempty"`
	// Specifies the message to send to the actor
	// Any proto message is allowed to be sent
	Message       *anypb.Any `protobuf:"bytes,3,opt,name=message,proto3" json:"message,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteMessage) Reset() {
	*x = RemoteMessage{}
	mi := &file_internal_remoting_proto_msgTypes[6]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteMessage) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteMessage) ProtoMessage() {}

func (x *RemoteMessage) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[6]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteMessage.ProtoReflect.Descriptor instead.
func (*RemoteMessage) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{6}
}

func (x *RemoteMessage) GetSender() *goaktpb.Address {
	if x != nil {
		return x.Sender
	}
	return nil
}

func (x *RemoteMessage) GetReceiver() *goaktpb.Address {
	if x != nil {
		return x.Receiver
	}
	return nil
}

func (x *RemoteMessage) GetMessage() *anypb.Any {
	if x != nil {
		return x.Message
	}
	return nil
}

type RemoteReSpawnRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the remote host address
	Host string `protobuf:"bytes,1,opt,name=host,proto3" json:"host,omitempty"`
	// Specifies the remote port
	Port int32 `protobuf:"varint,2,opt,name=port,proto3" json:"port,omitempty"`
	// Specifies the actor name
	Name          string `protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteReSpawnRequest) Reset() {
	*x = RemoteReSpawnRequest{}
	mi := &file_internal_remoting_proto_msgTypes[7]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteReSpawnRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteReSpawnRequest) ProtoMessage() {}

func (x *RemoteReSpawnRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[7]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteReSpawnRequest.ProtoReflect.Descriptor instead.
func (*RemoteReSpawnRequest) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{7}
}

func (x *RemoteReSpawnRequest) GetHost() string {
	if x != nil {
		return x.Host
	}
	return ""
}

func (x *RemoteReSpawnRequest) GetPort() int32 {
	if x != nil {
		return x.Port
	}
	return 0
}

func (x *RemoteReSpawnRequest) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

type RemoteReSpawnResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteReSpawnResponse) Reset() {
	*x = RemoteReSpawnResponse{}
	mi := &file_internal_remoting_proto_msgTypes[8]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteReSpawnResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteReSpawnResponse) ProtoMessage() {}

func (x *RemoteReSpawnResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[8]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteReSpawnResponse.ProtoReflect.Descriptor instead.
func (*RemoteReSpawnResponse) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{8}
}

type RemoteStopRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the remote host address
	Host string `protobuf:"bytes,1,opt,name=host,proto3" json:"host,omitempty"`
	// Specifies the remote port
	Port int32 `protobuf:"varint,2,opt,name=port,proto3" json:"port,omitempty"`
	// Specifies the actor name
	Name          string `protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteStopRequest) Reset() {
	*x = RemoteStopRequest{}
	mi := &file_internal_remoting_proto_msgTypes[9]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteStopRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteStopRequest) ProtoMessage() {}

func (x *RemoteStopRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[9]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteStopRequest.ProtoReflect.Descriptor instead.
func (*RemoteStopRequest) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{9}
}

func (x *RemoteStopRequest) GetHost() string {
	if x != nil {
		return x.Host
	}
	return ""
}

func (x *RemoteStopRequest) GetPort() int32 {
	if x != nil {
		return x.Port
	}
	return 0
}

func (x *RemoteStopRequest) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

type RemoteStopResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteStopResponse) Reset() {
	*x = RemoteStopResponse{}
	mi := &file_internal_remoting_proto_msgTypes[10]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteStopResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteStopResponse) ProtoMessage() {}

func (x *RemoteStopResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[10]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteStopResponse.ProtoReflect.Descriptor instead.
func (*RemoteStopResponse) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{10}
}

type RemoteSpawnRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the remote host address
	Host string `protobuf:"bytes,1,opt,name=host,proto3" json:"host,omitempty"`
	// Specifies the remote port
	Port int32 `protobuf:"varint,2,opt,name=port,proto3" json:"port,omitempty"`
	// Specifies the actor name.
	ActorName string `protobuf:"bytes,3,opt,name=actor_name,json=actorName,proto3" json:"actor_name,omitempty"`
	// Specifies the actor type
	ActorType string `protobuf:"bytes,4,opt,name=actor_type,json=actorType,proto3" json:"actor_type,omitempty"`
	// Specifies if the actor is a singleton
	IsSingleton bool `protobuf:"varint,5,opt,name=is_singleton,json=isSingleton,proto3" json:"is_singleton,omitempty"`
	// Specifies if the actor is relocatable
	Relocatable bool `protobuf:"varint,6,opt,name=relocatable,proto3" json:"relocatable,omitempty"`
	// Specifies the passivation strategy
	PassivationStrategy *PassivationStrategy `protobuf:"bytes,7,opt,name=passivation_strategy,json=passivationStrategy,proto3" json:"passivation_strategy,omitempty"`
	// Specifies the dependencies
	Dependencies []*Dependency `protobuf:"bytes,8,rep,name=dependencies,proto3" json:"dependencies,omitempty"`
	// States whether the actor will require a stash buffer
	EnableStash   bool `protobuf:"varint,9,opt,name=enable_stash,json=enableStash,proto3" json:"enable_stash,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteSpawnRequest) Reset() {
	*x = RemoteSpawnRequest{}
	mi := &file_internal_remoting_proto_msgTypes[11]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteSpawnRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteSpawnRequest) ProtoMessage() {}

func (x *RemoteSpawnRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[11]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteSpawnRequest.ProtoReflect.Descriptor instead.
func (*RemoteSpawnRequest) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{11}
}

func (x *RemoteSpawnRequest) GetHost() string {
	if x != nil {
		return x.Host
	}
	return ""
}

func (x *RemoteSpawnRequest) GetPort() int32 {
	if x != nil {
		return x.Port
	}
	return 0
}

func (x *RemoteSpawnRequest) GetActorName() string {
	if x != nil {
		return x.ActorName
	}
	return ""
}

func (x *RemoteSpawnRequest) GetActorType() string {
	if x != nil {
		return x.ActorType
	}
	return ""
}

func (x *RemoteSpawnRequest) GetIsSingleton() bool {
	if x != nil {
		return x.IsSingleton
	}
	return false
}

func (x *RemoteSpawnRequest) GetRelocatable() bool {
	if x != nil {
		return x.Relocatable
	}
	return false
}

func (x *RemoteSpawnRequest) GetPassivationStrategy() *PassivationStrategy {
	if x != nil {
		return x.PassivationStrategy
	}
	return nil
}

func (x *RemoteSpawnRequest) GetDependencies() []*Dependency {
	if x != nil {
		return x.Dependencies
	}
	return nil
}

func (x *RemoteSpawnRequest) GetEnableStash() bool {
	if x != nil {
		return x.EnableStash
	}
	return false
}

type RemoteSpawnResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteSpawnResponse) Reset() {
	*x = RemoteSpawnResponse{}
	mi := &file_internal_remoting_proto_msgTypes[12]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteSpawnResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteSpawnResponse) ProtoMessage() {}

func (x *RemoteSpawnResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[12]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteSpawnResponse.ProtoReflect.Descriptor instead.
func (*RemoteSpawnResponse) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{12}
}

type RemoteReinstateRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the remote host address
	Host string `protobuf:"bytes,1,opt,name=host,proto3" json:"host,omitempty"`
	// Specifies the remote port
	Port int32 `protobuf:"varint,2,opt,name=port,proto3" json:"port,omitempty"`
	// Specifies the actor name
	Name          string `protobuf:"bytes,3,opt,name=name,proto3" json:"name,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteReinstateRequest) Reset() {
	*x = RemoteReinstateRequest{}
	mi := &file_internal_remoting_proto_msgTypes[13]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteReinstateRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteReinstateRequest) ProtoMessage() {}

func (x *RemoteReinstateRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[13]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteReinstateRequest.ProtoReflect.Descriptor instead.
func (*RemoteReinstateRequest) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{13}
}

func (x *RemoteReinstateRequest) GetHost() string {
	if x != nil {
		return x.Host
	}
	return ""
}

func (x *RemoteReinstateRequest) GetPort() int32 {
	if x != nil {
		return x.Port
	}
	return 0
}

func (x *RemoteReinstateRequest) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

type RemoteReinstateResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RemoteReinstateResponse) Reset() {
	*x = RemoteReinstateResponse{}
	mi := &file_internal_remoting_proto_msgTypes[14]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RemoteReinstateResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteReinstateResponse) ProtoMessage() {}

func (x *RemoteReinstateResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_remoting_proto_msgTypes[14]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteReinstateResponse.ProtoReflect.Descriptor instead.
func (*RemoteReinstateResponse) Descriptor() ([]byte, []int) {
	return file_internal_remoting_proto_rawDescGZIP(), []int{14}
}

var File_internal_remoting_proto protoreflect.FileDescriptor

const file_internal_remoting_proto_rawDesc = "" +
	"\n" +
	"\x17internal/remoting.proto\x12\n" +
	"internalpb\x1a\x11goakt/goakt.proto\x1a\x19google/protobuf/any.proto\x1a\x1egoogle/protobuf/duration.proto\x1a\x19internal/dependency.proto\x1a\x1ainternal/passivation.proto\"\x8b\x01\n" +
	"\x10RemoteAskRequest\x12B\n" +
	"\x0fremote_messages\x18\x01 \x03(\v2\x19.internalpb.RemoteMessageR\x0eremoteMessages\x123\n" +
	"\atimeout\x18\x02 \x01(\v2\x19.google.protobuf.DurationR\atimeout\"E\n" +
	"\x11RemoteAskResponse\x120\n" +
	"\bmessages\x18\x01 \x03(\v2\x14.google.protobuf.AnyR\bmessages\"W\n" +
	"\x11RemoteTellRequest\x12B\n" +
	"\x0fremote_messages\x18\x01 \x03(\v2\x19.internalpb.RemoteMessageR\x0eremoteMessages\"\x14\n" +
	"\x12RemoteTellResponse\"Q\n" +
	"\x13RemoteLookupRequest\x12\x12\n" +
	"\x04host\x18\x01 \x01(\tR\x04host\x12\x12\n" +
	"\x04port\x18\x02 \x01(\x05R\x04port\x12\x12\n" +
	"\x04name\x18\x03 \x01(\tR\x04name\"B\n" +
	"\x14RemoteLookupResponse\x12*\n" +
	"\aaddress\x18\x01 \x01(\v2\x10.goaktpb.AddressR\aaddress\"\x97\x01\n" +
	"\rRemoteMessage\x12(\n" +
	"\x06sender\x18\x01 \x01(\v2\x10.goaktpb.AddressR\x06sender\x12,\n" +
	"\breceiver\x18\x02 \x01(\v2\x10.goaktpb.AddressR\breceiver\x12.\n" +
	"\amessage\x18\x03 \x01(\v2\x14.google.protobuf.AnyR\amessage\"R\n" +
	"\x14RemoteReSpawnRequest\x12\x12\n" +
	"\x04host\x18\x01 \x01(\tR\x04host\x12\x12\n" +
	"\x04port\x18\x02 \x01(\x05R\x04port\x12\x12\n" +
	"\x04name\x18\x03 \x01(\tR\x04name\"\x17\n" +
	"\x15RemoteReSpawnResponse\"O\n" +
	"\x11RemoteStopRequest\x12\x12\n" +
	"\x04host\x18\x01 \x01(\tR\x04host\x12\x12\n" +
	"\x04port\x18\x02 \x01(\x05R\x04port\x12\x12\n" +
	"\x04name\x18\x03 \x01(\tR\x04name\"\x14\n" +
	"\x12RemoteStopResponse\"\xf2\x02\n" +
	"\x12RemoteSpawnRequest\x12\x12\n" +
	"\x04host\x18\x01 \x01(\tR\x04host\x12\x12\n" +
	"\x04port\x18\x02 \x01(\x05R\x04port\x12\x1d\n" +
	"\n" +
	"actor_name\x18\x03 \x01(\tR\tactorName\x12\x1d\n" +
	"\n" +
	"actor_type\x18\x04 \x01(\tR\tactorType\x12!\n" +
	"\fis_singleton\x18\x05 \x01(\bR\visSingleton\x12 \n" +
	"\vrelocatable\x18\x06 \x01(\bR\vrelocatable\x12R\n" +
	"\x14passivation_strategy\x18\a \x01(\v2\x1f.internalpb.PassivationStrategyR\x13passivationStrategy\x12:\n" +
	"\fdependencies\x18\b \x03(\v2\x16.internalpb.DependencyR\fdependencies\x12!\n" +
	"\fenable_stash\x18\t \x01(\bR\venableStash\"\x15\n" +
	"\x13RemoteSpawnResponse\"T\n" +
	"\x16RemoteReinstateRequest\x12\x12\n" +
	"\x04host\x18\x01 \x01(\tR\x04host\x12\x12\n" +
	"\x04port\x18\x02 \x01(\x05R\x04port\x12\x12\n" +
	"\x04name\x18\x03 \x01(\tR\x04name\"\x19\n" +
	"\x17RemoteReinstateResponse2\xca\x04\n" +
	"\x0fRemotingService\x12H\n" +
	"\tRemoteAsk\x12\x1c.internalpb.RemoteAskRequest\x1a\x1d.internalpb.RemoteAskResponse\x12K\n" +
	"\n" +
	"RemoteTell\x12\x1d.internalpb.RemoteTellRequest\x1a\x1e.internalpb.RemoteTellResponse\x12Q\n" +
	"\fRemoteLookup\x12\x1f.internalpb.RemoteLookupRequest\x1a .internalpb.RemoteLookupResponse\x12T\n" +
	"\rRemoteReSpawn\x12 .internalpb.RemoteReSpawnRequest\x1a!.internalpb.RemoteReSpawnResponse\x12K\n" +
	"\n" +
	"RemoteStop\x12\x1d.internalpb.RemoteStopRequest\x1a\x1e.internalpb.RemoteStopResponse\x12N\n" +
	"\vRemoteSpawn\x12\x1e.internalpb.RemoteSpawnRequest\x1a\x1f.internalpb.RemoteSpawnResponse\x12Z\n" +
	"\x0fRemoteReinstate\x12\".internalpb.RemoteReinstateRequest\x1a#.internalpb.RemoteReinstateResponseB\xa6\x01\n" +
	"\x0ecom.internalpbB\rRemotingProtoH\x02P\x01Z;github.com/tochemey/goakt/v3/internal/internalpb;internalpb\xa2\x02\x03IXX\xaa\x02\n" +
	"Internalpb\xca\x02\n" +
	"Internalpb\xe2\x02\x16Internalpb\\GPBMetadata\xea\x02\n" +
	"Internalpbb\x06proto3"

var (
	file_internal_remoting_proto_rawDescOnce sync.Once
	file_internal_remoting_proto_rawDescData []byte
)

func file_internal_remoting_proto_rawDescGZIP() []byte {
	file_internal_remoting_proto_rawDescOnce.Do(func() {
		file_internal_remoting_proto_rawDescData = protoimpl.X.CompressGZIP(unsafe.Slice(unsafe.StringData(file_internal_remoting_proto_rawDesc), len(file_internal_remoting_proto_rawDesc)))
	})
	return file_internal_remoting_proto_rawDescData
}

var file_internal_remoting_proto_msgTypes = make([]protoimpl.MessageInfo, 15)
var file_internal_remoting_proto_goTypes = []any{
	(*RemoteAskRequest)(nil),        // 0: internalpb.RemoteAskRequest
	(*RemoteAskResponse)(nil),       // 1: internalpb.RemoteAskResponse
	(*RemoteTellRequest)(nil),       // 2: internalpb.RemoteTellRequest
	(*RemoteTellResponse)(nil),      // 3: internalpb.RemoteTellResponse
	(*RemoteLookupRequest)(nil),     // 4: internalpb.RemoteLookupRequest
	(*RemoteLookupResponse)(nil),    // 5: internalpb.RemoteLookupResponse
	(*RemoteMessage)(nil),           // 6: internalpb.RemoteMessage
	(*RemoteReSpawnRequest)(nil),    // 7: internalpb.RemoteReSpawnRequest
	(*RemoteReSpawnResponse)(nil),   // 8: internalpb.RemoteReSpawnResponse
	(*RemoteStopRequest)(nil),       // 9: internalpb.RemoteStopRequest
	(*RemoteStopResponse)(nil),      // 10: internalpb.RemoteStopResponse
	(*RemoteSpawnRequest)(nil),      // 11: internalpb.RemoteSpawnRequest
	(*RemoteSpawnResponse)(nil),     // 12: internalpb.RemoteSpawnResponse
	(*RemoteReinstateRequest)(nil),  // 13: internalpb.RemoteReinstateRequest
	(*RemoteReinstateResponse)(nil), // 14: internalpb.RemoteReinstateResponse
	(*durationpb.Duration)(nil),     // 15: google.protobuf.Duration
	(*anypb.Any)(nil),               // 16: google.protobuf.Any
	(*goaktpb.Address)(nil),         // 17: goaktpb.Address
	(*PassivationStrategy)(nil),     // 18: internalpb.PassivationStrategy
	(*Dependency)(nil),              // 19: internalpb.Dependency
}
var file_internal_remoting_proto_depIdxs = []int32{
	6,  // 0: internalpb.RemoteAskRequest.remote_messages:type_name -> internalpb.RemoteMessage
	15, // 1: internalpb.RemoteAskRequest.timeout:type_name -> google.protobuf.Duration
	16, // 2: internalpb.RemoteAskResponse.messages:type_name -> google.protobuf.Any
	6,  // 3: internalpb.RemoteTellRequest.remote_messages:type_name -> internalpb.RemoteMessage
	17, // 4: internalpb.RemoteLookupResponse.address:type_name -> goaktpb.Address
	17, // 5: internalpb.RemoteMessage.sender:type_name -> goaktpb.Address
	17, // 6: internalpb.RemoteMessage.receiver:type_name -> goaktpb.Address
	16, // 7: internalpb.RemoteMessage.message:type_name -> google.protobuf.Any
	18, // 8: internalpb.RemoteSpawnRequest.passivation_strategy:type_name -> internalpb.PassivationStrategy
	19, // 9: internalpb.RemoteSpawnRequest.dependencies:type_name -> internalpb.Dependency
	0,  // 10: internalpb.RemotingService.RemoteAsk:input_type -> internalpb.RemoteAskRequest
	2,  // 11: internalpb.RemotingService.RemoteTell:input_type -> internalpb.RemoteTellRequest
	4,  // 12: internalpb.RemotingService.RemoteLookup:input_type -> internalpb.RemoteLookupRequest
	7,  // 13: internalpb.RemotingService.RemoteReSpawn:input_type -> internalpb.RemoteReSpawnRequest
	9,  // 14: internalpb.RemotingService.RemoteStop:input_type -> internalpb.RemoteStopRequest
	11, // 15: internalpb.RemotingService.RemoteSpawn:input_type -> internalpb.RemoteSpawnRequest
	13, // 16: internalpb.RemotingService.RemoteReinstate:input_type -> internalpb.RemoteReinstateRequest
	1,  // 17: internalpb.RemotingService.RemoteAsk:output_type -> internalpb.RemoteAskResponse
	3,  // 18: internalpb.RemotingService.RemoteTell:output_type -> internalpb.RemoteTellResponse
	5,  // 19: internalpb.RemotingService.RemoteLookup:output_type -> internalpb.RemoteLookupResponse
	8,  // 20: internalpb.RemotingService.RemoteReSpawn:output_type -> internalpb.RemoteReSpawnResponse
	10, // 21: internalpb.RemotingService.RemoteStop:output_type -> internalpb.RemoteStopResponse
	12, // 22: internalpb.RemotingService.RemoteSpawn:output_type -> internalpb.RemoteSpawnResponse
	14, // 23: internalpb.RemotingService.RemoteReinstate:output_type -> internalpb.RemoteReinstateResponse
	17, // [17:24] is the sub-list for method output_type
	10, // [10:17] is the sub-list for method input_type
	10, // [10:10] is the sub-list for extension type_name
	10, // [10:10] is the sub-list for extension extendee
	0,  // [0:10] is the sub-list for field type_name
}

func init() { file_internal_remoting_proto_init() }
func file_internal_remoting_proto_init() {
	if File_internal_remoting_proto != nil {
		return
	}
	file_internal_dependency_proto_init()
	file_internal_passivation_proto_init()
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: unsafe.Slice(unsafe.StringData(file_internal_remoting_proto_rawDesc), len(file_internal_remoting_proto_rawDesc)),
			NumEnums:      0,
			NumMessages:   15,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_internal_remoting_proto_goTypes,
		DependencyIndexes: file_internal_remoting_proto_depIdxs,
		MessageInfos:      file_internal_remoting_proto_msgTypes,
	}.Build()
	File_internal_remoting_proto = out.File
	file_internal_remoting_proto_goTypes = nil
	file_internal_remoting_proto_depIdxs = nil
}
