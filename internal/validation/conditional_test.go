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

package validation

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type conditionalTestSuite struct {
	suite.Suite
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestConditionalValidator(t *testing.T) {
	suite.Run(t, new(conditionalTestSuite))
}

func (s *conditionalTestSuite) TestConditionalValidator() {
	s.Run("with condition set to true", func() {
		fieldName := "field"
		fieldValue := ""
		validator := NewConditionalValidator(true, NewEmptyStringValidator(fieldName, fieldValue))
		err := validator.Validate()
		s.Assert().Error(err)
		s.Assert().EqualError(err, "the [field] is required")
	})
	s.Run("with condition set to false", func() {
		fieldName := "field"
		fieldValue := ""
		validator := NewConditionalValidator(false, NewEmptyStringValidator(fieldName, fieldValue))
		err := validator.Validate()
		s.Assert().NoError(err)
	})
}
