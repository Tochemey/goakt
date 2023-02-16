// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        (unknown)
// source: goakt/v1/remoting.proto

package goaktv1

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	anypb "google.golang.org/protobuf/types/known/anypb"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// RemoteSendAsync is used to send a message to an actor remotely
// This type of message expects a reply to be sent back the sender
type RemoteSendSyncRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the sender' address
	Sender *Address `protobuf:"bytes,1,opt,name=sender,proto3" json:"sender,omitempty"`
	// Specifies the receiver' address
	Receiver *Address `protobuf:"bytes,2,opt,name=receiver,proto3" json:"receiver,omitempty"`
	// Specifies the message to send to the actor
	// Any proto message is allowed to be sent
	Message *anypb.Any `protobuf:"bytes,3,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *RemoteSendSyncRequest) Reset() {
	*x = RemoteSendSyncRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_goakt_v1_remoting_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RemoteSendSyncRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteSendSyncRequest) ProtoMessage() {}

func (x *RemoteSendSyncRequest) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_v1_remoting_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteSendSyncRequest.ProtoReflect.Descriptor instead.
func (*RemoteSendSyncRequest) Descriptor() ([]byte, []int) {
	return file_goakt_v1_remoting_proto_rawDescGZIP(), []int{0}
}

func (x *RemoteSendSyncRequest) GetSender() *Address {
	if x != nil {
		return x.Sender
	}
	return nil
}

func (x *RemoteSendSyncRequest) GetReceiver() *Address {
	if x != nil {
		return x.Receiver
	}
	return nil
}

func (x *RemoteSendSyncRequest) GetMessage() *anypb.Any {
	if x != nil {
		return x.Message
	}
	return nil
}

type RemoteSendSyncResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the message to send to the actor
	// Any proto message is allowed to be sent
	Message *anypb.Any `protobuf:"bytes,1,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *RemoteSendSyncResponse) Reset() {
	*x = RemoteSendSyncResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_goakt_v1_remoting_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RemoteSendSyncResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteSendSyncResponse) ProtoMessage() {}

func (x *RemoteSendSyncResponse) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_v1_remoting_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteSendSyncResponse.ProtoReflect.Descriptor instead.
func (*RemoteSendSyncResponse) Descriptor() ([]byte, []int) {
	return file_goakt_v1_remoting_proto_rawDescGZIP(), []int{1}
}

func (x *RemoteSendSyncResponse) GetMessage() *anypb.Any {
	if x != nil {
		return x.Message
	}
	return nil
}

// RemoteSendSync is used to send a message to an actor remotely without expecting any reply
type RemoteSendAsyncRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the actor address
	Address *Address `protobuf:"bytes,1,opt,name=address,proto3" json:"address,omitempty"`
	// Specifies the message to send to the actor
	// Any proto message is allowed to be sent
	Message *anypb.Any `protobuf:"bytes,2,opt,name=message,proto3" json:"message,omitempty"`
}

func (x *RemoteSendAsyncRequest) Reset() {
	*x = RemoteSendAsyncRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_goakt_v1_remoting_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RemoteSendAsyncRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteSendAsyncRequest) ProtoMessage() {}

func (x *RemoteSendAsyncRequest) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_v1_remoting_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteSendAsyncRequest.ProtoReflect.Descriptor instead.
func (*RemoteSendAsyncRequest) Descriptor() ([]byte, []int) {
	return file_goakt_v1_remoting_proto_rawDescGZIP(), []int{2}
}

func (x *RemoteSendAsyncRequest) GetAddress() *Address {
	if x != nil {
		return x.Address
	}
	return nil
}

func (x *RemoteSendAsyncRequest) GetMessage() *anypb.Any {
	if x != nil {
		return x.Message
	}
	return nil
}

type RemoteSendAsyncResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *RemoteSendAsyncResponse) Reset() {
	*x = RemoteSendAsyncResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_goakt_v1_remoting_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RemoteSendAsyncResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteSendAsyncResponse) ProtoMessage() {}

func (x *RemoteSendAsyncResponse) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_v1_remoting_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RemoteSendAsyncResponse.ProtoReflect.Descriptor instead.
func (*RemoteSendAsyncResponse) Descriptor() ([]byte, []int) {
	return file_goakt_v1_remoting_proto_rawDescGZIP(), []int{3}
}

// Address represents an actor address
type Address struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the actor system.
	ActorSystem string `protobuf:"bytes,1,opt,name=actor_system,json=actorSystem,proto3" json:"actor_system,omitempty"`
	// Specifies the remote host address
	Host string `protobuf:"bytes,2,opt,name=host,proto3" json:"host,omitempty"`
	// Specifies the remote port
	Port int32 `protobuf:"varint,3,opt,name=port,proto3" json:"port,omitempty"`
	// Specifies the actor name
	Name string `protobuf:"bytes,4,opt,name=name,proto3" json:"name,omitempty"`
	// Specifies the actor unique id on the remote host
	Id string `protobuf:"bytes,5,opt,name=id,proto3" json:"id,omitempty"`
}

func (x *Address) Reset() {
	*x = Address{}
	if protoimpl.UnsafeEnabled {
		mi := &file_goakt_v1_remoting_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Address) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Address) ProtoMessage() {}

func (x *Address) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_v1_remoting_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
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
	return file_goakt_v1_remoting_proto_rawDescGZIP(), []int{4}
}

func (x *Address) GetActorSystem() string {
	if x != nil {
		return x.ActorSystem
	}
	return ""
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

// RemoteLookupRequest checks whether a given actor exists on a remote host
type RemoteLookupRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the actor address
	Address *Address `protobuf:"bytes,1,opt,name=address,proto3" json:"address,omitempty"`
}

func (x *RemoteLookupRequest) Reset() {
	*x = RemoteLookupRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_goakt_v1_remoting_proto_msgTypes[5]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RemoteLookupRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteLookupRequest) ProtoMessage() {}

func (x *RemoteLookupRequest) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_v1_remoting_proto_msgTypes[5]
	if protoimpl.UnsafeEnabled && x != nil {
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
	return file_goakt_v1_remoting_proto_rawDescGZIP(), []int{5}
}

func (x *RemoteLookupRequest) GetAddress() *Address {
	if x != nil {
		return x.Address
	}
	return nil
}

type RemoteLookupResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *RemoteLookupResponse) Reset() {
	*x = RemoteLookupResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_goakt_v1_remoting_proto_msgTypes[6]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *RemoteLookupResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RemoteLookupResponse) ProtoMessage() {}

func (x *RemoteLookupResponse) ProtoReflect() protoreflect.Message {
	mi := &file_goakt_v1_remoting_proto_msgTypes[6]
	if protoimpl.UnsafeEnabled && x != nil {
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
	return file_goakt_v1_remoting_proto_rawDescGZIP(), []int{6}
}

var File_goakt_v1_remoting_proto protoreflect.FileDescriptor

var file_goakt_v1_remoting_proto_rawDesc = []byte{
	0x0a, 0x17, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2f, 0x76, 0x31, 0x2f, 0x72, 0x65, 0x6d, 0x6f, 0x74,
	0x69, 0x6e, 0x67, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x08, 0x67, 0x6f, 0x61, 0x6b, 0x74,
	0x2e, 0x76, 0x31, 0x1a, 0x19, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x62, 0x75, 0x66, 0x2f, 0x61, 0x6e, 0x79, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0xa1,
	0x01, 0x0a, 0x15, 0x52, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x53, 0x65, 0x6e, 0x64, 0x53, 0x79, 0x6e,
	0x63, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x29, 0x0a, 0x06, 0x73, 0x65, 0x6e, 0x64,
	0x65, 0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x11, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74,
	0x2e, 0x76, 0x31, 0x2e, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x52, 0x06, 0x73, 0x65, 0x6e,
	0x64, 0x65, 0x72, 0x12, 0x2d, 0x0a, 0x08, 0x72, 0x65, 0x63, 0x65, 0x69, 0x76, 0x65, 0x72, 0x18,
	0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x11, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2e, 0x76, 0x31,
	0x2e, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x52, 0x08, 0x72, 0x65, 0x63, 0x65, 0x69, 0x76,
	0x65, 0x72, 0x12, 0x2e, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x03, 0x20,
	0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x41, 0x6e, 0x79, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61,
	0x67, 0x65, 0x22, 0x48, 0x0a, 0x16, 0x52, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x53, 0x65, 0x6e, 0x64,
	0x53, 0x79, 0x6e, 0x63, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x2e, 0x0a, 0x07,
	0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e,
	0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e,
	0x41, 0x6e, 0x79, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x22, 0x75, 0x0a, 0x16,
	0x52, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x53, 0x65, 0x6e, 0x64, 0x41, 0x73, 0x79, 0x6e, 0x63, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x2b, 0x0a, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73,
	0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x11, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2e,
	0x76, 0x31, 0x2e, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x52, 0x07, 0x61, 0x64, 0x64, 0x72,
	0x65, 0x73, 0x73, 0x12, 0x2e, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x02,
	0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x41, 0x6e, 0x79, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73,
	0x61, 0x67, 0x65, 0x22, 0x19, 0x0a, 0x17, 0x52, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x53, 0x65, 0x6e,
	0x64, 0x41, 0x73, 0x79, 0x6e, 0x63, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x78,
	0x0a, 0x07, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x21, 0x0a, 0x0c, 0x61, 0x63, 0x74,
	0x6f, 0x72, 0x5f, 0x73, 0x79, 0x73, 0x74, 0x65, 0x6d, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x0b, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x53, 0x79, 0x73, 0x74, 0x65, 0x6d, 0x12, 0x12, 0x0a, 0x04,
	0x68, 0x6f, 0x73, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x68, 0x6f, 0x73, 0x74,
	0x12, 0x12, 0x0a, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x05, 0x52, 0x04,
	0x70, 0x6f, 0x72, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x04, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x0e, 0x0a, 0x02, 0x69, 0x64, 0x18, 0x05,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x02, 0x69, 0x64, 0x22, 0x42, 0x0a, 0x13, 0x52, 0x65, 0x6d, 0x6f,
	0x74, 0x65, 0x4c, 0x6f, 0x6f, 0x6b, 0x75, 0x70, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12,
	0x2b, 0x0a, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x11, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x41, 0x64, 0x64, 0x72,
	0x65, 0x73, 0x73, 0x52, 0x07, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x22, 0x16, 0x0a, 0x14,
	0x52, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x4c, 0x6f, 0x6f, 0x6b, 0x75, 0x70, 0x52, 0x65, 0x73, 0x70,
	0x6f, 0x6e, 0x73, 0x65, 0x32, 0x8d, 0x02, 0x0a, 0x0f, 0x52, 0x65, 0x6d, 0x6f, 0x74, 0x69, 0x6e,
	0x67, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x53, 0x0a, 0x0e, 0x52, 0x65, 0x6d, 0x6f,
	0x74, 0x65, 0x53, 0x65, 0x6e, 0x64, 0x53, 0x79, 0x6e, 0x63, 0x12, 0x1f, 0x2e, 0x67, 0x6f, 0x61,
	0x6b, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x52, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x53, 0x65, 0x6e, 0x64,
	0x53, 0x79, 0x6e, 0x63, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x20, 0x2e, 0x67, 0x6f,
	0x61, 0x6b, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x52, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x53, 0x65, 0x6e,
	0x64, 0x53, 0x79, 0x6e, 0x63, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x56, 0x0a,
	0x0f, 0x52, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x53, 0x65, 0x6e, 0x64, 0x41, 0x73, 0x79, 0x6e, 0x63,
	0x12, 0x20, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x52, 0x65, 0x6d, 0x6f,
	0x74, 0x65, 0x53, 0x65, 0x6e, 0x64, 0x41, 0x73, 0x79, 0x6e, 0x63, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x1a, 0x21, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x52, 0x65,
	0x6d, 0x6f, 0x74, 0x65, 0x53, 0x65, 0x6e, 0x64, 0x41, 0x73, 0x79, 0x6e, 0x63, 0x52, 0x65, 0x73,
	0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x4d, 0x0a, 0x0c, 0x52, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x4c,
	0x6f, 0x6f, 0x6b, 0x75, 0x70, 0x12, 0x1d, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2e, 0x76, 0x31,
	0x2e, 0x52, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x4c, 0x6f, 0x6f, 0x6b, 0x75, 0x70, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x1a, 0x1e, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2e, 0x76, 0x31, 0x2e,
	0x52, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x4c, 0x6f, 0x6f, 0x6b, 0x75, 0x70, 0x52, 0x65, 0x73, 0x70,
	0x6f, 0x6e, 0x73, 0x65, 0x42, 0x90, 0x01, 0x0a, 0x0c, 0x63, 0x6f, 0x6d, 0x2e, 0x67, 0x6f, 0x61,
	0x6b, 0x74, 0x2e, 0x76, 0x31, 0x42, 0x0d, 0x52, 0x65, 0x6d, 0x6f, 0x74, 0x69, 0x6e, 0x67, 0x50,
	0x72, 0x6f, 0x74, 0x6f, 0x48, 0x02, 0x50, 0x01, 0x5a, 0x2e, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62,
	0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x74, 0x6f, 0x63, 0x68, 0x65, 0x6d, 0x65, 0x79, 0x2f, 0x67, 0x6f,
	0x61, 0x6b, 0x74, 0x2f, 0x67, 0x65, 0x6e, 0x2f, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2f, 0x76, 0x31,
	0x3b, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x76, 0x31, 0xa2, 0x02, 0x03, 0x47, 0x58, 0x58, 0xaa, 0x02,
	0x08, 0x47, 0x6f, 0x61, 0x6b, 0x74, 0x2e, 0x56, 0x31, 0xca, 0x02, 0x08, 0x47, 0x6f, 0x61, 0x6b,
	0x74, 0x5c, 0x56, 0x31, 0xe2, 0x02, 0x14, 0x47, 0x6f, 0x61, 0x6b, 0x74, 0x5c, 0x56, 0x31, 0x5c,
	0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x09, 0x47, 0x6f,
	0x61, 0x6b, 0x74, 0x3a, 0x3a, 0x56, 0x31, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_goakt_v1_remoting_proto_rawDescOnce sync.Once
	file_goakt_v1_remoting_proto_rawDescData = file_goakt_v1_remoting_proto_rawDesc
)

func file_goakt_v1_remoting_proto_rawDescGZIP() []byte {
	file_goakt_v1_remoting_proto_rawDescOnce.Do(func() {
		file_goakt_v1_remoting_proto_rawDescData = protoimpl.X.CompressGZIP(file_goakt_v1_remoting_proto_rawDescData)
	})
	return file_goakt_v1_remoting_proto_rawDescData
}

var file_goakt_v1_remoting_proto_msgTypes = make([]protoimpl.MessageInfo, 7)
var file_goakt_v1_remoting_proto_goTypes = []interface{}{
	(*RemoteSendSyncRequest)(nil),   // 0: goakt.v1.RemoteSendSyncRequest
	(*RemoteSendSyncResponse)(nil),  // 1: goakt.v1.RemoteSendSyncResponse
	(*RemoteSendAsyncRequest)(nil),  // 2: goakt.v1.RemoteSendAsyncRequest
	(*RemoteSendAsyncResponse)(nil), // 3: goakt.v1.RemoteSendAsyncResponse
	(*Address)(nil),                 // 4: goakt.v1.Address
	(*RemoteLookupRequest)(nil),     // 5: goakt.v1.RemoteLookupRequest
	(*RemoteLookupResponse)(nil),    // 6: goakt.v1.RemoteLookupResponse
	(*anypb.Any)(nil),               // 7: google.protobuf.Any
}
var file_goakt_v1_remoting_proto_depIdxs = []int32{
	4,  // 0: goakt.v1.RemoteSendSyncRequest.sender:type_name -> goakt.v1.Address
	4,  // 1: goakt.v1.RemoteSendSyncRequest.receiver:type_name -> goakt.v1.Address
	7,  // 2: goakt.v1.RemoteSendSyncRequest.message:type_name -> google.protobuf.Any
	7,  // 3: goakt.v1.RemoteSendSyncResponse.message:type_name -> google.protobuf.Any
	4,  // 4: goakt.v1.RemoteSendAsyncRequest.address:type_name -> goakt.v1.Address
	7,  // 5: goakt.v1.RemoteSendAsyncRequest.message:type_name -> google.protobuf.Any
	4,  // 6: goakt.v1.RemoteLookupRequest.address:type_name -> goakt.v1.Address
	0,  // 7: goakt.v1.RemotingService.RemoteSendSync:input_type -> goakt.v1.RemoteSendSyncRequest
	2,  // 8: goakt.v1.RemotingService.RemoteSendAsync:input_type -> goakt.v1.RemoteSendAsyncRequest
	5,  // 9: goakt.v1.RemotingService.RemoteLookup:input_type -> goakt.v1.RemoteLookupRequest
	1,  // 10: goakt.v1.RemotingService.RemoteSendSync:output_type -> goakt.v1.RemoteSendSyncResponse
	3,  // 11: goakt.v1.RemotingService.RemoteSendAsync:output_type -> goakt.v1.RemoteSendAsyncResponse
	6,  // 12: goakt.v1.RemotingService.RemoteLookup:output_type -> goakt.v1.RemoteLookupResponse
	10, // [10:13] is the sub-list for method output_type
	7,  // [7:10] is the sub-list for method input_type
	7,  // [7:7] is the sub-list for extension type_name
	7,  // [7:7] is the sub-list for extension extendee
	0,  // [0:7] is the sub-list for field type_name
}

func init() { file_goakt_v1_remoting_proto_init() }
func file_goakt_v1_remoting_proto_init() {
	if File_goakt_v1_remoting_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_goakt_v1_remoting_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*RemoteSendSyncRequest); i {
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
		file_goakt_v1_remoting_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*RemoteSendSyncResponse); i {
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
		file_goakt_v1_remoting_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*RemoteSendAsyncRequest); i {
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
		file_goakt_v1_remoting_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*RemoteSendAsyncResponse); i {
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
		file_goakt_v1_remoting_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Address); i {
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
		file_goakt_v1_remoting_proto_msgTypes[5].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*RemoteLookupRequest); i {
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
		file_goakt_v1_remoting_proto_msgTypes[6].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*RemoteLookupResponse); i {
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
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_goakt_v1_remoting_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   7,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_goakt_v1_remoting_proto_goTypes,
		DependencyIndexes: file_goakt_v1_remoting_proto_depIdxs,
		MessageInfos:      file_goakt_v1_remoting_proto_msgTypes,
	}.Build()
	File_goakt_v1_remoting_proto = out.File
	file_goakt_v1_remoting_proto_rawDesc = nil
	file_goakt_v1_remoting_proto_goTypes = nil
	file_goakt_v1_remoting_proto_depIdxs = nil
}
