// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.5
// 	protoc        (unknown)
// source: internal/cluster.proto

package internalpb

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	anypb "google.golang.org/protobuf/types/known/anypb"
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

type GetNodeMetricRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the node address
	NodeAddress   string `protobuf:"bytes,1,opt,name=node_address,json=nodeAddress,proto3" json:"node_address,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetNodeMetricRequest) Reset() {
	*x = GetNodeMetricRequest{}
	mi := &file_internal_cluster_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetNodeMetricRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetNodeMetricRequest) ProtoMessage() {}

func (x *GetNodeMetricRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_cluster_proto_msgTypes[0]
	if x != nil {
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
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the node address
	NodeRemoteAddress string `protobuf:"bytes,1,opt,name=node_remote_address,json=nodeRemoteAddress,proto3" json:"node_remote_address,omitempty"`
	// Specifies the actors count for the given node
	ActorsCount   uint64 `protobuf:"varint,2,opt,name=actors_count,json=actorsCount,proto3" json:"actors_count,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetNodeMetricResponse) Reset() {
	*x = GetNodeMetricResponse{}
	mi := &file_internal_cluster_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetNodeMetricResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetNodeMetricResponse) ProtoMessage() {}

func (x *GetNodeMetricResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_cluster_proto_msgTypes[1]
	if x != nil {
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
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the node address
	NodeAddress   string `protobuf:"bytes,1,opt,name=node_address,json=nodeAddress,proto3" json:"node_address,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetKindsRequest) Reset() {
	*x = GetKindsRequest{}
	mi := &file_internal_cluster_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetKindsRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetKindsRequest) ProtoMessage() {}

func (x *GetKindsRequest) ProtoReflect() protoreflect.Message {
	mi := &file_internal_cluster_proto_msgTypes[2]
	if x != nil {
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

func (x *GetKindsRequest) GetNodeAddress() string {
	if x != nil {
		return x.NodeAddress
	}
	return ""
}

type GetKindsResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the list of kinds
	Kinds         []string `protobuf:"bytes,1,rep,name=kinds,proto3" json:"kinds,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *GetKindsResponse) Reset() {
	*x = GetKindsResponse{}
	mi := &file_internal_cluster_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *GetKindsResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*GetKindsResponse) ProtoMessage() {}

func (x *GetKindsResponse) ProtoReflect() protoreflect.Message {
	mi := &file_internal_cluster_proto_msgTypes[3]
	if x != nil {
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

type Disseminate struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the message unique id
	Id string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	// Specifies the topic
	Topic string `protobuf:"bytes,2,opt,name=topic,proto3" json:"topic,omitempty"`
	// Specifies the message
	Message       *anypb.Any `protobuf:"bytes,3,opt,name=message,proto3" json:"message,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Disseminate) Reset() {
	*x = Disseminate{}
	mi := &file_internal_cluster_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Disseminate) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Disseminate) ProtoMessage() {}

func (x *Disseminate) ProtoReflect() protoreflect.Message {
	mi := &file_internal_cluster_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Disseminate.ProtoReflect.Descriptor instead.
func (*Disseminate) Descriptor() ([]byte, []int) {
	return file_internal_cluster_proto_rawDescGZIP(), []int{4}
}

func (x *Disseminate) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

func (x *Disseminate) GetTopic() string {
	if x != nil {
		return x.Topic
	}
	return ""
}

func (x *Disseminate) GetMessage() *anypb.Any {
	if x != nil {
		return x.Message
	}
	return nil
}

var File_internal_cluster_proto protoreflect.FileDescriptor

var file_internal_cluster_proto_rawDesc = string([]byte{
	0x0a, 0x16, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x63, 0x6c, 0x75, 0x73, 0x74,
	0x65, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0a, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e,
	0x61, 0x6c, 0x70, 0x62, 0x1a, 0x19, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x61, 0x6e, 0x79, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22,
	0x39, 0x0a, 0x14, 0x47, 0x65, 0x74, 0x4e, 0x6f, 0x64, 0x65, 0x4d, 0x65, 0x74, 0x72, 0x69, 0x63,
	0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x21, 0x0a, 0x0c, 0x6e, 0x6f, 0x64, 0x65, 0x5f,
	0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x0b, 0x6e,
	0x6f, 0x64, 0x65, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x22, 0x6a, 0x0a, 0x15, 0x47, 0x65,
	0x74, 0x4e, 0x6f, 0x64, 0x65, 0x4d, 0x65, 0x74, 0x72, 0x69, 0x63, 0x52, 0x65, 0x73, 0x70, 0x6f,
	0x6e, 0x73, 0x65, 0x12, 0x2e, 0x0a, 0x13, 0x6e, 0x6f, 0x64, 0x65, 0x5f, 0x72, 0x65, 0x6d, 0x6f,
	0x74, 0x65, 0x5f, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x11, 0x6e, 0x6f, 0x64, 0x65, 0x52, 0x65, 0x6d, 0x6f, 0x74, 0x65, 0x41, 0x64, 0x64, 0x72,
	0x65, 0x73, 0x73, 0x12, 0x21, 0x0a, 0x0c, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x5f, 0x63, 0x6f,
	0x75, 0x6e, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x04, 0x52, 0x0b, 0x61, 0x63, 0x74, 0x6f, 0x72,
	0x73, 0x43, 0x6f, 0x75, 0x6e, 0x74, 0x22, 0x34, 0x0a, 0x0f, 0x47, 0x65, 0x74, 0x4b, 0x69, 0x6e,
	0x64, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x21, 0x0a, 0x0c, 0x6e, 0x6f, 0x64,
	0x65, 0x5f, 0x61, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x0b, 0x6e, 0x6f, 0x64, 0x65, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x22, 0x28, 0x0a, 0x10,
	0x47, 0x65, 0x74, 0x4b, 0x69, 0x6e, 0x64, 0x73, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65,
	0x12, 0x14, 0x0a, 0x05, 0x6b, 0x69, 0x6e, 0x64, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x09, 0x52,
	0x05, 0x6b, 0x69, 0x6e, 0x64, 0x73, 0x22, 0x63, 0x0a, 0x0b, 0x44, 0x69, 0x73, 0x73, 0x65, 0x6d,
	0x69, 0x6e, 0x61, 0x74, 0x65, 0x12, 0x0e, 0x0a, 0x02, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x02, 0x69, 0x64, 0x12, 0x14, 0x0a, 0x05, 0x74, 0x6f, 0x70, 0x69, 0x63, 0x18, 0x02,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x74, 0x6f, 0x70, 0x69, 0x63, 0x12, 0x2e, 0x0a, 0x07, 0x6d,
	0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x14, 0x2e, 0x67,
	0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x41,
	0x6e, 0x79, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x32, 0xad, 0x01, 0x0a, 0x0e,
	0x43, 0x6c, 0x75, 0x73, 0x74, 0x65, 0x72, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x54,
	0x0a, 0x0d, 0x47, 0x65, 0x74, 0x4e, 0x6f, 0x64, 0x65, 0x4d, 0x65, 0x74, 0x72, 0x69, 0x63, 0x12,
	0x20, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x47, 0x65, 0x74,
	0x4e, 0x6f, 0x64, 0x65, 0x4d, 0x65, 0x74, 0x72, 0x69, 0x63, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x1a, 0x21, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x47,
	0x65, 0x74, 0x4e, 0x6f, 0x64, 0x65, 0x4d, 0x65, 0x74, 0x72, 0x69, 0x63, 0x52, 0x65, 0x73, 0x70,
	0x6f, 0x6e, 0x73, 0x65, 0x12, 0x45, 0x0a, 0x08, 0x47, 0x65, 0x74, 0x4b, 0x69, 0x6e, 0x64, 0x73,
	0x12, 0x1b, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x47, 0x65,
	0x74, 0x4b, 0x69, 0x6e, 0x64, 0x73, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x1c, 0x2e,
	0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x47, 0x65, 0x74, 0x4b, 0x69,
	0x6e, 0x64, 0x73, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x42, 0xa5, 0x01, 0x0a, 0x0e,
	0x63, 0x6f, 0x6d, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x42, 0x0c,
	0x43, 0x6c, 0x75, 0x73, 0x74, 0x65, 0x72, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x48, 0x02, 0x50, 0x01,
	0x5a, 0x3b, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x74, 0x6f, 0x63,
	0x68, 0x65, 0x6d, 0x65, 0x79, 0x2f, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2f, 0x76, 0x33, 0x2f, 0x69,
	0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c,
	0x70, 0x62, 0x3b, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xa2, 0x02, 0x03,
	0x49, 0x58, 0x58, 0xaa, 0x02, 0x0a, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62,
	0xca, 0x02, 0x0a, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xe2, 0x02, 0x16,
	0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65,
	0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x0a, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61,
	0x6c, 0x70, 0x62, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
})

var (
	file_internal_cluster_proto_rawDescOnce sync.Once
	file_internal_cluster_proto_rawDescData []byte
)

func file_internal_cluster_proto_rawDescGZIP() []byte {
	file_internal_cluster_proto_rawDescOnce.Do(func() {
		file_internal_cluster_proto_rawDescData = protoimpl.X.CompressGZIP(unsafe.Slice(unsafe.StringData(file_internal_cluster_proto_rawDesc), len(file_internal_cluster_proto_rawDesc)))
	})
	return file_internal_cluster_proto_rawDescData
}

var file_internal_cluster_proto_msgTypes = make([]protoimpl.MessageInfo, 5)
var file_internal_cluster_proto_goTypes = []any{
	(*GetNodeMetricRequest)(nil),  // 0: internalpb.GetNodeMetricRequest
	(*GetNodeMetricResponse)(nil), // 1: internalpb.GetNodeMetricResponse
	(*GetKindsRequest)(nil),       // 2: internalpb.GetKindsRequest
	(*GetKindsResponse)(nil),      // 3: internalpb.GetKindsResponse
	(*Disseminate)(nil),           // 4: internalpb.Disseminate
	(*anypb.Any)(nil),             // 5: google.protobuf.Any
}
var file_internal_cluster_proto_depIdxs = []int32{
	5, // 0: internalpb.Disseminate.message:type_name -> google.protobuf.Any
	0, // 1: internalpb.ClusterService.GetNodeMetric:input_type -> internalpb.GetNodeMetricRequest
	2, // 2: internalpb.ClusterService.GetKinds:input_type -> internalpb.GetKindsRequest
	1, // 3: internalpb.ClusterService.GetNodeMetric:output_type -> internalpb.GetNodeMetricResponse
	3, // 4: internalpb.ClusterService.GetKinds:output_type -> internalpb.GetKindsResponse
	3, // [3:5] is the sub-list for method output_type
	1, // [1:3] is the sub-list for method input_type
	1, // [1:1] is the sub-list for extension type_name
	1, // [1:1] is the sub-list for extension extendee
	0, // [0:1] is the sub-list for field type_name
}

func init() { file_internal_cluster_proto_init() }
func file_internal_cluster_proto_init() {
	if File_internal_cluster_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: unsafe.Slice(unsafe.StringData(file_internal_cluster_proto_rawDesc), len(file_internal_cluster_proto_rawDesc)),
			NumEnums:      0,
			NumMessages:   5,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_internal_cluster_proto_goTypes,
		DependencyIndexes: file_internal_cluster_proto_depIdxs,
		MessageInfos:      file_internal_cluster_proto_msgTypes,
	}.Build()
	File_internal_cluster_proto = out.File
	file_internal_cluster_proto_goTypes = nil
	file_internal_cluster_proto_depIdxs = nil
}
