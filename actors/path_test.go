package actors

import (
	"testing"

	comp "github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"google.golang.org/protobuf/encoding/prototext"
)

func TestPath(t *testing.T) {
	name := "TestActor"
	addr := &Address{
		host:     "localhost",
		port:     888,
		system:   "Sys",
		protocol: protocol,
	}

	path := NewPath(name, addr)
	assert.NotNil(t, path)
	assert.IsType(t, new(Path), path)

	// these are just routine assertions
	assert.True(t, comp.Equal(addr, path.Address(), comp.AllowUnexported(Address{})))
	assert.Equal(t, name, path.Name())
	assert.Equal(t, "goakt://Sys@localhost:888/TestActor", path.String())
	remoteAddr := &pb.Address{
		Host: "localhost",
		Port: 888,
		Name: name,
		Id:   path.ID().String(),
	}
	assert.Equal(t, prototext.Format(remoteAddr), prototext.Format(path.RemoteAddress()))
}
