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

package types

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/internalpb"
)

// usesRegistryStub implements UsesRegistry for testing.
type usesRegistryStub struct{}

func (*usesRegistryStub) RegistryRequired() {}

func TestGlobalRegistry(t *testing.T) {
	t.Run("GlobalRegistry is non-nil", func(t *testing.T) {
		require.NotNil(t, GlobalRegistry)
	})
}

func TestRegisterSerializerType(t *testing.T) {
	type plainStruct struct{ X int }

	t.Run("registers when serializer implements UsesRegistry and msg is concrete non-proto", func(t *testing.T) {
		RegisterSerializerType(new(plainStruct), &usesRegistryStub{})
		t.Cleanup(func() { GlobalRegistry.Deregister(new(plainStruct)) })

		require.True(t, GlobalRegistry.Exists(new(plainStruct)))
	})

	t.Run("no-op when serializer does not implement UsesRegistry", func(t *testing.T) {
		type otherSerializer struct{}
		msg := new(plainStruct)

		RegisterSerializerType(msg, &otherSerializer{})

		require.False(t, GlobalRegistry.Exists(msg))
	})

	t.Run("no-op when msg is interface registration", func(t *testing.T) {
		type someInterface interface{ Foo() }
		// (*someInterface)(nil) is a typed nil pointer to interface
		RegisterSerializerType((*someInterface)(nil), &usesRegistryStub{})

		// GlobalRegistry should not have an entry for the interface type
		require.False(t, GlobalRegistry.Exists((*someInterface)(nil)))
	})

	t.Run("no-op when msg is proto message type", func(t *testing.T) {
		msg := new(internalpb.GrainId)
		RegisterSerializerType(msg, &usesRegistryStub{})

		// Proto types use protoregistry, not GlobalRegistry; should not register
		require.False(t, GlobalRegistry.Exists(msg))
	})

	t.Run("idempotent", func(t *testing.T) {
		msg := new(plainStruct)
		RegisterSerializerType(msg, &usesRegistryStub{})
		RegisterSerializerType(msg, &usesRegistryStub{})
		t.Cleanup(func() { GlobalRegistry.Deregister(msg) })

		require.True(t, GlobalRegistry.Exists(msg))
	})

	t.Run("multiple types", func(t *testing.T) {
		type anotherStruct struct{ Y string }
		msg1 := new(plainStruct)
		msg2 := new(anotherStruct)

		RegisterSerializerType(msg1, &usesRegistryStub{})
		RegisterSerializerType(msg2, &usesRegistryStub{})
		t.Cleanup(func() {
			GlobalRegistry.Deregister(msg1)
			GlobalRegistry.Deregister(msg2)
		})

		require.True(t, GlobalRegistry.Exists(msg1))
		require.True(t, GlobalRegistry.Exists(msg2))
	})
}
