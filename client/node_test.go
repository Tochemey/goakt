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

package client

import (
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"testing"

	"github.com/kapetan-io/tackle/autotls"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"
)

func TestNode(t *testing.T) {
	ports := dynaport.Get(1)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[0]))
	// AutoGenerate TLS certs
	conf := autotls.Config{
		AutoTLS:            true,
		ClientAuth:         tls.RequireAndVerifyClientCert,
		InsecureSkipVerify: false,
	}
	require.NoError(t, autotls.Setup(&conf))

	node := NewNode(address, WithWeight(10), WithTLS(conf.ClientTLS))
	require.NotNil(t, node)
	require.Equal(t, address, node.Address())
	require.Exactly(t, float64(10), node.Weight())
	require.NoError(t, node.Validate())
	require.NotNil(t, node.HTTPClient())
	require.Equal(t, fmt.Sprintf("https://%s", address), node.HTTPEndPoint())
	require.NotNil(t, node.Remoting())
	host, port := node.HostAndPort()
	require.Equal(t, "127.0.0.1", host)
	require.Equal(t, ports[0], port)
	node.Free()
}
