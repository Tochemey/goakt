// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.5
// 	protoc        (unknown)
// source: internal/peers.proto

package internalpb

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
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

type PeerState struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the peer host
	Host string `protobuf:"bytes,1,opt,name=host,proto3" json:"host,omitempty"`
	// Specifies the remoting port
	RemotingPort int32 `protobuf:"varint,2,opt,name=remoting_port,json=remotingPort,proto3" json:"remoting_port,omitempty"`
	// Specifies the remoting host
	PeersPort int32 `protobuf:"varint,3,opt,name=peers_port,json=peersPort,proto3" json:"peers_port,omitempty"`
	// Specifies the list of actors
	// actorName -> actorKind
	Actors        map[string]string `protobuf:"bytes,4,rep,name=actors,proto3" json:"actors,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"bytes,2,opt,name=value"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PeerState) Reset() {
	*x = PeerState{}
	mi := &file_internal_peers_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PeerState) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PeerState) ProtoMessage() {}

func (x *PeerState) ProtoReflect() protoreflect.Message {
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

// Deprecated: Use PeerState.ProtoReflect.Descriptor instead.
func (*PeerState) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{0}
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

func (x *PeerState) GetActors() map[string]string {
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
	mi := &file_internal_peers_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Rebalance) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Rebalance) ProtoMessage() {}

func (x *Rebalance) ProtoReflect() protoreflect.Message {
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

// Deprecated: Use Rebalance.ProtoReflect.Descriptor instead.
func (*Rebalance) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{1}
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
	mi := &file_internal_peers_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RebalanceComplete) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RebalanceComplete) ProtoMessage() {}

func (x *RebalanceComplete) ProtoReflect() protoreflect.Message {
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

// Deprecated: Use RebalanceComplete.ProtoReflect.Descriptor instead.
func (*RebalanceComplete) Descriptor() ([]byte, []int) {
	return file_internal_peers_proto_rawDescGZIP(), []int{2}
}

func (x *RebalanceComplete) GetPeerAddress() string {
	if x != nil {
		return x.PeerAddress
	}
	return ""
}

var File_internal_peers_proto protoreflect.FileDescriptor

var file_internal_peers_proto_rawDesc = string([]byte{
	0x0a, 0x14, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x70, 0x65, 0x65, 0x72, 0x73,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0a, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c,
	0x70, 0x62, 0x22, 0xd9, 0x01, 0x0a, 0x09, 0x50, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65,
	0x12, 0x12, 0x0a, 0x04, 0x68, 0x6f, 0x73, 0x74, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04,
	0x68, 0x6f, 0x73, 0x74, 0x12, 0x23, 0x0a, 0x0d, 0x72, 0x65, 0x6d, 0x6f, 0x74, 0x69, 0x6e, 0x67,
	0x5f, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x05, 0x52, 0x0c, 0x72, 0x65, 0x6d,
	0x6f, 0x74, 0x69, 0x6e, 0x67, 0x50, 0x6f, 0x72, 0x74, 0x12, 0x1d, 0x0a, 0x0a, 0x70, 0x65, 0x65,
	0x72, 0x73, 0x5f, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x03, 0x20, 0x01, 0x28, 0x05, 0x52, 0x09, 0x70,
	0x65, 0x65, 0x72, 0x73, 0x50, 0x6f, 0x72, 0x74, 0x12, 0x39, 0x0a, 0x06, 0x61, 0x63, 0x74, 0x6f,
	0x72, 0x73, 0x18, 0x04, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x21, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72,
	0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x50, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65, 0x2e,
	0x41, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x52, 0x06, 0x61, 0x63, 0x74,
	0x6f, 0x72, 0x73, 0x1a, 0x39, 0x0a, 0x0b, 0x41, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x45, 0x6e, 0x74,
	0x72, 0x79, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x03, 0x6b, 0x65, 0x79, 0x12, 0x14, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x3a, 0x02, 0x38, 0x01, 0x22, 0x41,
	0x0a, 0x09, 0x52, 0x65, 0x62, 0x61, 0x6c, 0x61, 0x6e, 0x63, 0x65, 0x12, 0x34, 0x0a, 0x0a, 0x70,
	0x65, 0x65, 0x72, 0x5f, 0x73, 0x74, 0x61, 0x74, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32,
	0x15, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x50, 0x65, 0x65,
	0x72, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x09, 0x70, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74,
	0x65, 0x22, 0x36, 0x0a, 0x11, 0x52, 0x65, 0x62, 0x61, 0x6c, 0x61, 0x6e, 0x63, 0x65, 0x43, 0x6f,
	0x6d, 0x70, 0x6c, 0x65, 0x74, 0x65, 0x12, 0x21, 0x0a, 0x0c, 0x70, 0x65, 0x65, 0x72, 0x5f, 0x61,
	0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x70, 0x65,
	0x65, 0x72, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x42, 0xa3, 0x01, 0x0a, 0x0e, 0x63, 0x6f,
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
})

var (
	file_internal_peers_proto_rawDescOnce sync.Once
	file_internal_peers_proto_rawDescData []byte
)

func file_internal_peers_proto_rawDescGZIP() []byte {
	file_internal_peers_proto_rawDescOnce.Do(func() {
		file_internal_peers_proto_rawDescData = protoimpl.X.CompressGZIP(unsafe.Slice(unsafe.StringData(file_internal_peers_proto_rawDesc), len(file_internal_peers_proto_rawDesc)))
	})
	return file_internal_peers_proto_rawDescData
}

var file_internal_peers_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_internal_peers_proto_goTypes = []any{
	(*PeerState)(nil),         // 0: internalpb.PeerState
	(*Rebalance)(nil),         // 1: internalpb.Rebalance
	(*RebalanceComplete)(nil), // 2: internalpb.RebalanceComplete
	nil,                       // 3: internalpb.PeerState.ActorsEntry
}
var file_internal_peers_proto_depIdxs = []int32{
	3, // 0: internalpb.PeerState.actors:type_name -> internalpb.PeerState.ActorsEntry
	0, // 1: internalpb.Rebalance.peer_state:type_name -> internalpb.PeerState
	2, // [2:2] is the sub-list for method output_type
	2, // [2:2] is the sub-list for method input_type
	2, // [2:2] is the sub-list for extension type_name
	2, // [2:2] is the sub-list for extension extendee
	0, // [0:2] is the sub-list for field type_name
}

func init() { file_internal_peers_proto_init() }
func file_internal_peers_proto_init() {
	if File_internal_peers_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: unsafe.Slice(unsafe.StringData(file_internal_peers_proto_rawDesc), len(file_internal_peers_proto_rawDesc)),
			NumEnums:      0,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_internal_peers_proto_goTypes,
		DependencyIndexes: file_internal_peers_proto_depIdxs,
		MessageInfos:      file_internal_peers_proto_msgTypes,
	}.Build()
	File_internal_peers_proto = out.File
	file_internal_peers_proto_goTypes = nil
	file_internal_peers_proto_depIdxs = nil
}
