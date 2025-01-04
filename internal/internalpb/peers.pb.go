// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.1
// 	protoc        (unknown)
// source: internal/peers.proto

package internalpb

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
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

// PeersSync is used to share a created/ restarted actor
// on a given node to his peers when cluster is enabled
type PeersSync struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the peer host
	Host string `protobuf:"bytes,1,opt,name=host,proto3" json:"host,omitempty"`
	// Specifies the remoting port
	RemotingPort int32 `protobuf:"varint,2,opt,name=remoting_port,json=remotingPort,proto3" json:"remoting_port,omitempty"`
	// Specifies the remoting host
	PeersPort int32 `protobuf:"varint,3,opt,name=peers_port,json=peersPort,proto3" json:"peers_port,omitempty"`
	// Specifies the wire actor
	Actor         *ActorRef `protobuf:"bytes,4,opt,name=actor,proto3" json:"actor,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PeersSync) Reset() {
	*x = PeersSync{}
	mi := &file_internal_peers_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PeersSync) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PeersSync) ProtoMessage() {}

func (x *PeersSync) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PeersSync.ProtoReflect.Descriptor instead.
func (*PeersSync) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{0}
}

func (x *PeersSync) GetHost() string {
	if x != nil {
		return x.Host
	}
	return ""
}

func (x *PeersSync) GetRemotingPort() int32 {
	if x != nil {
		return x.RemotingPort
	}
	return 0
}

func (x *PeersSync) GetPeersPort() int32 {
	if x != nil {
		return x.PeersPort
	}
	return 0
}

func (x *PeersSync) GetActor() *ActorRef {
	if x != nil {
		return x.Actor
	}
	return nil
}

type PeerState struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the peer host
	Host string `protobuf:"bytes,1,opt,name=host,proto3" json:"host,omitempty"`
	// Specifies the remoting port
	RemotingPort int32 `protobuf:"varint,2,opt,name=remoting_port,json=remotingPort,proto3" json:"remoting_port,omitempty"`
	// Specifies the remoting host
	PeersPort int32 `protobuf:"varint,3,opt,name=peers_port,json=peersPort,proto3" json:"peers_port,omitempty"`
	// Specifies the list of actors
	Actors        []*ActorRef `protobuf:"bytes,4,rep,name=actors,proto3" json:"actors,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PeerState) Reset() {
	*x = PeerState{}
	mi := &file_internal_peers_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PeerState) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PeerState) ProtoMessage() {}

func (x *PeerState) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PeerState.ProtoReflect.Descriptor instead.
func (*PeerState) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{1}
}

func (x *PeerState) GetHost() string {
	if x != nil {
		return x.Host
	}
	return ""
}

func (x *PeerState) GetRemotingPort() int32 {
	if x != nil {
		return x.RemotingPort
	}
	return 0
}

func (x *PeerState) GetPeersPort() int32 {
	if x != nil {
		return x.PeersPort
	}
	return 0
}

func (x *PeerState) GetActors() []*ActorRef {
	if x != nil {
		return x.Actors
	}
	return nil
}

type Rebalance struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the peer state
	PeerState     *PeerState `protobuf:"bytes,1,opt,name=peer_state,json=peerState,proto3" json:"peer_state,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Rebalance) Reset() {
	*x = Rebalance{}
	mi := &file_internal_peers_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Rebalance) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Rebalance) ProtoMessage() {}

func (x *Rebalance) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Rebalance.ProtoReflect.Descriptor instead.
func (*Rebalance) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{2}
}

func (x *Rebalance) GetPeerState() *PeerState {
	if x != nil {
		return x.PeerState
	}
	return nil
}

type RebalanceComplete struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the peer address
	PeerAddress   string `protobuf:"bytes,1,opt,name=peer_address,json=peerAddress,proto3" json:"peer_address,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RebalanceComplete) Reset() {
	*x = RebalanceComplete{}
	mi := &file_internal_peers_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RebalanceComplete) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RebalanceComplete) ProtoMessage() {}

func (x *RebalanceComplete) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RebalanceComplete.ProtoReflect.Descriptor instead.
func (*RebalanceComplete) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{3}
}

func (x *RebalanceComplete) GetPeerAddress() string {
	if x != nil {
		return x.PeerAddress
	}
	return ""
}

type PeerMeta struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the node name
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// Specifies the node host
	Host string `protobuf:"bytes,2,opt,name=host,proto3" json:"host,omitempty"`
	// Specifies the node port
	Port uint32 `protobuf:"varint,3,opt,name=port,proto3" json:"port,omitempty"`
	// Specifies the node discovery port
	DiscoveryPort uint32 `protobuf:"varint,4,opt,name=discovery_port,json=discoveryPort,proto3" json:"discovery_port,omitempty"`
	// Specifies the node remoting port
	RemotingPort uint32 `protobuf:"varint,5,opt,name=remoting_port,json=remotingPort,proto3" json:"remoting_port,omitempty"`
	// Specifies the creation time
	CreationTime  *timestamppb.Timestamp `protobuf:"bytes,6,opt,name=creation_time,json=creationTime,proto3" json:"creation_time,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PeerMeta) Reset() {
	*x = PeerMeta{}
	mi := &file_internal_peers_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PeerMeta) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PeerMeta) ProtoMessage() {}

func (x *PeerMeta) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PeerMeta.ProtoReflect.Descriptor instead.
func (*PeerMeta) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{4}
}

func (x *PeerMeta) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *PeerMeta) GetHost() string {
	if x != nil {
		return x.Host
	}
	return ""
}

func (x *PeerMeta) GetPort() uint32 {
	if x != nil {
		return x.Port
	}
	return 0
}

func (x *PeerMeta) GetDiscoveryPort() uint32 {
	if x != nil {
		return x.DiscoveryPort
	}
	return 0
}

func (x *PeerMeta) GetRemotingPort() uint32 {
	if x != nil {
		return x.RemotingPort
	}
	return 0
}

func (x *PeerMeta) GetCreationTime() *timestamppb.Timestamp {
	if x != nil {
		return x.CreationTime
	}
	return nil
}

type PutPeerStateRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the peer address
	PeerAddress string `protobuf:"bytes,1,opt,name=peer_address,json=peerAddress,proto3" json:"peer_address,omitempty"`
	// Specifies the peer state
	PeerState     *PeerState `protobuf:"bytes,2,opt,name=peer_state,json=peerState,proto3" json:"peer_state,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PutPeerStateRequest) Reset() {
	*x = PutPeerStateRequest{}
	mi := &file_internal_peers_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PutPeerStateRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PutPeerStateRequest) ProtoMessage() {}

func (x *PutPeerStateRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PutPeerStateRequest.ProtoReflect.Descriptor instead.
func (*PutPeerStateRequest) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{5}
}

func (x *PutPeerStateRequest) GetPeerAddress() string {
	if x != nil {
		return x.PeerAddress
	}
	return ""
}

func (x *PutPeerStateRequest) GetPeerState() *PeerState {
	if x != nil {
		return x.PeerState
	}
	return nil
}

type PutPeerStateResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the acknowledgement
	Ack           bool `protobuf:"varint,1,opt,name=ack,proto3" json:"ack,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PutPeerStateResponse) Reset() {
	*x = PutPeerStateResponse{}
	mi := &file_internal_peers_proto_msgTypes[6]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PutPeerStateResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PutPeerStateResponse) ProtoMessage() {}

func (x *PutPeerStateResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[6]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PutPeerStateResponse.ProtoReflect.Descriptor instead.
func (*PutPeerStateResponse) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{6}
}

func (x *PutPeerStateResponse) GetAck() bool {
	if x != nil {
		return x.Ack
	}
	return false
}

type DeletePeerStateRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the peer address
	PeerAddress   string `protobuf:"bytes,1,opt,name=peer_address,json=peerAddress,proto3" json:"peer_address,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeletePeerStateRequest) Reset() {
	*x = DeletePeerStateRequest{}
	mi := &file_internal_peers_proto_msgTypes[7]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeletePeerStateRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeletePeerStateRequest) ProtoMessage() {}

func (x *DeletePeerStateRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[7]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeletePeerStateRequest.ProtoReflect.Descriptor instead.
func (*DeletePeerStateRequest) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{7}
}

func (x *DeletePeerStateRequest) GetPeerAddress() string {
	if x != nil {
		return x.PeerAddress
	}
	return ""
}

type DeletePeerStateResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeletePeerStateResponse) Reset() {
	*x = DeletePeerStateResponse{}
	mi := &file_internal_peers_proto_msgTypes[8]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeletePeerStateResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeletePeerStateResponse) ProtoMessage() {}

func (x *DeletePeerStateResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[8]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeletePeerStateResponse.ProtoReflect.Descriptor instead.
func (*DeletePeerStateResponse) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{8}
}

type DeleteActorRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the peer address
	PeerAddress string `protobuf:"bytes,1,opt,name=peer_address,json=peerAddress,proto3" json:"peer_address,omitempty"`
	// Specifies the actor
	ActorRef      *ActorRef `protobuf:"bytes,2,opt,name=actor_ref,json=actorRef,proto3" json:"actor_ref,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeleteActorRequest) Reset() {
	*x = DeleteActorRequest{}
	mi := &file_internal_peers_proto_msgTypes[9]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeleteActorRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteActorRequest) ProtoMessage() {}

func (x *DeleteActorRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[9]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteActorRequest.ProtoReflect.Descriptor instead.
func (*DeleteActorRequest) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{9}
}

func (x *DeleteActorRequest) GetPeerAddress() string {
	if x != nil {
		return x.PeerAddress
	}
	return ""
}

func (x *DeleteActorRequest) GetActorRef() *ActorRef {
	if x != nil {
		return x.ActorRef
	}
	return nil
}

type DeleteActorResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeleteActorResponse) Reset() {
	*x = DeleteActorResponse{}
	mi := &file_internal_peers_proto_msgTypes[10]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeleteActorResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteActorResponse) ProtoMessage() {}

func (x *DeleteActorResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[10]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteActorResponse.ProtoReflect.Descriptor instead.
func (*DeleteActorResponse) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{10}
}

type PutJobKeysRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the peer address
	PeerAddress string `protobuf:"bytes,1,opt,name=peer_address,json=peerAddress,proto3" json:"peer_address,omitempty"`
	// Specifies the job keys
	JobKeys       []string `protobuf:"bytes,2,rep,name=job_keys,json=jobKeys,proto3" json:"job_keys,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PutJobKeysRequest) Reset() {
	*x = PutJobKeysRequest{}
	mi := &file_internal_peers_proto_msgTypes[11]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PutJobKeysRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PutJobKeysRequest) ProtoMessage() {}

func (x *PutJobKeysRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[11]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PutJobKeysRequest.ProtoReflect.Descriptor instead.
func (*PutJobKeysRequest) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{11}
}

func (x *PutJobKeysRequest) GetPeerAddress() string {
	if x != nil {
		return x.PeerAddress
	}
	return ""
}

func (x *PutJobKeysRequest) GetJobKeys() []string {
	if x != nil {
		return x.JobKeys
	}
	return nil
}

type PutJobKeysResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PutJobKeysResponse) Reset() {
	*x = PutJobKeysResponse{}
	mi := &file_internal_peers_proto_msgTypes[12]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PutJobKeysResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PutJobKeysResponse) ProtoMessage() {}

func (x *PutJobKeysResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[12]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PutJobKeysResponse.ProtoReflect.Descriptor instead.
func (*PutJobKeysResponse) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{12}
}

type DeleteJobKeyRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the peer address
	PeerAddress string `protobuf:"bytes,1,opt,name=peer_address,json=peerAddress,proto3" json:"peer_address,omitempty"`
	// Specifies the job key
	JobKey        string `protobuf:"bytes,2,opt,name=job_key,json=jobKey,proto3" json:"job_key,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeleteJobKeyRequest) Reset() {
	*x = DeleteJobKeyRequest{}
	mi := &file_internal_peers_proto_msgTypes[13]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeleteJobKeyRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteJobKeyRequest) ProtoMessage() {}

func (x *DeleteJobKeyRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[13]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteJobKeyRequest.ProtoReflect.Descriptor instead.
func (*DeleteJobKeyRequest) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{13}
}

func (x *DeleteJobKeyRequest) GetPeerAddress() string {
	if x != nil {
		return x.PeerAddress
	}
	return ""
}

func (x *DeleteJobKeyRequest) GetJobKey() string {
	if x != nil {
		return x.JobKey
	}
	return ""
}

type DeleteJobKeyResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *DeleteJobKeyResponse) Reset() {
	*x = DeleteJobKeyResponse{}
	mi := &file_internal_peers_proto_msgTypes[14]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *DeleteJobKeyResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*DeleteJobKeyResponse) ProtoMessage() {}

func (x *DeleteJobKeyResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_peers_proto_msgTypes[14]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use DeleteJobKeyResponse.ProtoReflect.Descriptor instead.
func (*DeleteJobKeyResponse) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{14}
}

var File_internal_peers_proto protoreflect.FileDescriptor

var file_internal_peers_proto_rawDesc = []byte{
	0x0a, 0x14, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x70, 0x65, 0x65, 0x72, 0x73,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0a, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c,
	0x70, 0x62, 0x1a, 0x1f, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x62, 0x75, 0x66, 0x2f, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x1a, 0x14, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x61, 0x63,
	0x74, 0x6f, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x8f, 0x01, 0x0a, 0x09, 0x50, 0x65,
	0x65, 0x72, 0x73, 0x53, 0x79, 0x6e, 0x63, 0x12, 0x12, 0x0a, 0x04, 0x68, 0x6f, 0x73, 0x74, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x68, 0x6f, 0x73, 0x74, 0x12, 0x23, 0x0a, 0x0d, 0x72,
	0x65, 0x6d, 0x6f, 0x74, 0x69, 0x6e, 0x67, 0x5f, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x05, 0x52, 0x0c, 0x72, 0x65, 0x6d, 0x6f, 0x74, 0x69, 0x6e, 0x67, 0x50, 0x6f, 0x72, 0x74,
	0x12, 0x1d, 0x0a, 0x0a, 0x70, 0x65, 0x65, 0x72, 0x73, 0x5f, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x03,
	0x20, 0x01, 0x28, 0x05, 0x52, 0x09, 0x70, 0x65, 0x65, 0x72, 0x73, 0x50, 0x6f, 0x72, 0x74, 0x12,
	0x2a, 0x0a, 0x05, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14,
	0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x41, 0x63, 0x74, 0x6f,
	0x72, 0x52, 0x65, 0x66, 0x52, 0x05, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x22, 0x91, 0x01, 0x0a, 0x09,
	0x50, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x68, 0x6f, 0x73,
	0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x68, 0x6f, 0x73, 0x74, 0x12, 0x23, 0x0a,
	0x0d, 0x72, 0x65, 0x6d, 0x6f, 0x74, 0x69, 0x6e, 0x67, 0x5f, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x02,
	0x20, 0x01, 0x28, 0x05, 0x52, 0x0c, 0x72, 0x65, 0x6d, 0x6f, 0x74, 0x69, 0x6e, 0x67, 0x50, 0x6f,
	0x72, 0x74, 0x12, 0x1d, 0x0a, 0x0a, 0x70, 0x65, 0x65, 0x72, 0x73, 0x5f, 0x70, 0x6f, 0x72, 0x74,
	0x18, 0x03, 0x20, 0x01, 0x28, 0x05, 0x52, 0x09, 0x70, 0x65, 0x65, 0x72, 0x73, 0x50, 0x6f, 0x72,
	0x74, 0x12, 0x2c, 0x0a, 0x06, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x18, 0x04, 0x20, 0x03, 0x28,
	0x0b, 0x32, 0x14, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x41,
	0x63, 0x74, 0x6f, 0x72, 0x52, 0x65, 0x66, 0x52, 0x06, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x22,
	0x41, 0x0a, 0x09, 0x52, 0x65, 0x62, 0x61, 0x6c, 0x61, 0x6e, 0x63, 0x65, 0x12, 0x34, 0x0a, 0x0a,
	0x70, 0x65, 0x65, 0x72, 0x5f, 0x73, 0x74, 0x61, 0x74, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x15, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x50, 0x65,
	0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x09, 0x70, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61,
	0x74, 0x65, 0x22, 0x36, 0x0a, 0x11, 0x52, 0x65, 0x62, 0x61, 0x6c, 0x61, 0x6e, 0x63, 0x65, 0x43,
	0x6f, 0x6d, 0x70, 0x6c, 0x65, 0x74, 0x65, 0x12, 0x21, 0x0a, 0x0c, 0x70, 0x65, 0x65, 0x72, 0x5f,
	0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x70,
	0x65, 0x65, 0x72, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x22, 0xd3, 0x01, 0x0a, 0x08, 0x50,
	0x65, 0x65, 0x72, 0x4d, 0x65, 0x74, 0x61, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x68,
	0x6f, 0x73, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x68, 0x6f, 0x73, 0x74, 0x12,
	0x12, 0x0a, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0d, 0x52, 0x04, 0x70,
	0x6f, 0x72, 0x74, 0x12, 0x25, 0x0a, 0x0e, 0x64, 0x69, 0x73, 0x63, 0x6f, 0x76, 0x65, 0x72, 0x79,
	0x5f, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0d, 0x52, 0x0d, 0x64, 0x69, 0x73,
	0x63, 0x6f, 0x76, 0x65, 0x72, 0x79, 0x50, 0x6f, 0x72, 0x74, 0x12, 0x23, 0x0a, 0x0d, 0x72, 0x65,
	0x6d, 0x6f, 0x74, 0x69, 0x6e, 0x67, 0x5f, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x05, 0x20, 0x01, 0x28,
	0x0d, 0x52, 0x0c, 0x72, 0x65, 0x6d, 0x6f, 0x74, 0x69, 0x6e, 0x67, 0x50, 0x6f, 0x72, 0x74, 0x12,
	0x3f, 0x0a, 0x0d, 0x63, 0x72, 0x65, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x5f, 0x74, 0x69, 0x6d, 0x65,
	0x18, 0x06, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61,
	0x6d, 0x70, 0x52, 0x0c, 0x63, 0x72, 0x65, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x54, 0x69, 0x6d, 0x65,
	0x22, 0x6e, 0x0a, 0x13, 0x50, 0x75, 0x74, 0x50, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x21, 0x0a, 0x0c, 0x70, 0x65, 0x65, 0x72, 0x5f,
	0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x70,
	0x65, 0x65, 0x72, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x34, 0x0a, 0x0a, 0x70, 0x65,
	0x65, 0x72, 0x5f, 0x73, 0x74, 0x61, 0x74, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x15,
	0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x50, 0x65, 0x65, 0x72,
	0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x09, 0x70, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65,
	0x22, 0x28, 0x0a, 0x14, 0x50, 0x75, 0x74, 0x50, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65,
	0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x10, 0x0a, 0x03, 0x61, 0x63, 0x6b, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x08, 0x52, 0x03, 0x61, 0x63, 0x6b, 0x22, 0x3b, 0x0a, 0x16, 0x44, 0x65,
	0x6c, 0x65, 0x74, 0x65, 0x50, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x12, 0x21, 0x0a, 0x0c, 0x70, 0x65, 0x65, 0x72, 0x5f, 0x61, 0x64, 0x64,
	0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x70, 0x65, 0x65, 0x72,
	0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x22, 0x19, 0x0a, 0x17, 0x44, 0x65, 0x6c, 0x65, 0x74,
	0x65, 0x50, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e,
	0x73, 0x65, 0x22, 0x6a, 0x0a, 0x12, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x41, 0x63, 0x74, 0x6f,
	0x72, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x21, 0x0a, 0x0c, 0x70, 0x65, 0x65, 0x72,
	0x5f, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b,
	0x70, 0x65, 0x65, 0x72, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x31, 0x0a, 0x09, 0x61,
	0x63, 0x74, 0x6f, 0x72, 0x5f, 0x72, 0x65, 0x66, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14,
	0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x41, 0x63, 0x74, 0x6f,
	0x72, 0x52, 0x65, 0x66, 0x52, 0x08, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x52, 0x65, 0x66, 0x22, 0x15,
	0x0a, 0x13, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x41, 0x63, 0x74, 0x6f, 0x72, 0x52, 0x65, 0x73,
	0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x51, 0x0a, 0x11, 0x50, 0x75, 0x74, 0x4a, 0x6f, 0x62, 0x4b,
	0x65, 0x79, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x21, 0x0a, 0x0c, 0x70, 0x65,
	0x65, 0x72, 0x5f, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x0b, 0x70, 0x65, 0x65, 0x72, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x19, 0x0a,
	0x08, 0x6a, 0x6f, 0x62, 0x5f, 0x6b, 0x65, 0x79, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x09, 0x52,
	0x07, 0x6a, 0x6f, 0x62, 0x4b, 0x65, 0x79, 0x73, 0x22, 0x14, 0x0a, 0x12, 0x50, 0x75, 0x74, 0x4a,
	0x6f, 0x62, 0x4b, 0x65, 0x79, 0x73, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x51,
	0x0a, 0x13, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x4a, 0x6f, 0x62, 0x4b, 0x65, 0x79, 0x52, 0x65,
	0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x21, 0x0a, 0x0c, 0x70, 0x65, 0x65, 0x72, 0x5f, 0x61, 0x64,
	0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x70, 0x65, 0x65,
	0x72, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x17, 0x0a, 0x07, 0x6a, 0x6f, 0x62, 0x5f,
	0x6b, 0x65, 0x79, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x06, 0x6a, 0x6f, 0x62, 0x4b, 0x65,
	0x79, 0x22, 0x16, 0x0a, 0x14, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x4a, 0x6f, 0x62, 0x4b, 0x65,
	0x79, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x32, 0xad, 0x03, 0x0a, 0x0c, 0x50, 0x65,
	0x65, 0x72, 0x73, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x51, 0x0a, 0x0c, 0x50, 0x75,
	0x74, 0x50, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65, 0x12, 0x1f, 0x2e, 0x69, 0x6e, 0x74,
	0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x50, 0x75, 0x74, 0x50, 0x65, 0x65, 0x72, 0x53,
	0x74, 0x61, 0x74, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x20, 0x2e, 0x69, 0x6e,
	0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x50, 0x75, 0x74, 0x50, 0x65, 0x65, 0x72,
	0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x5a, 0x0a,
	0x0f, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x50, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65,
	0x12, 0x22, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x44, 0x65,
	0x6c, 0x65, 0x74, 0x65, 0x50, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x65, 0x71,
	0x75, 0x65, 0x73, 0x74, 0x1a, 0x23, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70,
	0x62, 0x2e, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x50, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74,
	0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x4e, 0x0a, 0x0b, 0x44, 0x65, 0x6c,
	0x65, 0x74, 0x65, 0x41, 0x63, 0x74, 0x6f, 0x72, 0x12, 0x1e, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72,
	0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x41, 0x63, 0x74, 0x6f,
	0x72, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x1f, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72,
	0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x41, 0x63, 0x74, 0x6f,
	0x72, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x4b, 0x0a, 0x0a, 0x50, 0x75, 0x74,
	0x4a, 0x6f, 0x62, 0x4b, 0x65, 0x79, 0x73, 0x12, 0x1d, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e,
	0x61, 0x6c, 0x70, 0x62, 0x2e, 0x50, 0x75, 0x74, 0x4a, 0x6f, 0x62, 0x4b, 0x65, 0x79, 0x73, 0x52,
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x1e, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61,
	0x6c, 0x70, 0x62, 0x2e, 0x50, 0x75, 0x74, 0x4a, 0x6f, 0x62, 0x4b, 0x65, 0x79, 0x73, 0x52, 0x65,
	0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x51, 0x0a, 0x0c, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65,
	0x4a, 0x6f, 0x62, 0x4b, 0x65, 0x79, 0x12, 0x1f, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61,
	0x6c, 0x70, 0x62, 0x2e, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x4a, 0x6f, 0x62, 0x4b, 0x65, 0x79,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x20, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e,
	0x61, 0x6c, 0x70, 0x62, 0x2e, 0x44, 0x65, 0x6c, 0x65, 0x74, 0x65, 0x4a, 0x6f, 0x62, 0x4b, 0x65,
	0x79, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x42, 0xa3, 0x01, 0x0a, 0x0e, 0x63, 0x6f,
	0x6d, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x42, 0x0a, 0x50, 0x65,
	0x65, 0x72, 0x73, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x48, 0x02, 0x50, 0x01, 0x5a, 0x3b, 0x67, 0x69,
	0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x74, 0x6f, 0x63, 0x68, 0x65, 0x6d, 0x65,
	0x79, 0x2f, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2f, 0x76, 0x32, 0x2f, 0x69, 0x6e, 0x74, 0x65, 0x72,
	0x6e, 0x61, 0x6c, 0x2f, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x3b, 0x69,
	0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xa2, 0x02, 0x03, 0x49, 0x58, 0x58, 0xaa,
	0x02, 0x0a, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xca, 0x02, 0x0a, 0x49,
	0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xe2, 0x02, 0x16, 0x49, 0x6e, 0x74, 0x65,
	0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61,
	0x74, 0x61, 0xea, 0x02, 0x0a, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x62,
	0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_internal_peers_proto_rawDescOnce sync.Once
	file_internal_peers_proto_rawDescData = file_internal_peers_proto_rawDesc
)

func file_internal_peers_proto_rawDescGZIP() []byte {
	file_internal_peers_proto_rawDescOnce.Do(func() {
		file_internal_peers_proto_rawDescData = protoimpl.X.CompressGZIP(file_internal_peers_proto_rawDescData)
	})
	return file_internal_peers_proto_rawDescData
}

var file_internal_peers_proto_msgTypes = make([]protoimpl.MessageInfo, 15)
var file_internal_peers_proto_goTypes = []any{
	(*PeersSync)(nil),               // 0: internalpb.PeersSync
	(*PeerState)(nil),               // 1: internalpb.PeerState
	(*Rebalance)(nil),               // 2: internalpb.Rebalance
	(*RebalanceComplete)(nil),       // 3: internalpb.RebalanceComplete
	(*PeerMeta)(nil),                // 4: internalpb.PeerMeta
	(*PutPeerStateRequest)(nil),     // 5: internalpb.PutPeerStateRequest
	(*PutPeerStateResponse)(nil),    // 6: internalpb.PutPeerStateResponse
	(*DeletePeerStateRequest)(nil),  // 7: internalpb.DeletePeerStateRequest
	(*DeletePeerStateResponse)(nil), // 8: internalpb.DeletePeerStateResponse
	(*DeleteActorRequest)(nil),      // 9: internalpb.DeleteActorRequest
	(*DeleteActorResponse)(nil),     // 10: internalpb.DeleteActorResponse
	(*PutJobKeysRequest)(nil),       // 11: internalpb.PutJobKeysRequest
	(*PutJobKeysResponse)(nil),      // 12: internalpb.PutJobKeysResponse
	(*DeleteJobKeyRequest)(nil),     // 13: internalpb.DeleteJobKeyRequest
	(*DeleteJobKeyResponse)(nil),    // 14: internalpb.DeleteJobKeyResponse
	(*ActorRef)(nil),                // 15: internalpb.ActorRef
	(*timestamppb.Timestamp)(nil),   // 16: google.protobuf.Timestamp
}
var file_internal_peers_proto_depIdxs = []int32{
	15, // 0: internalpb.PeersSync.actor:type_name -> internalpb.ActorRef
	15, // 1: internalpb.PeerState.actors:type_name -> internalpb.ActorRef
	1,  // 2: internalpb.Rebalance.peer_state:type_name -> internalpb.PeerState
	16, // 3: internalpb.PeerMeta.creation_time:type_name -> google.protobuf.Timestamp
	1,  // 4: internalpb.PutPeerStateRequest.peer_state:type_name -> internalpb.PeerState
	15, // 5: internalpb.DeleteActorRequest.actor_ref:type_name -> internalpb.ActorRef
	5,  // 6: internalpb.PeersService.PutPeerState:input_type -> internalpb.PutPeerStateRequest
	7,  // 7: internalpb.PeersService.DeletePeerState:input_type -> internalpb.DeletePeerStateRequest
	9,  // 8: internalpb.PeersService.DeleteActor:input_type -> internalpb.DeleteActorRequest
	11, // 9: internalpb.PeersService.PutJobKeys:input_type -> internalpb.PutJobKeysRequest
	13, // 10: internalpb.PeersService.DeleteJobKey:input_type -> internalpb.DeleteJobKeyRequest
	6,  // 11: internalpb.PeersService.PutPeerState:output_type -> internalpb.PutPeerStateResponse
	8,  // 12: internalpb.PeersService.DeletePeerState:output_type -> internalpb.DeletePeerStateResponse
	10, // 13: internalpb.PeersService.DeleteActor:output_type -> internalpb.DeleteActorResponse
	12, // 14: internalpb.PeersService.PutJobKeys:output_type -> internalpb.PutJobKeysResponse
	14, // 15: internalpb.PeersService.DeleteJobKey:output_type -> internalpb.DeleteJobKeyResponse
	11, // [11:16] is the sub-list for method output_type
	6,  // [6:11] is the sub-list for method input_type
	6,  // [6:6] is the sub-list for extension type_name
	6,  // [6:6] is the sub-list for extension extendee
	0,  // [0:6] is the sub-list for field type_name
}

func init() { file_internal_peers_proto_init() }
func file_internal_peers_proto_init() {
	if File_internal_peers_proto != nil {
		return
	}
	file_internal_actor_proto_init()
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_internal_peers_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   15,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_internal_peers_proto_goTypes,
		DependencyIndexes: file_internal_peers_proto_depIdxs,
		MessageInfos:      file_internal_peers_proto_msgTypes,
	}.Build()
	File_internal_peers_proto = out.File
	file_internal_peers_proto_rawDesc = nil
	file_internal_peers_proto_goTypes = nil
	file_internal_peers_proto_depIdxs = nil
}
