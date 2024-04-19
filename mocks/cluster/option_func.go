// Code generated by mockery v2.32.4. DO NOT EDIT.

package cluster

import (
	mock "github.com/stretchr/testify/mock"
	internalcluster "github.com/tochemey/goakt/internal/cluster"
)

// OptionFunc is an autogenerated mock type for the OptionFunc type
type OptionFunc struct {
	mock.Mock
}

type OptionFunc_Expecter struct {
	mock *mock.Mock
}

func (_m *OptionFunc) EXPECT() *OptionFunc_Expecter {
	return &OptionFunc_Expecter{mock: &_m.Mock}
}

// Execute provides a mock function with given fields: cl
func (_m *OptionFunc) Execute(cl *internalcluster.Node) {
	_m.Called(cl)
}

// OptionFunc_Execute_Call is a *mock.Call that shadows Run/Return methods with type explicit version for method 'Execute'
type OptionFunc_Execute_Call struct {
	*mock.Call
}

// Execute is a helper method to define mock.On call
//   - cl *internalcluster.Node
func (_e *OptionFunc_Expecter) Execute(cl interface{}) *OptionFunc_Execute_Call {
	return &OptionFunc_Execute_Call{Call: _e.mock.On("Execute", cl)}
}

func (_c *OptionFunc_Execute_Call) Run(run func(cl *internalcluster.Node)) *OptionFunc_Execute_Call {
	_c.Call.Run(func(args mock.Arguments) {
		run(args[0].(*internalcluster.Node))
	})
	return _c
}

func (_c *OptionFunc_Execute_Call) Return() *OptionFunc_Execute_Call {
	_c.Call.Return()
	return _c
}

func (_c *OptionFunc_Execute_Call) RunAndReturn(run func(*internalcluster.Node)) *OptionFunc_Execute_Call {
	_c.Call.Return(run)
	return _c
}

// NewOptionFunc creates a new instance of OptionFunc. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
// The first argument is typically a *testing.T value.
func NewOptionFunc(t interface {
	mock.TestingT
	Cleanup(func())
}) *OptionFunc {
	mock := &OptionFunc{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
