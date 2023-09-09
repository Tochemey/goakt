package cluster

import (
	"encoding/base64"

	internalpb "github.com/tochemey/goakt/internal/v1"
	"google.golang.org/protobuf/proto"
)

// encode marshals a wire actor into a base64 string
// the output of this function can be persisted to the Cluster
func encode(actor *internalpb.WireActor) (string, error) {
	// let us marshal it
	bytea, _ := proto.Marshal(actor)
	// let us base64 encode the bytea before sending it into the Cluster
	return base64.StdEncoding.EncodeToString(bytea), nil
}

// decode decodes the encoded base64 representation of a wire actor
func decode(base64Str string) (*internalpb.WireActor, error) {
	// let base64 decode the data before parsing it
	bytea, err := base64.StdEncoding.DecodeString(base64Str)
	// handle the error
	if err != nil {
		return nil, err
	}

	// create an instance of proto message
	actor := new(internalpb.WireActor)
	// let us unpack the byte array
	if err := proto.Unmarshal(bytea, actor); err != nil {
		return nil, err
	}

	return actor, nil
}
