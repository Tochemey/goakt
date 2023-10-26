// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.31.0
// 	protoc        (unknown)
// source: scheduler/v1/scheduler.proto

package schedulerpb

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

// Once states that the scheduled message
// will only be sent once to the receiving actor
type Once struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the time when the to send the message
	// The timestamp must include the following:
	// year, month, day of month, hour, minute and second
	When *timestamppb.Timestamp `protobuf:"bytes,1,opt,name=when,proto3" json:"when,omitempty"`
}

func (x *Once) Reset() {
	*x = Once{}
	if protoimpl.UnsafeEnabled {
		mi := &file_scheduler_v1_scheduler_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Once) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Once) ProtoMessage() {}

func (x *Once) ProtoReflect() protoreflect.Message {
	mi := &file_scheduler_v1_scheduler_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Once.ProtoReflect.Descriptor instead.
func (*Once) Descriptor() ([]byte, []int) {
	return file_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{0}
}

func (x *Once) GetWhen() *timestamppb.Timestamp {
	if x != nil {
		return x.When
	}
	return nil
}

// Repeatedly states that the scheduled message
// will be sent to the receiving actor n times with a given delay interval
type Repeatedly struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the number of times to send the message
	Times int32 `protobuf:"varint,1,opt,name=times,proto3" json:"times,omitempty"`
	// Specifies the delay interval between messages sent
	Delay int64 `protobuf:"varint,2,opt,name=delay,proto3" json:"delay,omitempty"`
	// Specifies the time when to send the message
	When *Time `protobuf:"bytes,3,opt,name=when,proto3" json:"when,omitempty"`
}

func (x *Repeatedly) Reset() {
	*x = Repeatedly{}
	if protoimpl.UnsafeEnabled {
		mi := &file_scheduler_v1_scheduler_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Repeatedly) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Repeatedly) ProtoMessage() {}

func (x *Repeatedly) ProtoReflect() protoreflect.Message {
	mi := &file_scheduler_v1_scheduler_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Repeatedly.ProtoReflect.Descriptor instead.
func (*Repeatedly) Descriptor() ([]byte, []int) {
	return file_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{1}
}

func (x *Repeatedly) GetTimes() int32 {
	if x != nil {
		return x.Times
	}
	return 0
}

func (x *Repeatedly) GetDelay() int64 {
	if x != nil {
		return x.Delay
	}
	return 0
}

func (x *Repeatedly) GetWhen() *Time {
	if x != nil {
		return x.When
	}
	return nil
}

// Frequency defines how often the scheduled message should be sent
// to the receiving actor
type Frequency struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Specifies the frequency value.
	//
	// Types that are assignable to Value:
	//
	//	*Frequency_Once
	//	*Frequency_Repeatedly
	Value isFrequency_Value `protobuf_oneof:"value"`
}

func (x *Frequency) Reset() {
	*x = Frequency{}
	if protoimpl.UnsafeEnabled {
		mi := &file_scheduler_v1_scheduler_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Frequency) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Frequency) ProtoMessage() {}

func (x *Frequency) ProtoReflect() protoreflect.Message {
	mi := &file_scheduler_v1_scheduler_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Frequency.ProtoReflect.Descriptor instead.
func (*Frequency) Descriptor() ([]byte, []int) {
	return file_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{2}
}

func (m *Frequency) GetValue() isFrequency_Value {
	if m != nil {
		return m.Value
	}
	return nil
}

func (x *Frequency) GetOnce() *Once {
	if x, ok := x.GetValue().(*Frequency_Once); ok {
		return x.Once
	}
	return nil
}

func (x *Frequency) GetRepeatedly() *Repeatedly {
	if x, ok := x.GetValue().(*Frequency_Repeatedly); ok {
		return x.Repeatedly
	}
	return nil
}

type isFrequency_Value interface {
	isFrequency_Value()
}

type Frequency_Once struct {
	// Specifies to send the message once
	Once *Once `protobuf:"bytes,1,opt,name=once,proto3,oneof"`
}

type Frequency_Repeatedly struct {
	// Specifies the send the message n times
	Repeatedly *Repeatedly `protobuf:"bytes,2,opt,name=repeatedly,proto3,oneof"`
}

func (*Frequency_Once) isFrequency_Value() {}

func (*Frequency_Repeatedly) isFrequency_Value() {}

// Time defines the date time of a scheduled message
type Time struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	// Month of year. Must be from 1 to 12.
	Month int32 `protobuf:"varint,2,opt,name=month,proto3" json:"month,omitempty"`
	// Day of month. Must be from 1 to 31 and valid for the year and
	// month.
	Day int32 `protobuf:"varint,3,opt,name=day,proto3" json:"day,omitempty"`
	// Hours of day in 24 hour format. Should be from 0 to 23. An API
	// may choose to allow the value "24:00:00" for scenarios like business
	// closing time.
	Hours int32 `protobuf:"varint,4,opt,name=hours,proto3" json:"hours,omitempty"`
	// Minutes of hour of day. Must be from 0 to 59.
	Minutes int32 `protobuf:"varint,5,opt,name=minutes,proto3" json:"minutes,omitempty"`
	// Required. Seconds of minutes of the time. Must normally be from 0 to 59. An
	// API may allow the value 60 if it allows leap-seconds.
	Seconds int32 `protobuf:"varint,6,opt,name=seconds,proto3" json:"seconds,omitempty"`
}

func (x *Time) Reset() {
	*x = Time{}
	if protoimpl.UnsafeEnabled {
		mi := &file_scheduler_v1_scheduler_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Time) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Time) ProtoMessage() {}

func (x *Time) ProtoReflect() protoreflect.Message {
	mi := &file_scheduler_v1_scheduler_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Time.ProtoReflect.Descriptor instead.
func (*Time) Descriptor() ([]byte, []int) {
	return file_scheduler_v1_scheduler_proto_rawDescGZIP(), []int{3}
}

func (x *Time) GetMonth() int32 {
	if x != nil {
		return x.Month
	}
	return 0
}

func (x *Time) GetDay() int32 {
	if x != nil {
		return x.Day
	}
	return 0
}

func (x *Time) GetHours() int32 {
	if x != nil {
		return x.Hours
	}
	return 0
}

func (x *Time) GetMinutes() int32 {
	if x != nil {
		return x.Minutes
	}
	return 0
}

func (x *Time) GetSeconds() int32 {
	if x != nil {
		return x.Seconds
	}
	return 0
}

var File_scheduler_v1_scheduler_proto protoreflect.FileDescriptor

var file_scheduler_v1_scheduler_proto_rawDesc = []byte{
	0x0a, 0x1c, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2f, 0x76, 0x31, 0x2f, 0x73,
	0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x0c,
	0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e, 0x76, 0x31, 0x1a, 0x1f, 0x67, 0x6f,
	0x6f, 0x67, 0x6c, 0x65, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x62, 0x75, 0x66, 0x2f, 0x74, 0x69,
	0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x36, 0x0a,
	0x04, 0x4f, 0x6e, 0x63, 0x65, 0x12, 0x2e, 0x0a, 0x04, 0x77, 0x68, 0x65, 0x6e, 0x18, 0x01, 0x20,
	0x01, 0x28, 0x0b, 0x32, 0x1a, 0x2e, 0x67, 0x6f, 0x6f, 0x67, 0x6c, 0x65, 0x2e, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x62, 0x75, 0x66, 0x2e, 0x54, 0x69, 0x6d, 0x65, 0x73, 0x74, 0x61, 0x6d, 0x70, 0x52,
	0x04, 0x77, 0x68, 0x65, 0x6e, 0x22, 0x60, 0x0a, 0x0a, 0x52, 0x65, 0x70, 0x65, 0x61, 0x74, 0x65,
	0x64, 0x6c, 0x79, 0x12, 0x14, 0x0a, 0x05, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x05, 0x52, 0x05, 0x74, 0x69, 0x6d, 0x65, 0x73, 0x12, 0x14, 0x0a, 0x05, 0x64, 0x65, 0x6c,
	0x61, 0x79, 0x18, 0x02, 0x20, 0x01, 0x28, 0x03, 0x52, 0x05, 0x64, 0x65, 0x6c, 0x61, 0x79, 0x12,
	0x26, 0x0a, 0x04, 0x77, 0x68, 0x65, 0x6e, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x12, 0x2e,
	0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e, 0x76, 0x31, 0x2e, 0x54, 0x69, 0x6d,
	0x65, 0x52, 0x04, 0x77, 0x68, 0x65, 0x6e, 0x22, 0x7a, 0x0a, 0x09, 0x46, 0x72, 0x65, 0x71, 0x75,
	0x65, 0x6e, 0x63, 0x79, 0x12, 0x28, 0x0a, 0x04, 0x6f, 0x6e, 0x63, 0x65, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x12, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e, 0x76,
	0x31, 0x2e, 0x4f, 0x6e, 0x63, 0x65, 0x48, 0x00, 0x52, 0x04, 0x6f, 0x6e, 0x63, 0x65, 0x12, 0x3a,
	0x0a, 0x0a, 0x72, 0x65, 0x70, 0x65, 0x61, 0x74, 0x65, 0x64, 0x6c, 0x79, 0x18, 0x02, 0x20, 0x01,
	0x28, 0x0b, 0x32, 0x18, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e, 0x76,
	0x31, 0x2e, 0x52, 0x65, 0x70, 0x65, 0x61, 0x74, 0x65, 0x64, 0x6c, 0x79, 0x48, 0x00, 0x52, 0x0a,
	0x72, 0x65, 0x70, 0x65, 0x61, 0x74, 0x65, 0x64, 0x6c, 0x79, 0x42, 0x07, 0x0a, 0x05, 0x76, 0x61,
	0x6c, 0x75, 0x65, 0x22, 0x78, 0x0a, 0x04, 0x54, 0x69, 0x6d, 0x65, 0x12, 0x14, 0x0a, 0x05, 0x6d,
	0x6f, 0x6e, 0x74, 0x68, 0x18, 0x02, 0x20, 0x01, 0x28, 0x05, 0x52, 0x05, 0x6d, 0x6f, 0x6e, 0x74,
	0x68, 0x12, 0x10, 0x0a, 0x03, 0x64, 0x61, 0x79, 0x18, 0x03, 0x20, 0x01, 0x28, 0x05, 0x52, 0x03,
	0x64, 0x61, 0x79, 0x12, 0x14, 0x0a, 0x05, 0x68, 0x6f, 0x75, 0x72, 0x73, 0x18, 0x04, 0x20, 0x01,
	0x28, 0x05, 0x52, 0x05, 0x68, 0x6f, 0x75, 0x72, 0x73, 0x12, 0x18, 0x0a, 0x07, 0x6d, 0x69, 0x6e,
	0x75, 0x74, 0x65, 0x73, 0x18, 0x05, 0x20, 0x01, 0x28, 0x05, 0x52, 0x07, 0x6d, 0x69, 0x6e, 0x75,
	0x74, 0x65, 0x73, 0x12, 0x18, 0x0a, 0x07, 0x73, 0x65, 0x63, 0x6f, 0x6e, 0x64, 0x73, 0x18, 0x06,
	0x20, 0x01, 0x28, 0x05, 0x52, 0x07, 0x73, 0x65, 0x63, 0x6f, 0x6e, 0x64, 0x73, 0x42, 0xa9, 0x01,
	0x0a, 0x10, 0x63, 0x6f, 0x6d, 0x2e, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e,
	0x76, 0x31, 0x42, 0x0e, 0x53, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x50, 0x72, 0x6f,
	0x74, 0x6f, 0x48, 0x02, 0x50, 0x01, 0x5a, 0x32, 0x67, 0x69, 0x74, 0x68, 0x75, 0x62, 0x2e, 0x63,
	0x6f, 0x6d, 0x2f, 0x74, 0x6f, 0x63, 0x68, 0x65, 0x6d, 0x65, 0x79, 0x2f, 0x67, 0x6f, 0x61, 0x6b,
	0x74, 0x2f, 0x73, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2f, 0x76, 0x31, 0x3b, 0x73,
	0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x70, 0x62, 0xa2, 0x02, 0x03, 0x53, 0x58, 0x58,
	0xaa, 0x02, 0x0c, 0x53, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x2e, 0x56, 0x31, 0xca,
	0x02, 0x0c, 0x53, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x5c, 0x56, 0x31, 0xe2, 0x02,
	0x18, 0x53, 0x63, 0x68, 0x65, 0x64, 0x75, 0x6c, 0x65, 0x72, 0x5c, 0x56, 0x31, 0x5c, 0x47, 0x50,
	0x42, 0x4d, 0x65, 0x74, 0x61, 0x64, 0x61, 0x74, 0x61, 0xea, 0x02, 0x0d, 0x53, 0x63, 0x68, 0x65,
	0x64, 0x75, 0x6c, 0x65, 0x72, 0x3a, 0x3a, 0x56, 0x31, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x33,
}

var (
	file_scheduler_v1_scheduler_proto_rawDescOnce sync.Once
	file_scheduler_v1_scheduler_proto_rawDescData = file_scheduler_v1_scheduler_proto_rawDesc
)

func file_scheduler_v1_scheduler_proto_rawDescGZIP() []byte {
	file_scheduler_v1_scheduler_proto_rawDescOnce.Do(func() {
		file_scheduler_v1_scheduler_proto_rawDescData = protoimpl.X.CompressGZIP(file_scheduler_v1_scheduler_proto_rawDescData)
	})
	return file_scheduler_v1_scheduler_proto_rawDescData
}

var file_scheduler_v1_scheduler_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_scheduler_v1_scheduler_proto_goTypes = []interface{}{
	(*Once)(nil),                  // 0: scheduler.v1.Once
	(*Repeatedly)(nil),            // 1: scheduler.v1.Repeatedly
	(*Frequency)(nil),             // 2: scheduler.v1.Frequency
	(*Time)(nil),                  // 3: scheduler.v1.Time
	(*timestamppb.Timestamp)(nil), // 4: google.protobuf.Timestamp
}
var file_scheduler_v1_scheduler_proto_depIdxs = []int32{
	4, // 0: scheduler.v1.Once.when:type_name -> google.protobuf.Timestamp
	3, // 1: scheduler.v1.Repeatedly.when:type_name -> scheduler.v1.Time
	0, // 2: scheduler.v1.Frequency.once:type_name -> scheduler.v1.Once
	1, // 3: scheduler.v1.Frequency.repeatedly:type_name -> scheduler.v1.Repeatedly
	4, // [4:4] is the sub-list for method output_type
	4, // [4:4] is the sub-list for method input_type
	4, // [4:4] is the sub-list for extension type_name
	4, // [4:4] is the sub-list for extension extendee
	0, // [0:4] is the sub-list for field type_name
}

func init() { file_scheduler_v1_scheduler_proto_init() }
func file_scheduler_v1_scheduler_proto_init() {
	if File_scheduler_v1_scheduler_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_scheduler_v1_scheduler_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Once); i {
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
		file_scheduler_v1_scheduler_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Repeatedly); i {
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
		file_scheduler_v1_scheduler_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Frequency); i {
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
		file_scheduler_v1_scheduler_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Time); i {
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
	file_scheduler_v1_scheduler_proto_msgTypes[2].OneofWrappers = []interface{}{
		(*Frequency_Once)(nil),
		(*Frequency_Repeatedly)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_scheduler_v1_scheduler_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_scheduler_v1_scheduler_proto_goTypes,
		DependencyIndexes: file_scheduler_v1_scheduler_proto_depIdxs,
		MessageInfos:      file_scheduler_v1_scheduler_proto_msgTypes,
	}.Build()
	File_scheduler_v1_scheduler_proto = out.File
	file_scheduler_v1_scheduler_proto_rawDesc = nil
	file_scheduler_v1_scheduler_proto_goTypes = nil
	file_scheduler_v1_scheduler_proto_depIdxs = nil
}
