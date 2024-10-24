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

package client

import (
	"github.com/tochemey/goakt/v2/internal/errorschain"
	"github.com/tochemey/goakt/v2/internal/validation"
)

// addr defines an TCP address
// in the form of host:port
type addr string

// Validate validates the given address
func (a addr) Validate() error {
	return validation.
		New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("address", string(a))).
		AddValidator(validation.NewTCPAddressValidator(string(a))).
		Validate()
}

type addrs []addr

// Validate the slice of addresses
func (a addrs) Validate() error {
	chain := errorschain.New(errorschain.ReturnFirst())
	for _, addr := range a {
		chain.AddError(addr.Validate())
	}
	return chain.Error()
}

func validateAddress(addresses []string) error {
	wrapped := make(addrs, len(addresses))
	for index, address := range addresses {
		wrapped[index] = addr(address)
	}
	return wrapped.Validate()
}
