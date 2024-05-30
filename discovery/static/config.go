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

package static

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/pkg/errors"

	"github.com/tochemey/goakt/v2/internal/validation"
)

var errFmt = "invalid address=(%s)"

// Config represents the static discovery provider configuration
type Config struct {
	// Hosts defines the list of hosts, ip:port
	Hosts []string
}

// Validate checks whether the given discovery configuration is valid
func (x Config) Validate() error {
	chain := validation.
		New(validation.FailFast()).
		AddAssertion(len(x.Hosts) != 0, "hosts are required")

	for _, host := range x.Hosts {
		chain = chain.AddValidator(newAddressValidator(host))
	}

	return chain.Validate()
}

type addressValidator struct {
	address string
}

// making sure the given struct implements the given interface
var _ validation.Validator = (*addressValidator)(nil)

func newAddressValidator(address string) *addressValidator {
	return &addressValidator{address: address}
}

// Validate implements validation.Validator.
func (a *addressValidator) Validate() error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(a.address))
	if err != nil {
		return errors.Wrapf(err, errFmt, a.address)
	}

	// let us validate the port number
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return errors.Wrapf(err, errFmt, a.address)
	}

	// TODO: maybe we only need to check port number not to be negative
	if host == "" || portNum > 65535 || portNum < 1 {
		return fmt.Errorf(errFmt, a.address)
	}

	return nil
}
