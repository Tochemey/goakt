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
	"go.uber.org/multierr"
)

// Validator interface generalizes the validator implementations
type Validator interface {
	Validate() error
}

// Chain represents list of validators and is used to accumulate errors and return them as a single "error"
type Chain struct {
	failFast   bool
	validators []Validator
	violations error
}

// ChainOption configures a validation chain at creation time.
type ChainOption func(*Chain)

// New creates a new validation chain.
func New(opts ...ChainOption) *Chain {
	chain := &Chain{
		validators: make([]Validator, 0),
	}

	for _, opt := range opts {
		opt(chain)
	}

	return chain
}

// FailFast sets whether a chain should stop validation on first error.
func FailFast() ChainOption {
	return func(c *Chain) { c.failFast = true }
}

// AllErrors sets whether a chain should return all errors.
func AllErrors() ChainOption {
	return func(c *Chain) { c.failFast = false }
}

// AddValidator adds validator to the validation chain.
func (c *Chain) AddValidator(v Validator) *Chain {
	c.validators = append(c.validators, v)
	return c
}

// AddAssertion adds assertion to the validation chain.
func (c *Chain) AddAssertion(isTrue bool, message string) *Chain {
	c.validators = append(c.validators, NewBooleanValidator(isTrue, message))
	return c
}

// Validate runs validation chain and returns resulting error(s).
// It returns all validation error by default, use FailFast option to stop validation on first error.
func (c *Chain) Validate() error {
	for _, v := range c.validators {
		if violations := v.Validate(); violations != nil {
			if c.failFast {
				// just return the error
				return violations
			}
			// append error to the violations
			c.violations = multierr.Append(c.violations, violations)
		}
	}
	return c.violations
}
