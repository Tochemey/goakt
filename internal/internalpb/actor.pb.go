// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.6
// 	protoc        (unknown)
// source: internal/actor.proto

package internalpb

import (
	goaktpb "github.com/tochemey/goakt/v3/goaktpb"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	_ "google.golang.org/protobuf/types/known/anypb"
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

// ActorRef represents the actor information on the wire.
type ActorRef struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the actor address
	ActorAddress *goaktpb.Address `protobuf:"bytes,1,opt,name=actor_address,json=actorAddress,proto3" json:"actor_address,omitempty"`
	// Specifies the actor type
	ActorType string `protobuf:"bytes,2,opt,name=actor_type,json=actorType,proto3" json:"actor_type,omitempty"`
	// Specifies if the actor is a singleton
	IsSingleton bool `protobuf:"varint,3,opt,name=is_singleton,json=isSingleton,proto3" json:"is_singleton,omitempty"`
	// Specifies if the actor is disabled for relocation
	Relocatable bool `protobuf:"varint,4,opt,name=relocatable,proto3" json:"relocatable,omitempty"`
	// Specifies whether actor is an entity
	IsEntity bool `protobuf:"varint,5,opt,name=is_entity,json=isEntity,proto3" json:"is_entity,omitempty"`
	// Specifies the initial actor state if it is an entity
	InitialStateType *string `protobuf:"bytes,6,opt,name=initial_state_type,json=initialStateType,proto3,oneof" json:"initial_state_type,omitempty"`
	unknownFields    protoimpl.UnknownFields
	sizeCache        protoimpl.SizeCache
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

func (x *ActorRef) GetIsSingleton() bool {
	if x != nil {
		return x.IsSingleton
	}
	return false
}

func (x *ActorRef) GetRelocatable() bool {
	if x != nil {
		return x.Relocatable
	}
	return false
}

func (x *ActorRef) GetIsEntity() bool {
	if x != nil {
		return x.IsEntity
	}
	return false
}

func (x *ActorRef) GetInitialStateType() string {
	if x != nil && x.InitialStateType != nil {
		return *x.InitialStateType
	}
	return ""
}

// ActorProps defines the properties of an actor
// that can be used to spawn an actor remotely.
type ActorProps struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the actor name.
	ActorName string `protobuf:"bytes,1,opt,name=actor_name,json=actorName,proto3" json:"actor_name,omitempty"`
	// Specifies the actor type
	ActorType string `protobuf:"bytes,2,opt,name=actor_type,json=actorType,proto3" json:"actor_type,omitempty"`
	// Specifies if the actor is a singleton
	IsSingleton bool `protobuf:"varint,3,opt,name=is_singleton,json=isSingleton,proto3" json:"is_singleton,omitempty"`
	// Specifies if the actor is disabled for relocation
	Relocatable bool `protobuf:"varint,4,opt,name=relocatable,proto3" json:"relocatable,omitempty"`
	// Specifies whether actor is an entity
	IsEntity bool `protobuf:"varint,5,opt,name=is_entity,json=isEntity,proto3" json:"is_entity,omitempty"`
	// Specifies the initial actor state if it is an entity
	InitialStateType *string `protobuf:"bytes,6,opt,name=initial_state_type,json=initialStateType,proto3,oneof" json:"initial_state_type,omitempty"`
	unknownFields    protoimpl.UnknownFields
	sizeCache        protoimpl.SizeCache
}

func (x *ActorProps) Reset() {
	*x = ActorProps{}
	mi := &file_internal_actor_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ActorProps) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ActorProps) ProtoMessage() {}

func (x *ActorProps) ProtoReflect() protoreflect.Message {
	mi := &file_internal_actor_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ActorProps.ProtoReflect.Descriptor instead.
func (*ActorProps) Descriptor() ([]byte, []int) {
	return file_internal_actor_proto_rawDescGZIP(), []int{1}
}

func (x *ActorProps) GetActorName() string {
	if x != nil {
		return x.ActorName
	}
	return ""
}

func (x *ActorProps) GetActorType() string {
	if x != nil {
		return x.ActorType
	}
	return ""
}

func (x *ActorProps) GetIsSingleton() bool {
	if x != nil {
		return x.IsSingleton
	}
	return false
}

func (x *ActorProps) GetRelocatable() bool {
	if x != nil {
		return x.Relocatable
	}
	return false
}

func (x *ActorProps) GetIsEntity() bool {
	if x != nil {
		return x.IsEntity
	}
	return false
}

func (x *ActorProps) GetInitialStateType() string {
	if x != nil && x.InitialStateType != nil {
		return *x.InitialStateType
	}
	return ""
}

var File_internal_actor_proto protoreflect.FileDescriptor

const file_internal_actor_proto_rawDesc = "" +
	"\n" +
	"\x14internal/actor.proto\x12\n" +
	"internalpb\x1a\x11goakt/goakt.proto\x1a\x19google/protobuf/any.proto\"\x8c\x02\n" +
	"\bActorRef\x125\n" +
	"\ractor_address\x18\x01 \x01(\v2\x10.goaktpb.AddressR\factorAddress\x12\x1d\n" +
	"\n" +
	"actor_type\x18\x02 \x01(\tR\tactorType\x12!\n" +
	"\fis_singleton\x18\x03 \x01(\bR\visSingleton\x12 \n" +
	"\vrelocatable\x18\x04 \x01(\bR\vrelocatable\x12\x1b\n" +
	"\tis_entity\x18\x05 \x01(\bR\bisEntity\x121\n" +
	"\x12initial_state_type\x18\x06 \x01(\tH\x00R\x10initialStateType\x88\x01\x01B\x15\n" +
	"\x13_initial_state_type\"\xf6\x01\n" +
	"\n" +
	"ActorProps\x12\x1d\n" +
	"\n" +
	"actor_name\x18\x01 \x01(\tR\tactorName\x12\x1d\n" +
	"\n" +
	"actor_type\x18\x02 \x01(\tR\tactorType\x12!\n" +
	"\fis_singleton\x18\x03 \x01(\bR\visSingleton\x12 \n" +
	"\vrelocatable\x18\x04 \x01(\bR\vrelocatable\x12\x1b\n" +
	"\tis_entity\x18\x05 \x01(\bR\bisEntity\x121\n" +
	"\x12initial_state_type\x18\x06 \x01(\tH\x00R\x10initialStateType\x88\x01\x01B\x15\n" +
	"\x13_initial_state_typeB\xa3\x01\n" +
	"\x0ecom.internalpbB\n" +
	"ActorProtoH\x02P\x01Z;github.com/tochemey/goakt/v3/internal/internalpb;internalpb\xa2\x02\x03IXX\xaa\x02\n" +
	"Internalpb\xca\x02\n" +
	"Internalpb\xe2\x02\x16Internalpb\\GPBMetadata\xea\x02\n" +
	"Internalpbb\x06proto3"

var (
	file_internal_actor_proto_rawDescOnce sync.Once
	file_internal_actor_proto_rawDescData []byte
)

func file_internal_actor_proto_rawDescGZIP() []byte {
	file_internal_actor_proto_rawDescOnce.Do(func() {
		file_internal_actor_proto_rawDescData = protoimpl.X.CompressGZIP(unsafe.Slice(unsafe.StringData(file_internal_actor_proto_rawDesc), len(file_internal_actor_proto_rawDesc)))
	})
	return file_internal_actor_proto_rawDescData
}

var file_internal_actor_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_internal_actor_proto_goTypes = []any{
	(*ActorRef)(nil),        // 0: internalpb.ActorRef
	(*ActorProps)(nil),      // 1: internalpb.ActorProps
	(*goaktpb.Address)(nil), // 2: goaktpb.Address
}
var file_internal_actor_proto_depIdxs = []int32{
	2, // 0: internalpb.ActorRef.actor_address:type_name -> goaktpb.Address
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
	file_internal_actor_proto_msgTypes[0].OneofWrappers = []any{}
	file_internal_actor_proto_msgTypes[1].OneofWrappers = []any{}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: unsafe.Slice(unsafe.StringData(file_internal_actor_proto_rawDesc), len(file_internal_actor_proto_rawDesc)),
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_internal_actor_proto_goTypes,
		DependencyIndexes: file_internal_actor_proto_depIdxs,
		MessageInfos:      file_internal_actor_proto_msgTypes,
	}.Build()
	File_internal_actor_proto = out.File
	file_internal_actor_proto_goTypes = nil
	file_internal_actor_proto_depIdxs = nil
}
