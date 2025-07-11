/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package errorschain

import (
	"context"

	"go.uber.org/multierr"
)

// Chain defines an error chain
type Chain struct {
	returnFirst bool
	errs        []error
}

// ChainOption configures a validation chain at creation time.
type ChainOption func(*Chain)

// New creates a new error chain. All errors will be evaluated respectively
// according to their insertion order
func New(opts ...ChainOption) *Chain {
	chain := &Chain{
		errs: make([]error, 0),
	}

	for _, opt := range opts {
		opt(chain)
	}

	return chain
}

// AddError add an error to the chain
func (c *Chain) AddError(err error) *Chain {
	if c.returnFirst {
		if len(c.errs) == 0 {
			if err != nil {
				c.errs = append(c.errs, err)
				return c
			}
		}
		return c
	}

	if err != nil {
		c.errs = append(c.errs, err)
		return c
	}

	return c
}

// AddErrorFn add an error to the chain
func (c *Chain) AddErrorFn(fn func() error) *Chain {
	if c.returnFirst {
		if len(c.errs) == 0 {
			if err := fn(); err != nil {
				c.errs = append(c.errs, err)
				return c
			}
		}
		return c
	}

	if err := fn(); err != nil {
		c.errs = append(c.errs, err)
		return c
	}

	return c
}

// AddErrorFnIf adds an error function to the chain if the condition is true
func (c *Chain) AddErrorFnIf(ctx context.Context, condition bool, fn func(ctx context.Context) error) *Chain {
	if condition {
		if c.returnFirst {
			if len(c.errs) == 0 {
				if err := fn(ctx); err != nil {
					c.errs = append(c.errs, err)
					return c
				}
			}
			return c
		}

		if err := fn(ctx); err != nil {
			c.errs = append(c.errs, err)
			return c
		}
	}

	return c
}

// AddErrorFns add a slice of error functions to the chain. Remember the slice order does matter here
func (c *Chain) AddErrorFns(fn ...func() error) *Chain {
	for _, f := range fn {
		c = c.AddErrorFn(f)
	}
	return c
}

// Error returns the error
func (c *Chain) Error() error {
	if c.returnFirst {
		if len(c.errs) == 0 {
			return nil
		}
		return c.errs[0]
	}

	var err error
	for _, v := range c.errs {
		if v != nil {
			// append error to the violations
			err = multierr.Append(err, v)
		}
	}
	return err
}

// ReturnFirst sets whether a chain should stop validation on first error.
func ReturnFirst() ChainOption {
	return func(c *Chain) { c.returnFirst = true }
}

// ReturnAll sets whether a chain should return all errors.
func ReturnAll() ChainOption {
	return func(c *Chain) { c.returnFirst = false }
}
