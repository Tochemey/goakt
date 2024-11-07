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

package validation

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type validationTestSuite struct {
	suite.Suite
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestValidation(t *testing.T) {
	suite.Run(t, new(validationTestSuite))
}

func (s *validationTestSuite) TestNewChain() {
	s.Run("new chain without option", func() {
		chain := New()
		s.Assert().NotNil(chain)
	})
	s.Run("new chain with options", func() {
		chain := New(FailFast())
		s.Assert().NotNil(chain)
		s.Assert().True(chain.failFast)
		chain2 := New(AllErrors())
		s.Assert().NotNil(chain2)
		s.Assert().False(chain2.failFast)
	})
}

func (s *validationTestSuite) TestAddValidator() {
	chain := New()
	s.Assert().NotNil(chain)
	s.Assert().Empty(chain.validators)
	chain.AddValidator(NewBooleanValidator(true, ""))
	s.Assert().NotEmpty(chain.validators)
	s.Assert().Equal(1, len(chain.validators))
}

func (s *validationTestSuite) TestAddAssertion() {
	chain := New()
	s.Assert().NotNil(chain)
	s.Assert().Empty(chain.validators)
	chain.AddAssertion(true, "")
	s.Assert().NotEmpty(chain.validators)
	s.Assert().Equal(1, len(chain.validators))
}

func (s *validationTestSuite) TestValidate() {
	s.Run("with single validator", func() {
		chain := New()
		s.Assert().NotNil(chain)
		chain.AddValidator(NewEmptyStringValidator("field", ""))
		s.Assert().Nil(chain.violations)
		err := chain.Validate()
		s.Assert().NotNil(chain.violations)
		s.Assert().Error(err)
		s.Assert().EqualError(err, "the [field] is required")
	})
	s.Run("with multiple validators and FailFast option", func() {
		chain := New(FailFast())
		s.Assert().NotNil(chain)
		chain.
			AddValidator(NewEmptyStringValidator("field", "")).
			AddAssertion(false, "this is false")
		s.Assert().Nil(chain.violations)
		err := chain.Validate()
		s.Assert().Nil(chain.violations)
		s.Assert().Error(err)
		s.Assert().EqualError(err, "the [field] is required")
	})
	s.Run("with multiple validators and AllErrors option", func() {
		chain := New(AllErrors())
		s.Assert().NotNil(chain)
		chain.
			AddValidator(NewEmptyStringValidator("field", "")).
			AddAssertion(false, "this is false")
		s.Assert().Nil(chain.violations)
		err := chain.Validate()
		s.Assert().NotNil(chain.violations)
		s.Assert().Error(err)
		s.Assert().EqualError(err, "the [field] is required; this is false")
	})
}
