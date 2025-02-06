// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.5
// 	protoc        (unknown)
// source: benchmark/benchmark.proto

package benchpb

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

type BenchTell struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *BenchTell) Reset() {
	*x = BenchTell{}
	mi := &file_benchmark_benchmark_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *BenchTell) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*BenchTell) ProtoMessage() {}

func (x *BenchTell) ProtoReflect() protoreflect.Message {
	mi := &file_benchmark_benchmark_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use BenchTell.ProtoReflect.Descriptor instead.
func (*BenchTell) Descriptor() ([]byte, []int) {
	return file_benchmark_benchmark_proto_rawDescGZIP(), []int{0}
}

type BenchRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *BenchRequest) Reset() {
	*x = BenchRequest{}
	mi := &file_benchmark_benchmark_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *BenchRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*BenchRequest) ProtoMessage() {}

func (x *BenchRequest) ProtoReflect() protoreflect.Message {
	mi := &file_benchmark_benchmark_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use BenchRequest.ProtoReflect.Descriptor instead.
func (*BenchRequest) Descriptor() ([]byte, []int) {
	return file_benchmark_benchmark_proto_rawDescGZIP(), []int{1}
}

type BenchResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *BenchResponse) Reset() {
	*x = BenchResponse{}
	mi := &file_benchmark_benchmark_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *BenchResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*BenchResponse) ProtoMessage() {}

func (x *BenchResponse) ProtoReflect() protoreflect.Message {
	mi := &file_benchmark_benchmark_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use BenchResponse.ProtoReflect.Descriptor instead.
func (*BenchResponse) Descriptor() ([]byte, []int) {
	return file_benchmark_benchmark_proto_rawDescGZIP(), []int{2}
}

type Ping struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Ping) Reset() {
	*x = Ping{}
	mi := &file_benchmark_benchmark_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Ping) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Ping) ProtoMessage() {}

func (x *Ping) ProtoReflect() protoreflect.Message {
	mi := &file_benchmark_benchmark_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Ping.ProtoReflect.Descriptor instead.
func (*Ping) Descriptor() ([]byte, []int) {
	return file_benchmark_benchmark_proto_rawDescGZIP(), []int{3}
}

type Pong struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Pong) Reset() {
	*x = Pong{}
	mi := &file_benchmark_benchmark_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Pong) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Pong) ProtoMessage() {}

func (x *Pong) ProtoReflect() protoreflect.Message {
	mi := &file_benchmark_benchmark_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Pong.ProtoReflect.Descriptor instead.
func (*Pong) Descriptor() ([]byte, []int) {
	return file_benchmark_benchmark_proto_rawDescGZIP(), []int{4}
}

type BenchPriorityMailbox struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the message priority
	Priority      int64 `protobuf:"varint,1,opt,name=priority,proto3" json:"priority,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *BenchPriorityMailbox) Reset() {
	*x = BenchPriorityMailbox{}
	mi := &file_benchmark_benchmark_proto_msgTypes[5]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *BenchPriorityMailbox) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*BenchPriorityMailbox) ProtoMessage() {}

func (x *BenchPriorityMailbox) ProtoReflect() protoreflect.Message {
	mi := &file_benchmark_benchmark_proto_msgTypes[5]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use BenchPriorityMailbox.ProtoReflect.Descriptor instead.
func (*BenchPriorityMailbox) Descriptor() ([]byte, []int) {
	return file_benchmark_benchmark_proto_rawDescGZIP(), []int{5}
}

func (x *BenchPriorityMailbox) GetPriority() int64 {
	if x != nil {
		return x.Priority
	}
	return 0
}

var File_benchmark_benchmark_proto protoreflect.FileDescriptor

var file_benchmark_benchmark_proto_rawDesc = string([]byte{
	0x0a, 0x19, 0x62, 0x65, 0x6e, 0x63, 0x68, 0x6d, 0x61, 0x72, 0x6b, 0x2f, 0x62, 0x65, 0x6e, 0x63,
	0x68, 0x6d, 0x61, 0x72, 0x6b, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x07, 0x62, 0x65, 0x6e,
	0x63, 0x68, 0x70, 0x62, 0x22, 0x0b, 0x0a, 0x09, 0x42, 0x65, 0x6e, 0x63, 0x68, 0x54, 0x65, 0x6c,
	0x6c, 0x22, 0x0e, 0x0a, 0x0c, 0x42, 0x65, 0x6e, 0x63, 0x68, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x22, 0x0f, 0x0a, 0x0d, 0x42, 0x65, 0x6e, 0x63, 0x68, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e,
	0x73, 0x65, 0x22, 0x06, 0x0a, 0x04, 0x50, 0x69, 0x6e, 0x67, 0x22, 0x06, 0x0a, 0x04, 0x50, 0x6f,
	0x6e, 0x67, 0x22, 0x32, 0x0a, 0x14, 0x42, 0x65, 0x6e, 0x63, 0x68, 0x50, 0x72, 0x69, 0x6f, 0x72,
	0x69, 0x74, 0x79, 0x4d, 0x61, 0x69, 0x6c, 0x62, 0x6f, 0x78, 0x12, 0x1a, 0x0a, 0x08, 0x70, 0x72,
	0x69, 0x6f, 0x72, 0x69, 0x74, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x03, 0x52, 0x08, 0x70, 0x72,
	0x69, 0x6f, 0x72, 0x69, 0x74, 0x79, 0x42, 0x8f, 0x01, 0x0a, 0x0b, 0x63, 0x6f, 0x6d, 0x2e, 0x62,
	0x65, 0x6e, 0x63, 0x68, 0x70, 0x62, 0x42, 0x0e, 0x42, 0x65, 0x6e, 0x63, 0x68, 0x6d, 0x61, 0x72,
	0x6b, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x48, 0x02, 0x50, 0x01, 0x5a, 0x32, 0x67, 0x69, 0x74, 0x68,
	0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x74, 0x6f, 0x63, 0x68, 0x65, 0x6d, 0x65, 0x79, 0x2f,
	0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2f, 0x76, 0x32, 0x2f, 0x62, 0x65, 0x6e, 0x63, 0x68, 0x2f, 0x62,
	0x65, 0x6e, 0x63, 0x68, 0x70, 0x62, 0x3b, 0x62, 0x65, 0x6e, 0x63, 0x68, 0x70, 0x62, 0xa2, 0x02,
	0x03, 0x42, 0x58, 0x58, 0xaa, 0x02, 0x07, 0x42, 0x65, 0x6e, 0x63, 0x68, 0x70, 0x62, 0xca, 0x02,
	0x07, 0x42, 0x65, 0x6e, 0x63, 0x68, 0x70, 0x62, 0xe2, 0x02, 0x13, 0x42, 0x65, 0x6e, 0x63, 0x68,
	0x70, 0x62, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02,
	0x07, 0x42, 0x65, 0x6e, 0x63, 0x68, 0x70, 0x62, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
})

var (
	file_benchmark_benchmark_proto_rawDescOnce sync.Once
	file_benchmark_benchmark_proto_rawDescData []byte
)

func file_benchmark_benchmark_proto_rawDescGZIP() []byte {
	file_benchmark_benchmark_proto_rawDescOnce.Do(func() {
		file_benchmark_benchmark_proto_rawDescData = protoimpl.X.CompressGZIP(unsafe.Slice(unsafe.StringData(file_benchmark_benchmark_proto_rawDesc), len(file_benchmark_benchmark_proto_rawDesc)))
	})
	return file_benchmark_benchmark_proto_rawDescData
}

var file_benchmark_benchmark_proto_msgTypes = make([]protoimpl.MessageInfo, 6)
var file_benchmark_benchmark_proto_goTypes = []any{
	(*BenchTell)(nil),            // 0: benchpb.BenchTell
	(*BenchRequest)(nil),         // 1: benchpb.BenchRequest
	(*BenchResponse)(nil),        // 2: benchpb.BenchResponse
	(*Ping)(nil),                 // 3: benchpb.Ping
	(*Pong)(nil),                 // 4: benchpb.Pong
	(*BenchPriorityMailbox)(nil), // 5: benchpb.BenchPriorityMailbox
}
var file_benchmark_benchmark_proto_depIdxs = []int32{
	0, // [0:0] is the sub-list for method output_type
	0, // [0:0] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_benchmark_benchmark_proto_init() }
func file_benchmark_benchmark_proto_init() {
	if File_benchmark_benchmark_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: unsafe.Slice(unsafe.StringData(file_benchmark_benchmark_proto_rawDesc), len(file_benchmark_benchmark_proto_rawDesc)),
			NumEnums:      0,
			NumMessages:   6,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_benchmark_benchmark_proto_goTypes,
		DependencyIndexes: file_benchmark_benchmark_proto_depIdxs,
		MessageInfos:      file_benchmark_benchmark_proto_msgTypes,
	}.Build()
	File_benchmark_benchmark_proto = out.File
	file_benchmark_benchmark_proto_goTypes = nil
	file_benchmark_benchmark_proto_depIdxs = nil
}
