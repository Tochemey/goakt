// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.1
// 	protoc        (unknown)
// source: internal/actor.proto

package internalpb

import (
	goaktpb "github.com/tochemey/goakt/v2/goaktpb"
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

// ActorRef represents the actor information on the wire.
type ActorRef struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the actor address
	ActorAddress *goaktpb.Address `protobuf:"bytes,1,opt,name=actor_address,json=actorAddress,proto3" json:"actor_address,omitempty"`
	// Specifies the actor type
	ActorType     string `protobuf:"bytes,2,opt,name=actor_type,json=actorType,proto3" json:"actor_type,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ActorRef) Reset() {
	*x = ActorRef{}
	mi := &file_internal_actor_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ActorRef) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ActorRef) ProtoMessage() {}

func (x *ActorRef) ProtoReflect() protoreflect.Message {
	mi := &file_internal_actor_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ActorRef.ProtoReflect.Descriptor instead.
func (*ActorRef) Descriptor() ([]byte, []int) {
	return file_internal_actor_proto_rawDescGZIP(), []int{0}
}

func (x *ActorRef) GetActorAddress() *goaktpb.Address {
	if x != nil {
		return x.ActorAddress
	}
	return nil
}

func (x *ActorRef) GetActorType() string {
	if x != nil {
		return x.ActorType
	}
	return ""
}

var File_internal_actor_proto protoreflect.FileDescriptor

var file_internal_actor_proto_rawDesc = []byte{
	0x0a, 0x14, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x61, 0x63, 0x74, 0x6f, 0x72,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0a, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c,
	0x70, 0x62, 0x1a, 0x11, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2f, 0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x60, 0x0a, 0x08, 0x41, 0x63, 0x74, 0x6f, 0x72, 0x52, 0x65,
	0x66, 0x12, 0x35, 0x0a, 0x0d, 0x61, 0x63, 0x74, 0x6f, 0x72, 0x5f, 0x61, 0x64, 0x64, 0x72, 0x65,
	0x73, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x10, 0x2e, 0x67, 0x6f, 0x61, 0x6b, 0x74,
	0x70, 0x62, 0x2e, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x52, 0x0c, 0x61, 0x63, 0x74, 0x6f,
	0x72, 0x41, 0x64, 0x64, 0x72, 0x65, 0x73, 0x73, 0x12, 0x1d, 0x0a, 0x0a, 0x61, 0x63, 0x74, 0x6f,
	0x72, 0x5f, 0x74, 0x79, 0x70, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x09, 0x61, 0x63,
	0x74, 0x6f, 0x72, 0x54, 0x79, 0x70, 0x65, 0x42, 0xa3, 0x01, 0x0a, 0x0e, 0x63, 0x6f, 0x6d, 0x2e,
	0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x42, 0x0a, 0x41, 0x63, 0x74, 0x6f,
	0x72, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x48, 0x02, 0x50, 0x01, 0x5a, 0x3b, 0x67, 0x69, 0x74, 0x68,
	0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x74, 0x6f, 0x63, 0x68, 0x65, 0x6d, 0x65, 0x79, 0x2f,
	0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2f, 0x76, 0x32, 0x2f, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61,
	0x6c, 0x2f, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x3b, 0x69, 0x6e, 0x74,
	0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xa2, 0x02, 0x03, 0x49, 0x58, 0x58, 0xaa, 0x02, 0x0a,
	0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xca, 0x02, 0x0a, 0x49, 0x6e, 0x74,
	0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xe2, 0x02, 0x16, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e,
	0x61, 0x6c, 0x70, 0x62, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61,
	0xea, 0x02, 0x0a, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x62, 0x06, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_internal_actor_proto_rawDescOnce sync.Once
	file_internal_actor_proto_rawDescData = file_internal_actor_proto_rawDesc
)

func file_internal_actor_proto_rawDescGZIP() []byte {
	file_internal_actor_proto_rawDescOnce.Do(func() {
		file_internal_actor_proto_rawDescData = protoimpl.X.CompressGZIP(file_internal_actor_proto_rawDescData)
	})
	return file_internal_actor_proto_rawDescData
}

var file_internal_actor_proto_msgTypes = make([]protoimpl.MessageInfo, 1)
var file_internal_actor_proto_goTypes = []any{
	(*ActorRef)(nil),        // 0: internalpb.ActorRef
	(*goaktpb.Address)(nil), // 1: goaktpb.Address
}
var file_internal_actor_proto_depIdxs = []int32{
	1, // 0: internalpb.ActorRef.actor_address:type_name -> goaktpb.Address
	1, // [1:1] is the sub-list for method output_type
	1, // [1:1] is the sub-list for method input_type
	1, // [1:1] is the sub-list for extension type_name
	1, // [1:1] is the sub-list for extension extendee
	0, // [0:1] is the sub-list for field type_name
}

func init() { file_internal_actor_proto_init() }
func file_internal_actor_proto_init() {
	if File_internal_actor_proto != nil {
		return
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_internal_actor_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   1,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_internal_actor_proto_goTypes,
		DependencyIndexes: file_internal_actor_proto_depIdxs,
		MessageInfos:      file_internal_actor_proto_msgTypes,
	}.Build()
	File_internal_actor_proto = out.File
	file_internal_actor_proto_rawDesc = nil
	file_internal_actor_proto_goTypes = nil
	file_internal_actor_proto_depIdxs = nil
}
