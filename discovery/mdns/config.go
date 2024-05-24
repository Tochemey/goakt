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

package mdns

import "github.com/tochemey/goakt/v2/internal/validation"

// Config represents the mDNS provider discoConfig
type Config struct {
	// Service specifies the service name
	ServiceName string
	// Service specifies the service type
	Service string
	// Specifies the service domain
	Domain string
	// Port specifies the port the service is listening to
	Port int
	// IPv6 states whether to fetch ipv6 address instead of ipv4
	IPv6 *bool
}

// Validate checks whether the given discovery configuration is valid
func (x Config) Validate() error {
	return validation.New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("ServiceName", x.ServiceName)).
		AddValidator(validation.NewEmptyStringValidator("Service", x.Service)).
		AddValidator(validation.NewEmptyStringValidator("Domain", x.Domain)).
		Validate()
}
