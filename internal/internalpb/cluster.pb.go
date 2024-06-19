// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.33.0
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

type GetNodeMetricRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the node address
	NodeAddress string `protobuf:"bytes,1,opt,name=node_address,json=nodeAddress,proto3" json:"node_address,omitempty"`
}

func (x *GetNodeMetricRequest) Reset() {
	*x = GetNodeMetricRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_internal_cluster_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GetNodeMetricRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetNodeMetricRequest) ProtoMessage() {}

func (x *GetNodeMetricRequest) ProtoReflect() protoreflect.Message {
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

// Deprecated: Use GetNodeMetricRequest.ProtoReflect.Descriptor instead.
func (*GetNodeMetricRequest) Descriptor() ([]byte, []int) {
	return file_internal_cluster_proto_rawDescGZIP(), []int{0}
}

func (x *GetNodeMetricRequest) GetNodeAddress() string {
	if x != nil {
		return x.NodeAddress
	}
	return ""
}

type GetNodeMetricResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the node address
	NodeRemoteAddress string `protobuf:"bytes,1,opt,name=node_remote_address,json=nodeRemoteAddress,proto3" json:"node_remote_address,omitempty"`
	// Specifies the actors count for the given node
	ActorsCount uint64 `protobuf:"varint,2,opt,name=actors_count,json=actorsCount,proto3" json:"actors_count,omitempty"`
}

func (x *GetNodeMetricResponse) Reset() {
	*x = GetNodeMetricResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_internal_cluster_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GetNodeMetricResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetNodeMetricResponse) ProtoMessage() {}

func (x *GetNodeMetricResponse) ProtoReflect() protoreflect.Message {
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

// Deprecated: Use GetNodeMetricResponse.ProtoReflect.Descriptor instead.
func (*GetNodeMetricResponse) Descriptor() ([]byte, []int) {
	return file_internal_cluster_proto_rawDescGZIP(), []int{1}
}

func (x *GetNodeMetricResponse) GetNodeRemoteAddress() string {
	if x != nil {
		return x.NodeRemoteAddress
	}
	return ""
}

func (x *GetNodeMetricResponse) GetActorsCount() uint64 {
	if x != nil {
		return x.ActorsCount
	}
	return 0
}

type GetKindsRequest struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *GetKindsRequest) Reset() {
	*x = GetKindsRequest{}
	if protoimpl.UnsafeEnabled {
		mi := &file_internal_cluster_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GetKindsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetKindsRequest) ProtoMessage() {}

func (x *GetKindsRequest) ProtoReflect() protoreflect.Message {
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

// Deprecated: Use GetKindsRequest.ProtoReflect.Descriptor instead.
func (*GetKindsRequest) Descriptor() ([]byte, []int) {
	return file_internal_cluster_proto_rawDescGZIP(), []int{2}
}

type GetKindsResponse struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the list of kinds
	Kinds []string `protobuf:"bytes,1,rep,name=kinds,proto3" json:"kinds,omitempty"`
}

func (x *GetKindsResponse) Reset() {
	*x = GetKindsResponse{}
	if protoimpl.UnsafeEnabled {
		mi := &file_internal_cluster_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *GetKindsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetKindsResponse) ProtoMessage() {}

func (x *GetKindsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_cluster_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use GetKindsResponse.ProtoReflect.Descriptor instead.
func (*GetKindsResponse) Descriptor() ([]byte, []int) {
	return file_internal_cluster_proto_rawDescGZIP(), []int{3}
}

func (x *GetKindsResponse) GetKinds() []string {
	if x != nil {
		return x.Kinds
	}
	return nil
}

var File_internal_cluster_proto protoreflect.FileDescriptor

var file_internal_cluster_proto_rawDesc = []byte{
	0x0a, 0x16, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x63, 0x6c, 0x75, 0x73, 0x74,
	0x65, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0a, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e,
	0x61, 0x6c, 0x70, 0x62, 0x22, 0x39, 0x0a, 0x14, 0x47, 0x65, 0x74, 0x4e, 0x6f, 0x64, 0x65, 0x4d,
	0x65, 0x74, 0x72, 0x69, 0x63, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x21, 0x0a, 0x0c,
	0x6e, 0x6f, 0x64, 0x65, 0x5f, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x0b, 0x6e, 0x6f, 0x64, 0x65, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x22,
	0x6a, 0x0a, 0x15, 0x47, 0x65, 0x74, 0x4e, 0x6f, 0x64, 0x65, 0x4d, 0x65, 0x74, 0x72, 0x69, 0x63,
	0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x2e, 0x0a, 0x13, 0x6e, 0x6f, 0x64, 0x65,
	0x5f, 0x72, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x5f, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18,
	0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x11, 0x6e, 0x6f, 0x64, 0x65, 0x52, 0x65, 0x6d, 0x6f, 0x74,
	0x65, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x21, 0x0a, 0x0c, 0x61, 0x63, 0x74, 0x6f,
	0x72, 0x73, 0x5f, 0x63, 0x6f, 0x75, 0x6e, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x04, 0x52, 0x0b,
	0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x43, 0x6f, 0x75, 0x6e, 0x74, 0x22, 0x11, 0x0a, 0x0f, 0x47,
	0x65, 0x74, 0x4b, 0x69, 0x6e, 0x64, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x22, 0x28,
	0x0a, 0x10, 0x47, 0x65, 0x74, 0x4b, 0x69, 0x6e, 0x64, 0x73, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e,
	0x73, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x6b, 0x69, 0x6e, 0x64, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28,
	0x09, 0x52, 0x05, 0x6b, 0x69, 0x6e, 0x64, 0x73, 0x32, 0xad, 0x01, 0x0a, 0x0e, 0x43, 0x6c, 0x75,
	0x73, 0x74, 0x65, 0x72, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x54, 0x0a, 0x0d, 0x47,
	0x65, 0x74, 0x4e, 0x6f, 0x64, 0x65, 0x4d, 0x65, 0x74, 0x72, 0x69, 0x63, 0x12, 0x20, 0x2e, 0x69,
	0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x47, 0x65, 0x74, 0x4e, 0x6f, 0x64,
	0x65, 0x4d, 0x65, 0x74, 0x72, 0x69, 0x63, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x21,
	0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x47, 0x65, 0x74, 0x4e,
	0x6f, 0x64, 0x65, 0x4d, 0x65, 0x74, 0x72, 0x69, 0x63, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73,
	0x65, 0x12, 0x45, 0x0a, 0x08, 0x47, 0x65, 0x74, 0x4b, 0x69, 0x6e, 0x64, 0x73, 0x12, 0x1b, 0x2e,
	0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x47, 0x65, 0x74, 0x4b, 0x69,
	0x6e, 0x64, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x1c, 0x2e, 0x69, 0x6e, 0x74,
	0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x47, 0x65, 0x74, 0x4b, 0x69, 0x6e, 0x64, 0x73,
	0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x42, 0xa5, 0x01, 0x0a, 0x0e, 0x63, 0x6f, 0x6d,
	0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x42, 0x0c, 0x43, 0x6c, 0x75,
	0x73, 0x74, 0x65, 0x72, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x48, 0x02, 0x50, 0x01, 0x5a, 0x3b, 0x67,
	0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x74, 0x6f, 0x63, 0x68, 0x65, 0x6d,
	0x65, 0x79, 0x2f, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2f, 0x76, 0x32, 0x2f, 0x69, 0x6e, 0x74, 0x65,
	0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x3b,
	0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xa2, 0x02, 0x03, 0x49, 0x58, 0x58,
	0xaa, 0x02, 0x0a, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xca, 0x02, 0x0a,
	0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xe2, 0x02, 0x16, 0x49, 0x6e, 0x74,
	0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64,
	0x61, 0x74, 0x61, 0xea, 0x02, 0x0a, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62,
	0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
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

var file_internal_cluster_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_internal_cluster_proto_goTypes = []interface{}{
	(*GetNodeMetricRequest)(nil),  // 0: internalpb.GetNodeMetricRequest
	(*GetNodeMetricResponse)(nil), // 1: internalpb.GetNodeMetricResponse
	(*GetKindsRequest)(nil),       // 2: internalpb.GetKindsRequest
	(*GetKindsResponse)(nil),      // 3: internalpb.GetKindsResponse
}
var file_internal_cluster_proto_depIdxs = []int32{
	0, // 0: internalpb.ClusterService.GetNodeMetric:input_type -> internalpb.GetNodeMetricRequest
	2, // 1: internalpb.ClusterService.GetKinds:input_type -> internalpb.GetKindsRequest
	1, // 2: internalpb.ClusterService.GetNodeMetric:output_type -> internalpb.GetNodeMetricResponse
	3, // 3: internalpb.ClusterService.GetKinds:output_type -> internalpb.GetKindsResponse
	2, // [2:4] is the sub-list for method output_type
	0, // [0:2] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_internal_cluster_proto_init() }
func file_internal_cluster_proto_init() {
	if File_internal_cluster_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_internal_cluster_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GetNodeMetricRequest); i {
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
			switch v := v.(*GetNodeMetricResponse); i {
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
			switch v := v.(*GetKindsRequest); i {
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
		file_internal_cluster_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*GetKindsResponse); i {
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
			NumMessages:   4,
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
