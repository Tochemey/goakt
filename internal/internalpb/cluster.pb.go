// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.31.0
// 	protoc        (unknown)
// source: internal/cluster.proto

package internalpb

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type PushStateRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the node state
	PeerState *PeerState `protobuf:"bytes,1,opt,name=peer_state,json=peerState,proto3" json:"peer_state,omitempty"`
}

func (x *PushStateRequest) Reset() {
	*x = PushStateRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_internal_cluster_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PushStateRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PushStateRequest) ProtoMessage() {}

func (x *PushStateRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_cluster_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PushStateRequest.ProtoReflect.Descriptor instead.
func (*PushStateRequest) Descriptor() ([]byte, []int) {
	return file_internal_cluster_proto_rawDescGZIP(), []int{0}
}

func (x *PushStateRequest) GetPeerState() *PeerState {
	if x != nil {
		return x.PeerState
	}
	return nil
}

type PushStateResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *PushStateResponse) Reset() {
	*x = PushStateResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_internal_cluster_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PushStateResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PushStateResponse) ProtoMessage() {}

func (x *PushStateResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_cluster_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PushStateResponse.ProtoReflect.Descriptor instead.
func (*PushStateResponse) Descriptor() ([]byte, []int) {
	return file_internal_cluster_proto_rawDescGZIP(), []int{1}
}

// NodeState is used to replicate their actors map to their peers
// in the cluster
type PeerState struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the node address
	Address string `protobuf:"bytes,1,opt,name=address,proto3" json:"address,omitempty"`
	// Specifies the list of actors
	Actors []*WireActor `protobuf:"bytes,2,rep,name=actors,proto3" json:"actors,omitempty"`
}

func (x *PeerState) Reset() {
	*x = PeerState{}
	if protoimpl.UnsafeEnabled {
		mi := &file_internal_cluster_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PeerState) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PeerState) ProtoMessage() {}

func (x *PeerState) ProtoReflect() protoreflect.Message {
	mi := &file_internal_cluster_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
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
	return file_internal_cluster_proto_rawDescGZIP(), []int{2}
}

func (x *PeerState) GetAddress() string {
	if x != nil {
		return x.Address
	}
	return ""
}

func (x *PeerState) GetActors() []*WireActor {
	if x != nil {
		return x.Actors
	}
	return nil
}

var File_internal_cluster_proto protoreflect.FileDescriptor

var file_internal_cluster_proto_rawDesc = []byte{
	0x0a, 0x16, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x63, 0x6c, 0x75, 0x73, 0x74,
	0x65, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0a, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e,
	0x61, 0x6c, 0x70, 0x62, 0x1a, 0x18, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x77,
	0x69, 0x72, 0x65, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x48,
	0x0a, 0x10, 0x50, 0x75, 0x73, 0x68, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x12, 0x34, 0x0a, 0x0a, 0x70, 0x65, 0x65, 0x72, 0x5f, 0x73, 0x74, 0x61, 0x74, 0x65,
	0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x15, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61,
	0x6c, 0x70, 0x62, 0x2e, 0x50, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x09, 0x70,
	0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65, 0x22, 0x13, 0x0a, 0x11, 0x50, 0x75, 0x73, 0x68,
	0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x54, 0x0a,
	0x09, 0x50, 0x65, 0x65, 0x72, 0x53, 0x74, 0x61, 0x74, 0x65, 0x12, 0x18, 0x0a, 0x07, 0x61, 0x64,
	0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x61, 0x64, 0x64,
	0x72, 0x65, 0x73, 0x73, 0x12, 0x2d, 0x0a, 0x06, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x18, 0x02,
	0x20, 0x03, 0x28, 0x0b, 0x32, 0x15, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70,
	0x62, 0x2e, 0x57, 0x69, 0x72, 0x65, 0x41, 0x63, 0x74, 0x6f, 0x72, 0x52, 0x06, 0x61, 0x63, 0x74,
	0x6f, 0x72, 0x73, 0x32, 0x5a, 0x0a, 0x0e, 0x43, 0x6c, 0x75, 0x73, 0x74, 0x65, 0x72, 0x53, 0x65,
	0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x48, 0x0a, 0x09, 0x50, 0x75, 0x73, 0x68, 0x53, 0x74, 0x61,
	0x74, 0x65, 0x12, 0x1c, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e,
	0x50, 0x75, 0x73, 0x68, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74,
	0x1a, 0x1d, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x50, 0x75,
	0x73, 0x68, 0x53, 0x74, 0x61, 0x74, 0x65, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x42,
	0xa2, 0x01, 0x0a, 0x0e, 0x63, 0x6f, 0x6d, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c,
	0x70, 0x62, 0x42, 0x0c, 0x43, 0x6c, 0x75, 0x73, 0x74, 0x65, 0x72, 0x50, 0x72, 0x6f, 0x74, 0x6f,
	0x48, 0x02, 0x50, 0x01, 0x5a, 0x38, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d,
	0x2f, 0x74, 0x6f, 0x63, 0x68, 0x65, 0x6d, 0x65, 0x79, 0x2f, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2f,
	0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61,
	0x6c, 0x70, 0x62, 0x3b, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xa2, 0x02,
	0x03, 0x49, 0x58, 0x58, 0xaa, 0x02, 0x0a, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70,
	0x62, 0xca, 0x02, 0x0a, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xe2, 0x02,
	0x16, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x5c, 0x47, 0x50, 0x42, 0x4d,
	0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x0a, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e,
	0x61, 0x6c, 0x70, 0x62, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_internal_cluster_proto_rawDescOnce sync.Once
	file_internal_cluster_proto_rawDescData = file_internal_cluster_proto_rawDesc
)

func file_internal_cluster_proto_rawDescGZIP() []byte {
	file_internal_cluster_proto_rawDescOnce.Do(func() {
		file_internal_cluster_proto_rawDescData = protoimpl.X.CompressGZIP(file_internal_cluster_proto_rawDescData)
	})
	return file_internal_cluster_proto_rawDescData
}

var file_internal_cluster_proto_msgTypes = make([]protoimpl.MessageInfo, 3)
var file_internal_cluster_proto_goTypes = []interface{}{
	(*PushStateRequest)(nil),  // 0: internalpb.PushStateRequest
	(*PushStateResponse)(nil), // 1: internalpb.PushStateResponse
	(*PeerState)(nil),         // 2: internalpb.PeerState
	(*WireActor)(nil),         // 3: internalpb.WireActor
}
var file_internal_cluster_proto_depIdxs = []int32{
	2, // 0: internalpb.PushStateRequest.peer_state:type_name -> internalpb.PeerState
	3, // 1: internalpb.PeerState.actors:type_name -> internalpb.WireActor
	0, // 2: internalpb.ClusterService.PushState:input_type -> internalpb.PushStateRequest
	1, // 3: internalpb.ClusterService.PushState:output_type -> internalpb.PushStateResponse
	3, // [3:4] is the sub-list for method output_type
	2, // [2:3] is the sub-list for method input_type
	2, // [2:2] is the sub-list for extension type_name
	2, // [2:2] is the sub-list for extension extendee
	0, // [0:2] is the sub-list for field type_name
}

func init() { file_internal_cluster_proto_init() }
func file_internal_cluster_proto_init() {
	if File_internal_cluster_proto != nil {
		return
	}
	file_internal_wireactor_proto_init()
	if !protoimpl.UnsafeEnabled {
		file_internal_cluster_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PushStateRequest); i {
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
		file_internal_cluster_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PushStateResponse); i {
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
		file_internal_cluster_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PeerState); i {
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
			RawDescriptor: file_internal_cluster_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   3,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_internal_cluster_proto_goTypes,
		DependencyIndexes: file_internal_cluster_proto_depIdxs,
		MessageInfos:      file_internal_cluster_proto_msgTypes,
	}.Build()
	File_internal_cluster_proto = out.File
	file_internal_cluster_proto_rawDesc = nil
	file_internal_cluster_proto_goTypes = nil
	file_internal_cluster_proto_depIdxs = nil
}
