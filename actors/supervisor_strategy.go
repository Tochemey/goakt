/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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
	"reflect"
	"runtime"
	"sync"
)

// DefaultSupervisorStrategies defines the default supervisor strategies
var DefaultSupervisorStrategies = []*SupervisorStrategy{
	NewSupervisorStrategy(PanicError{}, NewStopDirective()),
	NewSupervisorStrategy(&runtime.PanicNilError{}, NewStopDirective()),
}

// SupervisorStrategy defines the rules to apply to a faulty actor
// during message processing
type SupervisorStrategy struct {
	// specifies the type of directive to apply
	directive SupervisorDirective
	// specifies the error type
	err error
}

// NewSupervisorStrategy creates an instance of SupervisorStrategy
func NewSupervisorStrategy(err error, directive SupervisorDirective) *SupervisorStrategy {
	return &SupervisorStrategy{
		directive: directive,
		err:       err,
	}
}

// Directive returns the directive of the supervisor strategy
func (s *SupervisorStrategy) Directive() SupervisorDirective {
	return s.directive
}

// Kind returns the error type of the supervisor strategy
func (s *SupervisorStrategy) Error() error {
	return s.err
}

// strategiesMap defines the strategies map
// this will be use internally by actor to define their supervisor strategies
type strategiesMap struct {
	rwMutex sync.RWMutex
	data    map[string]*SupervisorStrategy
}

// newStrategiesMap creates an instance of strategiesMap
func newStrategiesMap() *strategiesMap {
	return &strategiesMap{
		data:    make(map[string]*SupervisorStrategy),
		rwMutex: sync.RWMutex{},
	}
}

// Put sets the supervisor strategy
func (m *strategiesMap) Put(strategy *SupervisorStrategy) {
	m.rwMutex.Lock()
	key := errorType(strategy.Error())
	m.data[key] = strategy
	m.rwMutex.Unlock()
}

// Get retrieves based upon the error the given strategy
func (m *strategiesMap) Get(err error) (val *SupervisorStrategy, ok bool) {
	m.rwMutex.RLock()
	key := errorType(err)
	val, ok = m.data[key]
	m.rwMutex.RUnlock()
	return val, ok
}

// Reset resets the strategiesMap
func (m *strategiesMap) Reset() {
	m.rwMutex.Lock()
	m.data = make(map[string]*SupervisorStrategy)
	m.rwMutex.Unlock()
}

// errorType returns the string representation of an error's type using reflection
func errorType(err error) string {
	// Handle nil errors first
	if err == nil {
		return "nil"
	}

	rtype := reflect.TypeOf(err)
	if rtype.Kind() == reflect.Ptr {
		rtype = rtype.Elem()
	}

	return rtype.String()
}
