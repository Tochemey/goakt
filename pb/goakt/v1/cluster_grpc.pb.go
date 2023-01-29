// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.2.0
// - protoc             (unknown)
// source: goakt/v1/cluster.proto

package goaktv1

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// GossipServiceClient is the client API for GossipService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type GossipServiceClient interface {
	Put(ctx context.Context, in *PutRequest, opts ...grpc.CallOption) (*PutResponse, error)
	Get(ctx context.Context, in *GetRequest, opts ...grpc.CallOption) (*GetResponse, error)
}

type gossipServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewGossipServiceClient(cc grpc.ClientConnInterface) GossipServiceClient {
	return &gossipServiceClient{cc}
}

func (c *gossipServiceClient) Put(ctx context.Context, in *PutRequest, opts ...grpc.CallOption) (*PutResponse, error) {
	out := new(PutResponse)
	err := c.cc.Invoke(ctx, "/goakt.v1.GossipService/Put", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *gossipServiceClient) Get(ctx context.Context, in *GetRequest, opts ...grpc.CallOption) (*GetResponse, error) {
	out := new(GetResponse)
	err := c.cc.Invoke(ctx, "/goakt.v1.GossipService/Get", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// GossipServiceServer is the server API for GossipService service.
// All implementations should embed UnimplementedGossipServiceServer
// for forward compatibility
type GossipServiceServer interface {
	Put(context.Context, *PutRequest) (*PutResponse, error)
	Get(context.Context, *GetRequest) (*GetResponse, error)
}

// UnimplementedGossipServiceServer should be embedded to have forward compatible implementations.
type UnimplementedGossipServiceServer struct {
}

func (UnimplementedGossipServiceServer) Put(context.Context, *PutRequest) (*PutResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Put not implemented")
}
func (UnimplementedGossipServiceServer) Get(context.Context, *GetRequest) (*GetResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Get not implemented")
}

// UnsafeGossipServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to GossipServiceServer will
// result in compilation errors.
type UnsafeGossipServiceServer interface {
	mustEmbedUnimplementedGossipServiceServer()
}

func RegisterGossipServiceServer(s grpc.ServiceRegistrar, srv GossipServiceServer) {
	s.RegisterService(&GossipService_ServiceDesc, srv)
}

func _GossipService_Put_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PutRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GossipServiceServer).Put(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/goakt.v1.GossipService/Put",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GossipServiceServer).Put(ctx, req.(*PutRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _GossipService_Get_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GossipServiceServer).Get(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/goakt.v1.GossipService/Get",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GossipServiceServer).Get(ctx, req.(*GetRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// GossipService_ServiceDesc is the grpc.ServiceDesc for GossipService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var GossipService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "goakt.v1.GossipService",
	HandlerType: (*GossipServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Put",
			Handler:    _GossipService_Put_Handler,
		},
		{
			MethodName: "Get",
			Handler:    _GossipService_Get_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "goakt/v1/cluster.proto",
}
