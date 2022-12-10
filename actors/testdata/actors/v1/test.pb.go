// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.28.1
// 	protoc        (unknown)
// source: actors/v1/test.proto

package actorsv1

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

type TestReply struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *TestReply) Reset() {
	*x = TestReply{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_test_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TestReply) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TestReply) ProtoMessage() {}

func (x *TestReply) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_test_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TestReply.ProtoReflect.Descriptor instead.
func (*TestReply) Descriptor() ([]byte, []int) {
	return file_actors_v1_test_proto_rawDescGZIP(), []int{0}
}

type TestPanic struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *TestPanic) Reset() {
	*x = TestPanic{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_test_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TestPanic) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TestPanic) ProtoMessage() {}

func (x *TestPanic) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_test_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TestPanic.ProtoReflect.Descriptor instead.
func (*TestPanic) Descriptor() ([]byte, []int) {
	return file_actors_v1_test_proto_rawDescGZIP(), []int{1}
}

type TestTimeout struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *TestTimeout) Reset() {
	*x = TestTimeout{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_test_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TestTimeout) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TestTimeout) ProtoMessage() {}

func (x *TestTimeout) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_test_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TestTimeout.ProtoReflect.Descriptor instead.
func (*TestTimeout) Descriptor() ([]byte, []int) {
	return file_actors_v1_test_proto_rawDescGZIP(), []int{2}
}

type Reply struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Content string `protobuf:"bytes,1,opt,name=content,proto3" json:"content,omitempty"`
}

func (x *Reply) Reset() {
	*x = Reply{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_test_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Reply) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Reply) ProtoMessage() {}

func (x *Reply) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_test_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Reply.ProtoReflect.Descriptor instead.
func (*Reply) Descriptor() ([]byte, []int) {
	return file_actors_v1_test_proto_rawDescGZIP(), []int{3}
}

func (x *Reply) GetContent() string {
	if x != nil {
		return x.Content
	}
	return ""
}

type TestSend struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields
}

func (x *TestSend) Reset() {
	*x = TestSend{}
	if protoimpl.UnsafeEnabled {
		mi := &file_actors_v1_test_proto_msgTypes[4]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *TestSend) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TestSend) ProtoMessage() {}

func (x *TestSend) ProtoReflect() protoreflect.Message {
	mi := &file_actors_v1_test_proto_msgTypes[4]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TestSend.ProtoReflect.Descriptor instead.
func (*TestSend) Descriptor() ([]byte, []int) {
	return file_actors_v1_test_proto_rawDescGZIP(), []int{4}
}

var File_actors_v1_test_proto protoreflect.FileDescriptor

var file_actors_v1_test_proto_rawDesc = []byte{
	0x0a, 0x14, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x2f, 0x76, 0x31, 0x2f, 0x74, 0x65, 0x73, 0x74,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x09, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x2e, 0x76,
	0x31, 0x22, 0x0b, 0x0a, 0x09, 0x54, 0x65, 0x73, 0x74, 0x52, 0x65, 0x70, 0x6c, 0x79, 0x22, 0x0b,
	0x0a, 0x09, 0x54, 0x65, 0x73, 0x74, 0x50, 0x61, 0x6e, 0x69, 0x63, 0x22, 0x0d, 0x0a, 0x0b, 0x54,
	0x65, 0x73, 0x74, 0x54, 0x69, 0x6d, 0x65, 0x6f, 0x75, 0x74, 0x22, 0x21, 0x0a, 0x05, 0x52, 0x65,
	0x70, 0x6c, 0x79, 0x12, 0x18, 0x0a, 0x07, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x22, 0x0a, 0x0a,
	0x08, 0x54, 0x65, 0x73, 0x74, 0x53, 0x65, 0x6e, 0x64, 0x42, 0x93, 0x01, 0x0a, 0x0d, 0x63, 0x6f,
	0x6d, 0x2e, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x2e, 0x76, 0x31, 0x42, 0x09, 0x54, 0x65, 0x73,
	0x74, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x48, 0x02, 0x50, 0x01, 0x5a, 0x30, 0x67, 0x69, 0x74, 0x68,
	0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x74, 0x6f, 0x63, 0x68, 0x65, 0x6d, 0x65, 0x79, 0x2f,
	0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2f, 0x67, 0x65, 0x6e, 0x2f, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73,
	0x2f, 0x76, 0x31, 0x3b, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x76, 0x31, 0xa2, 0x02, 0x03, 0x41,
	0x58, 0x58, 0xaa, 0x02, 0x09, 0x41, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x2e, 0x56, 0x31, 0xca, 0x02,
	0x09, 0x41, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x5c, 0x56, 0x31, 0xe2, 0x02, 0x15, 0x41, 0x63, 0x74,
	0x6f, 0x72, 0x73, 0x5c, 0x56, 0x31, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61,
	0x74, 0x61, 0xea, 0x02, 0x0a, 0x41, 0x63, 0x74, 0x6f, 0x72, 0x73, 0x3a, 0x3a, 0x56, 0x31, 0x62,
	0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_actors_v1_test_proto_rawDescOnce sync.Once
	file_actors_v1_test_proto_rawDescData = file_actors_v1_test_proto_rawDesc
)

func file_actors_v1_test_proto_rawDescGZIP() []byte {
	file_actors_v1_test_proto_rawDescOnce.Do(func() {
		file_actors_v1_test_proto_rawDescData = protoimpl.X.CompressGZIP(file_actors_v1_test_proto_rawDescData)
	})
	return file_actors_v1_test_proto_rawDescData
}

var file_actors_v1_test_proto_msgTypes = make([]protoimpl.MessageInfo, 5)
var file_actors_v1_test_proto_goTypes = []interface{}{
	(*TestReply)(nil),   // 0: actors.v1.TestReply
	(*TestPanic)(nil),   // 1: actors.v1.TestPanic
	(*TestTimeout)(nil), // 2: actors.v1.TestTimeout
	(*Reply)(nil),       // 3: actors.v1.Reply
	(*TestSend)(nil),    // 4: actors.v1.TestSend
}
var file_actors_v1_test_proto_depIdxs = []int32{
	0, // [0:0] is the sub-list for method output_type
	0, // [0:0] is the sub-list for method input_type
	0, // [0:0] is the sub-list for extension type_name
	0, // [0:0] is the sub-list for extension extendee
	0, // [0:0] is the sub-list for field type_name
}

func init() { file_actors_v1_test_proto_init() }
func file_actors_v1_test_proto_init() {
	if File_actors_v1_test_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_actors_v1_test_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*TestReply); i {
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
		file_actors_v1_test_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*TestPanic); i {
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
		file_actors_v1_test_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*TestTimeout); i {
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
		file_actors_v1_test_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Reply); i {
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
		file_actors_v1_test_proto_msgTypes[4].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*TestSend); i {
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
			RawDescriptor: file_actors_v1_test_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   5,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_actors_v1_test_proto_goTypes,
		DependencyIndexes: file_actors_v1_test_proto_depIdxs,
		MessageInfos:      file_actors_v1_test_proto_msgTypes,
	}.Build()
	File_actors_v1_test_proto = out.File
	file_actors_v1_test_proto_rawDesc = nil
	file_actors_v1_test_proto_goTypes = nil
	file_actors_v1_test_proto_depIdxs = nil
}
