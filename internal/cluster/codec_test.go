/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/internalpb"
)

func TestCodec(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		// create an instance of wire actor
		actor := &internalpb.Actor{
			Address: &goaktpb.Address{
				Host: "127.0.0.1",
				Port: 2345,
				Name: "account-1",
				Id:   uuid.NewString(),
			},
		}

		// encode the actor
		actual, err := encode(actor)
		require.NoError(t, err)
		assert.NotEmpty(t, actual)

		// decode the actor
		decoded, err := decode(actual)
		require.NoError(t, err)
		assert.True(t, proto.Equal(actor, decoded))
	})
	t.Run("With invalid encoded actor", func(t *testing.T) {
		decoded, err := decode([]byte("invalid proto message"))
		require.Error(t, err)
		assert.Nil(t, decoded)
	})
	t.Run("With Grain encoding", func(t *testing.T) {
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{},
			Host:    "127.0.0.1",
			Port:    2345,
		}
		// encode the grain
		actual, err := encodeGrain(grain)
		require.NoError(t, err)
		require.NotNil(t, actual)
	})

	t.Run("With Grain decoding", func(t *testing.T) {
		grain := &internalpb.Grain{
			GrainId: &internalpb.GrainId{},
			Host:    "127.0.0.1",
			Port:    2345,
		}
		// encode the grain
		actual, err := encodeGrain(grain)
		require.NoError(t, err)
		require.NotNil(t, actual)

		decoded, err := decodeGrain(actual)
		require.NoError(t, err)
		assert.True(t, proto.Equal(grain, decoded))
	})
	t.Run("With invalid encoded grain", func(t *testing.T) {
		decoded, err := decodeGrain([]byte("invalid proto message"))
		require.Error(t, err)
		assert.Nil(t, decoded)
	})
}
