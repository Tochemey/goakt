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
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	internalpb "github.com/tochemey/goakt/internal/v1"
	addresspb "github.com/tochemey/goakt/pb/address/v1"
	"google.golang.org/protobuf/proto"
)

func TestCodec(t *testing.T) {
	t.Run("With happy path", func(t *testing.T) {
		// create an instance of wire actor
		actor := &internalpb.WireActor{
			ActorName: "account-1",
			ActorAddress: &addresspb.Address{
				Host: "localhost",
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
		encoded := base64.StdEncoding.EncodeToString([]byte("invalid proto message"))
		decoded, err := decode(encoded)
		require.Error(t, err)
		assert.Nil(t, decoded)
	})
}
