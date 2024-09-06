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

package actors

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/prototext"

	"github.com/tochemey/goakt/v2/goaktpb"
)

func TestPath(t *testing.T) {
	name := "testActor"
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
	require.Equal(t, name, path.Name())
	require.Equal(t, "goakt://Sys@localhost:888/testActor", path.String())
	remoteAddr := &goaktpb.Address{
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

	pathCopy := path
	require.True(t, path.Equals(pathCopy))
}
