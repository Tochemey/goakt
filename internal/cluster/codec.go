/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/internal/internalpb"
)

// encode marshals a wire actor into a byte array
// the output of this function can be persisted to the Cluster
func encode(actor *internalpb.Actor) ([]byte, error) {
	// let us marshal it
	bytea, _ := proto.Marshal(actor)
	return bytea, nil
}

// decode decodes the encoded representation of a wire actor
func decode(bytea []byte) (*internalpb.Actor, error) {
	// create an instance of a proto message
	actor := new(internalpb.Actor)
	// let us unpack the byte array
	if err := proto.Unmarshal(bytea, actor); err != nil {
		return nil, err
	}

	return actor, nil
}

// encode marshals a wire Grain into a byte array
// the output of this function can be persisted to the Cluster
func encodeGrain(grain *internalpb.Grain) ([]byte, error) {
	// let us marshal it
	bytea, _ := proto.Marshal(grain)
	return bytea, nil
}

// decode decodes the encoded representation of a wire Grain
func decodeGrain(bytea []byte) (*internalpb.Grain, error) {
	// create an instance of a proto message
	grain := new(internalpb.Grain)
	// let us unpack the byte array
	if err := proto.Unmarshal(bytea, grain); err != nil {
		return nil, err
	}

	return grain, nil
}
