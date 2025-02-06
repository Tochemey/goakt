// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.5
// 	protoc        (unknown)
// source: internal/supervision.proto

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

// HandleFault message is sent by a child
// actor to its parent when it is panicking or returning an error
// while processing message
type HandleFault struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the actor id
	ActorId string `protobuf:"bytes,1,opt,name=actor_id,json=actorId,proto3" json:"actor_id,omitempty"`
	// Specifies the message
	Message string `protobuf:"bytes,2,opt,name=message,proto3" json:"message,omitempty"`
	// Specifies the directive
	//
	// Types that are valid to be assigned to Directive:
	//
	//	*HandleFault_Stop
	//	*HandleFault_Resume
	//	*HandleFault_Restart
	//	*HandleFault_Escalate
	Directive     isHandleFault_Directive `protobuf_oneof:"directive"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *HandleFault) Reset() {
	*x = HandleFault{}
	mi := &file_internal_supervision_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *HandleFault) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*HandleFault) ProtoMessage() {}

func (x *HandleFault) ProtoReflect() protoreflect.Message {
	mi := &file_internal_supervision_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use HandleFault.ProtoReflect.Descriptor instead.
func (*HandleFault) Descriptor() ([]byte, []int) {
	return file_internal_supervision_proto_rawDescGZIP(), []int{0}
}

func (x *HandleFault) GetActorId() string {
	if x != nil {
		return x.ActorId
	}
	return ""
}

func (x *HandleFault) GetMessage() string {
	if x != nil {
		return x.Message
	}
	return ""
}

func (x *HandleFault) GetDirective() isHandleFault_Directive {
	if x != nil {
		return x.Directive
	}
	return nil
}

func (x *HandleFault) GetStop() *StopDirective {
	if x != nil {
		if x, ok := x.Directive.(*HandleFault_Stop); ok {
			return x.Stop
		}
	}
	return nil
}

func (x *HandleFault) GetResume() *ResumeDirective {
	if x != nil {
		if x, ok := x.Directive.(*HandleFault_Resume); ok {
			return x.Resume
		}
	}
	return nil
}

func (x *HandleFault) GetRestart() *RestartDirective {
	if x != nil {
		if x, ok := x.Directive.(*HandleFault_Restart); ok {
			return x.Restart
		}
	}
	return nil
}

func (x *HandleFault) GetEscalate() *EscalateDirective {
	if x != nil {
		if x, ok := x.Directive.(*HandleFault_Escalate); ok {
			return x.Escalate
		}
	}
	return nil
}

type isHandleFault_Directive interface {
	isHandleFault_Directive()
}

type HandleFault_Stop struct {
	Stop *StopDirective `protobuf:"bytes,3,opt,name=stop,proto3,oneof"`
}

type HandleFault_Resume struct {
	Resume *ResumeDirective `protobuf:"bytes,4,opt,name=resume,proto3,oneof"`
}

type HandleFault_Restart struct {
	Restart *RestartDirective `protobuf:"bytes,5,opt,name=restart,proto3,oneof"`
}

type HandleFault_Escalate struct {
	Escalate *EscalateDirective `protobuf:"bytes,6,opt,name=escalate,proto3,oneof"`
}

func (*HandleFault_Stop) isHandleFault_Directive() {}

func (*HandleFault_Resume) isHandleFault_Directive() {}

func (*HandleFault_Restart) isHandleFault_Directive() {}

func (*HandleFault_Escalate) isHandleFault_Directive() {}

// StopDirective defines the supervisor stop directive
type StopDirective struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *StopDirective) Reset() {
	*x = StopDirective{}
	mi := &file_internal_supervision_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *StopDirective) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*StopDirective) ProtoMessage() {}

func (x *StopDirective) ProtoReflect() protoreflect.Message {
	mi := &file_internal_supervision_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use StopDirective.ProtoReflect.Descriptor instead.
func (*StopDirective) Descriptor() ([]byte, []int) {
	return file_internal_supervision_proto_rawDescGZIP(), []int{1}
}

// ResumeDirective defines the supervisor resume directive
// This ignores the failure and process the next message, instead
type ResumeDirective struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ResumeDirective) Reset() {
	*x = ResumeDirective{}
	mi := &file_internal_supervision_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *ResumeDirective) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*ResumeDirective) ProtoMessage() {}

func (x *ResumeDirective) ProtoReflect() protoreflect.Message {
	mi := &file_internal_supervision_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use ResumeDirective.ProtoReflect.Descriptor instead.
func (*ResumeDirective) Descriptor() ([]byte, []int) {
	return file_internal_supervision_proto_rawDescGZIP(), []int{2}
}

// EscalateDirective defines the supervisor escalation directive
// It escalates the failure to the next parent in the hierarchy, thereby failing itself
type EscalateDirective struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *EscalateDirective) Reset() {
	*x = EscalateDirective{}
	mi := &file_internal_supervision_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *EscalateDirective) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*EscalateDirective) ProtoMessage() {}

func (x *EscalateDirective) ProtoReflect() protoreflect.Message {
	mi := &file_internal_supervision_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use EscalateDirective.ProtoReflect.Descriptor instead.
func (*EscalateDirective) Descriptor() ([]byte, []int) {
	return file_internal_supervision_proto_rawDescGZIP(), []int{3}
}

// RestartDirective defines supervisor restart directive
type RestartDirective struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Specifies the maximum number of retries
	// When reaching this number the faulty actor is stopped
	MaxRetries uint32 `protobuf:"varint,1,opt,name=max_retries,json=maxRetries,proto3" json:"max_retries,omitempty"`
	// Specifies the time range to restart the faulty actor
	Timeout       int64 `protobuf:"varint,2,opt,name=timeout,proto3" json:"timeout,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *RestartDirective) Reset() {
	*x = RestartDirective{}
	mi := &file_internal_supervision_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *RestartDirective) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*RestartDirective) ProtoMessage() {}

func (x *RestartDirective) ProtoReflect() protoreflect.Message {
	mi := &file_internal_supervision_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use RestartDirective.ProtoReflect.Descriptor instead.
func (*RestartDirective) Descriptor() ([]byte, []int) {
	return file_internal_supervision_proto_rawDescGZIP(), []int{4}
}

func (x *RestartDirective) GetMaxRetries() uint32 {
	if x != nil {
		return x.MaxRetries
	}
	return 0
}

func (x *RestartDirective) GetTimeout() int64 {
	if x != nil {
		return x.Timeout
	}
	return 0
}

var File_internal_supervision_proto protoreflect.FileDescriptor

var file_internal_supervision_proto_rawDesc = string([]byte{
	0x0a, 0x1a, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x2f, 0x73, 0x75, 0x70, 0x65, 0x72,
	0x76, 0x69, 0x73, 0x69, 0x6f, 0x6e, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0a, 0x69, 0x6e,
	0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x22, 0xae, 0x02, 0x0a, 0x0b, 0x48, 0x61, 0x6e,
	0x64, 0x6c, 0x65, 0x46, 0x61, 0x75, 0x6c, 0x74, 0x12, 0x19, 0x0a, 0x08, 0x61, 0x63, 0x74, 0x6f,
	0x72, 0x5f, 0x69, 0x64, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x61, 0x63, 0x74, 0x6f,
	0x72, 0x49, 0x64, 0x12, 0x18, 0x0a, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x18, 0x02,
	0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x6d, 0x65, 0x73, 0x73, 0x61, 0x67, 0x65, 0x12, 0x2f, 0x0a,
	0x04, 0x73, 0x74, 0x6f, 0x70, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x19, 0x2e, 0x69, 0x6e,
	0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x53, 0x74, 0x6f, 0x70, 0x44, 0x69, 0x72,
	0x65, 0x63, 0x74, 0x69, 0x76, 0x65, 0x48, 0x00, 0x52, 0x04, 0x73, 0x74, 0x6f, 0x70, 0x12, 0x35,
	0x0a, 0x06, 0x72, 0x65, 0x73, 0x75, 0x6d, 0x65, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1b,
	0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x52, 0x65, 0x73, 0x75,
	0x6d, 0x65, 0x44, 0x69, 0x72, 0x65, 0x63, 0x74, 0x69, 0x76, 0x65, 0x48, 0x00, 0x52, 0x06, 0x72,
	0x65, 0x73, 0x75, 0x6d, 0x65, 0x12, 0x38, 0x0a, 0x07, 0x72, 0x65, 0x73, 0x74, 0x61, 0x72, 0x74,
	0x18, 0x05, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x1c, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61,
	0x6c, 0x70, 0x62, 0x2e, 0x52, 0x65, 0x73, 0x74, 0x61, 0x72, 0x74, 0x44, 0x69, 0x72, 0x65, 0x63,
	0x74, 0x69, 0x76, 0x65, 0x48, 0x00, 0x52, 0x07, 0x72, 0x65, 0x73, 0x74, 0x61, 0x72, 0x74, 0x12,
	0x3b, 0x0a, 0x08, 0x65, 0x73, 0x63, 0x61, 0x6c, 0x61, 0x74, 0x65, 0x18, 0x06, 0x20, 0x01, 0x28,
	0x0b, 0x32, 0x1d, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x2e, 0x45,
	0x73, 0x63, 0x61, 0x6c, 0x61, 0x74, 0x65, 0x44, 0x69, 0x72, 0x65, 0x63, 0x74, 0x69, 0x76, 0x65,
	0x48, 0x00, 0x52, 0x08, 0x65, 0x73, 0x63, 0x61, 0x6c, 0x61, 0x74, 0x65, 0x42, 0x0b, 0x0a, 0x09,
	0x64, 0x69, 0x72, 0x65, 0x63, 0x74, 0x69, 0x76, 0x65, 0x22, 0x0f, 0x0a, 0x0d, 0x53, 0x74, 0x6f,
	0x70, 0x44, 0x69, 0x72, 0x65, 0x63, 0x74, 0x69, 0x76, 0x65, 0x22, 0x11, 0x0a, 0x0f, 0x52, 0x65,
	0x73, 0x75, 0x6d, 0x65, 0x44, 0x69, 0x72, 0x65, 0x63, 0x74, 0x69, 0x76, 0x65, 0x22, 0x13, 0x0a,
	0x11, 0x45, 0x73, 0x63, 0x61, 0x6c, 0x61, 0x74, 0x65, 0x44, 0x69, 0x72, 0x65, 0x63, 0x74, 0x69,
	0x76, 0x65, 0x22, 0x4d, 0x0a, 0x10, 0x52, 0x65, 0x73, 0x74, 0x61, 0x72, 0x74, 0x44, 0x69, 0x72,
	0x65, 0x63, 0x74, 0x69, 0x76, 0x65, 0x12, 0x1f, 0x0a, 0x0b, 0x6d, 0x61, 0x78, 0x5f, 0x72, 0x65,
	0x74, 0x72, 0x69, 0x65, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0d, 0x52, 0x0a, 0x6d, 0x61, 0x78,
	0x52, 0x65, 0x74, 0x72, 0x69, 0x65, 0x73, 0x12, 0x18, 0x0a, 0x07, 0x74, 0x69, 0x6d, 0x65, 0x6f,
	0x75, 0x74, 0x18, 0x02, 0x20, 0x01, 0x28, 0x03, 0x52, 0x07, 0x74, 0x69, 0x6d, 0x65, 0x6f, 0x75,
	0x74, 0x42, 0xa9, 0x01, 0x0a, 0x0e, 0x63, 0x6f, 0x6d, 0x2e, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e,
	0x61, 0x6c, 0x70, 0x62, 0x42, 0x10, 0x53, 0x75, 0x70, 0x65, 0x72, 0x76, 0x69, 0x73, 0x69, 0x6f,
	0x6e, 0x50, 0x72, 0x6f, 0x74, 0x6f, 0x48, 0x02, 0x50, 0x01, 0x5a, 0x3b, 0x67, 0x69, 0x74, 0x68,
	0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x74, 0x6f, 0x63, 0x68, 0x65, 0x6d, 0x65, 0x79, 0x2f,
	0x67, 0x6f, 0x61, 0x6b, 0x74, 0x2f, 0x76, 0x32, 0x2f, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61,
	0x6c, 0x2f, 0x69, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x3b, 0x69, 0x6e, 0x74,
	0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xa2, 0x02, 0x03, 0x49, 0x58, 0x58, 0xaa, 0x02, 0x0a,
	0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xca, 0x02, 0x0a, 0x49, 0x6e, 0x74,
	0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0xe2, 0x02, 0x16, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e,
	0x61, 0x6c, 0x70, 0x62, 0x5c, 0x47, 0x50, 0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61,
	0xea, 0x02, 0x0a, 0x49, 0x6e, 0x74, 0x65, 0x72, 0x6e, 0x61, 0x6c, 0x70, 0x62, 0x62, 0x06, 0x70,
	0x72, 0x6f, 0x74, 0x6f, 0x33,
})

var (
	file_internal_supervision_proto_rawDescOnce sync.Once
	file_internal_supervision_proto_rawDescData []byte
)

func file_internal_supervision_proto_rawDescGZIP() []byte {
	file_internal_supervision_proto_rawDescOnce.Do(func() {
		file_internal_supervision_proto_rawDescData = protoimpl.X.CompressGZIP(unsafe.Slice(unsafe.StringData(file_internal_supervision_proto_rawDesc), len(file_internal_supervision_proto_rawDesc)))
	})
	return file_internal_supervision_proto_rawDescData
}

var file_internal_supervision_proto_msgTypes = make([]protoimpl.MessageInfo, 5)
var file_internal_supervision_proto_goTypes = []any{
	(*HandleFault)(nil),       // 0: internalpb.HandleFault
	(*StopDirective)(nil),     // 1: internalpb.StopDirective
	(*ResumeDirective)(nil),   // 2: internalpb.ResumeDirective
	(*EscalateDirective)(nil), // 3: internalpb.EscalateDirective
	(*RestartDirective)(nil),  // 4: internalpb.RestartDirective
}
var file_internal_supervision_proto_depIdxs = []int32{
	1, // 0: internalpb.HandleFault.stop:type_name -> internalpb.StopDirective
	2, // 1: internalpb.HandleFault.resume:type_name -> internalpb.ResumeDirective
	4, // 2: internalpb.HandleFault.restart:type_name -> internalpb.RestartDirective
	3, // 3: internalpb.HandleFault.escalate:type_name -> internalpb.EscalateDirective
	4, // [4:4] is the sub-list for method output_type
	4, // [4:4] is the sub-list for method input_type
	4, // [4:4] is the sub-list for extension type_name
	4, // [4:4] is the sub-list for extension extendee
	0, // [0:4] is the sub-list for field type_name
}

func init() { file_internal_supervision_proto_init() }
func file_internal_supervision_proto_init() {
	if File_internal_supervision_proto != nil {
		return
	}
	file_internal_supervision_proto_msgTypes[0].OneofWrappers = []any{
		(*HandleFault_Stop)(nil),
		(*HandleFault_Resume)(nil),
		(*HandleFault_Restart)(nil),
		(*HandleFault_Escalate)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: unsafe.Slice(unsafe.StringData(file_internal_supervision_proto_rawDesc), len(file_internal_supervision_proto_rawDesc)),
			NumEnums:      0,
			NumMessages:   5,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_internal_supervision_proto_goTypes,
		DependencyIndexes: file_internal_supervision_proto_depIdxs,
		MessageInfos:      file_internal_supervision_proto_msgTypes,
	}.Build()
	File_internal_supervision_proto = out.File
	file_internal_supervision_proto_goTypes = nil
	file_internal_supervision_proto_depIdxs = nil
}
