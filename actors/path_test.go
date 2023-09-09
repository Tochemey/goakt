package actors

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	addresspb "github.com/tochemey/goakt/pb/address/v1"
	"google.golang.org/protobuf/encoding/prototext"
)

func TestPath(t *testing.T) {
	name := "Tester"
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
	assert.Equal(t, name, path.Name())
	assert.Equal(t, "goakt://Sys@localhost:888/Tester", path.String())
	remoteAddr := &addresspb.Address{
		Host: "localhost",
		Port: 888,
		Name: name,
		Id:   path.ID().String(),
	}

	pathRemoteAddr := path.RemoteAddress()
	assert.Equal(t, prototext.Format(remoteAddr), prototext.Format(pathRemoteAddr))

	parent := NewPath("parent", &Address{
		host:     "localhost",
		port:     887,
		system:   "Sys",
		protocol: protocol,
	})

	newPath := path.WithParent(parent)
	assert.True(t, cmp.Equal(parent, newPath.Parent(), cmpopts.IgnoreUnexported(Path{})))
}
