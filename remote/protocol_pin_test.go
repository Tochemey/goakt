// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package remote

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtocolPin(t *testing.T) {
	assert.Equal(t, "auto", ProtocolPinAuto.String())
	assert.Equal(t, "legacy", ProtocolPinLegacy.String())
	assert.Equal(t, "duplex", ProtocolPinDuplex.String())
	assert.Equal(t, "unknown", ProtocolPin(99).String())

	assert.True(t, ProtocolPinAuto.Valid())
	assert.True(t, ProtocolPinLegacy.Valid())
	assert.True(t, ProtocolPinDuplex.Valid())
	assert.False(t, ProtocolPin(99).Valid())
}

func TestConfigProtocolPinDefaultAndOption(t *testing.T) {
	cfg := NewConfig("127.0.0.1", 0)
	assert.Equal(t, ProtocolPinAuto, cfg.ProtocolPin())
	require.NoError(t, cfg.Validate())

	cfg = NewConfig("127.0.0.1", 0, WithProtocolPin(ProtocolPinDuplex))
	assert.Equal(t, ProtocolPinDuplex, cfg.ProtocolPin())
	require.NoError(t, cfg.Validate())

	cfg = NewConfig("127.0.0.1", 0)
	cfg.protocolPin = ProtocolPin(99)
	require.Error(t, cfg.Validate())
}
