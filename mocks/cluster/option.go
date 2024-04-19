// Code generated by mockery v2.32.4. DO NOT EDIT.

package cluster

import (
	mock "github.com/stretchr/testify/mock"
	internalcluster "github.com/tochemey/goakt/internal/cluster"
)

// Option is an autogenerated mock type for the Option type
type Option struct {
	mock.Mock
}

type Option_Expecter struct {
	mock *mock.Mock
}

func (_m *Option) EXPECT() *Option_Expecter {
	return &Option_Expecter{mock: &_m.Mock}
}

// Apply provides a mock function with given fields: cl
func (_m *Option) Apply(cl *internalcluster.Node) {
	_m.Called(cl)
}

// Option_Apply_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Apply'
type Option_Apply_Call struct {
	*mock.Call
}

// Apply is a helper method to define mock.On call
//   - cl *internalcluster.Node
func (_e *Option_Expecter) Apply(cl interface{}) *Option_Apply_Call {
	return &Option_Apply_Call{Call: _e.mock.On("Apply", cl)}
}

func (_c *Option_Apply_Call) Run(run func(cl *internalcluster.Node)) *Option_Apply_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(*internalcluster.Node))
	})
	return _c
}

func (_c *Option_Apply_Call) Return() *Option_Apply_Call {
	_c.Call.Return()
	return _c
}

func (_c *Option_Apply_Call) RunAndReturn(run func(*internalcluster.Node)) *Option_Apply_Call {
	_c.Call.Return(run)
	return _c
}

// NewOption creates a new instance of Option. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewOption(t interface {
	mock.TestingT
	Cleanup(func())
}) *Option {
	mock := &Option{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
