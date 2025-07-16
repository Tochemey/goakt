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

package actor

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/registry"
)

func TestReflection(t *testing.T) {
	t.Run("With NewActor happy path", func(t *testing.T) {
		newRegistry := registry.NewRegistry()
		actor := NewMockActor()
		newRegistry.Register(actor)
		reflection := newReflection(newRegistry)
		actual, err := reflection.NewActor("actor.MockActor")
		assert.NoError(t, err)
		assert.NotNil(t, actual)
		assert.IsType(t, new(MockActor), actual)
	})
	t.Run("With NewActor actor not found", func(t *testing.T) {
		newRegistry := registry.NewRegistry()
		reflection := newReflection(newRegistry)
		actual, err := reflection.NewActor("actor.fakeActor")
		assert.Error(t, err)
		assert.Nil(t, actual)
	})
	t.Run("With unregistered actor", func(t *testing.T) {
		tl := registry.NewRegistry()
		reflection := newReflection(tl)
		actual, err := reflection.NewActor("actor.fakeActor")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrTypeNotRegistered)
		assert.Nil(t, actual)
	})
	t.Run("With NewActor actor interface not implemented", func(t *testing.T) {
		newRegistry := registry.NewRegistry()
		type normalStruct struct{}
		newRegistry.Register(new(normalStruct))
		reflection := newReflection(newRegistry)
		actual, err := reflection.NewActor("actor.normalStruct")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInstanceNotAnActor)
		assert.Nil(t, actual)
	})
	t.Run("With NewDependency happy path", func(t *testing.T) {
		newRegistry := registry.NewRegistry()
		dependency := NewMockDependency("1", "userName", "email")
		newRegistry.Register(dependency)
		reflection := newReflection(newRegistry)
		typeName := registry.Name(dependency)
		bytea, err := dependency.MarshalBinary()
		require.NoError(t, err)
		require.NotNil(t, bytea)
		require.NotEmpty(t, bytea)

		actual, err := reflection.NewDependency(typeName, bytea)
		require.NoError(t, err)
		require.NotNil(t, actual)
		require.IsType(t, dependency, actual)
		require.True(t, reflect.DeepEqual(dependency, actual))
	})
	t.Run("With NewDependency Dependency interface not implemented", func(t *testing.T) {
		newRegistry := registry.NewRegistry()
		type normalStruct struct{}
		newRegistry.Register(new(normalStruct))
		reflection := newReflection(newRegistry)
		actual, err := reflection.NewDependency("actor.normalStruct", []byte{})
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInstanceNotDependency)
		assert.Nil(t, actual)
	})
	t.Run("With unregistered dependency", func(t *testing.T) {
		tl := registry.NewRegistry()
		reflection := newReflection(tl)
		actual, err := reflection.NewDependency("actor.fakeDependency", []byte{})
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrDependencyTypeNotRegistered)
		assert.Nil(t, actual)
	})
	t.Run("With NewDependency UnmarshalBinary failure", func(t *testing.T) {
		newRegistry := registry.NewRegistry()
		dependency := NewMockDependency("1", "userName", "email")
		newRegistry.Register(dependency)
		reflection := newReflection(newRegistry)
		typeName := registry.Name(dependency)

		actual, err := reflection.NewDependency(typeName, []byte("invalid"))
		require.Error(t, err)
		require.Nil(t, actual)
	})
	t.Run("With DependenciesFromProtobuf happy path", func(t *testing.T) {
		newRegistry := registry.NewRegistry()
		dependency := NewMockDependency("1", "userName", "email")
		newRegistry.Register(dependency)
		reflection := newReflection(newRegistry)
		typeName := registry.Name(dependency)
		bytea, err := dependency.MarshalBinary()
		require.NoError(t, err)
		require.NotNil(t, bytea)
		require.NotEmpty(t, bytea)

		pb := &internalpb.Dependency{
			Id:       dependency.ID(),
			TypeName: typeName,
			Bytea:    bytea,
		}

		dependencies, err := reflection.NewDependencies(pb)
		require.NoError(t, err)
		require.NotNil(t, dependencies)
		require.Len(t, dependencies, 1)
		actual := dependencies[0]
		require.IsType(t, dependency, actual)
		require.True(t, reflect.DeepEqual(dependency, actual))
	})
	t.Run("With DependenciesFromProtobuf UnmarshalBinary failure", func(t *testing.T) {
		newRegistry := registry.NewRegistry()
		dependency := NewMockDependency("1", "userName", "email")
		newRegistry.Register(dependency)
		reflection := newReflection(newRegistry)
		typeName := registry.Name(dependency)

		pb := &internalpb.Dependency{
			Id:       dependency.ID(),
			TypeName: typeName,
			Bytea:    []byte("invalid"),
		}

		dependencies, err := reflection.NewDependencies(pb)
		require.Error(t, err)
		require.Nil(t, dependencies)
		require.Empty(t, dependencies)
	})
	t.Run("With NewGrain happy path", func(t *testing.T) {
		newRegistry := registry.NewRegistry()
		grain := NewMockGrain()
		newRegistry.Register(grain)
		reflection := newReflection(newRegistry)
		actual, err := reflection.NewGrain("actor.MockGrain")
		assert.NoError(t, err)
		assert.NotNil(t, actual)
		assert.IsType(t, new(MockGrain), actual)
	})
	t.Run("With NewGrain grain not found", func(t *testing.T) {
		newRegistry := registry.NewRegistry()
		reflection := newReflection(newRegistry)
		actual, err := reflection.NewGrain("actor.fakeGrain")
		assert.Error(t, err)
		assert.Nil(t, actual)
	})
	t.Run("With unregistered grain", func(t *testing.T) {
		tl := registry.NewRegistry()
		reflection := newReflection(tl)
		actual, err := reflection.NewGrain("actor.fakeGrain")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrGrainNotRegistered)
		assert.Nil(t, actual)
	})
	t.Run("With NewGrain Grain interface not implemented", func(t *testing.T) {
		newRegistry := registry.NewRegistry()
		type normalStruct struct{}
		newRegistry.Register(new(normalStruct))
		reflection := newReflection(newRegistry)
		actual, err := reflection.NewGrain("actor.normalStruct")
		assert.Error(t, err)
		assert.ErrorIs(t, err, ErrInstanceNotAnGrain)
		assert.Nil(t, actual)
	})
}
