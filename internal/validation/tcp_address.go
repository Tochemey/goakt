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
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

var errFmt = "invalid address=(%s)"

// TCPAddressValidator helps validate a TCP address
type TCPAddressValidator struct {
	address string
}

// making sure the given struct implements the given interface
var _ Validator = (*TCPAddressValidator)(nil)

// NewTCPAddressValidator creates an instance of TCPAddressValidator
func NewTCPAddressValidator(address string) *TCPAddressValidator {
	return &TCPAddressValidator{address: address}
}

// Validate implements validation.Validator.
func (a *TCPAddressValidator) Validate() error {
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
