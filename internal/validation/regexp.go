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

package validation

import (
	"errors"
	"regexp"
)

var errInvalidExpression = errors.New("invalid expression")

type regexpValidator struct {
	re         *regexp.Regexp
	expression string
	customErr  error
}

var _ Validator = (*regexpValidator)(nil)

// NewRegexpValidator creates a validator that accepts expression when it
// matches re, a pattern the caller compiled once, and otherwise returns
// customErr, or errInvalidExpression when customErr is nil.
func NewRegexpValidator(re *regexp.Regexp, expression string, customErr error) Validator {
	return &regexpValidator{
		re:         re,
		expression: expression,
		customErr:  customErr,
	}
}

// Validate executes the validation
func (x *regexpValidator) Validate() error {
	if x.re.MatchString(x.expression) {
		return nil
	}

	if x.customErr != nil {
		return x.customErr
	}

	return errInvalidExpression
}
