/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

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
