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

package net

import (
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetReturnsBindablePorts(t *testing.T) {
	ports := Get(3)
	require.Len(t, ports, 3)

	for _, p := range ports {
		assert.Greater(t, p, 0)
		assert.Less(t, p, 65536)

		tcp, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: p})
		require.NoError(t, err, "port %d should be bindable on TCP", p)
		require.NoError(t, tcp.Close())

		udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: p})
		require.NoError(t, err, "port %d should be bindable on UDP", p)
		require.NoError(t, udp.Close())
	}
}

func TestGetReturnsDistinctPorts(t *testing.T) {
	ports := Get(16)
	seen := make(map[int]struct{}, len(ports))
	for _, p := range ports {
		_, dup := seen[p]
		assert.False(t, dup, "port %d returned twice in one Get call", p)
		seen[p] = struct{}{}
	}
}

func TestGetZeroAndNegative(t *testing.T) {
	require.Empty(t, Get(0))

	ports, err := GetWithErr(-1)
	require.NoError(t, err)
	require.Empty(t, ports)
}

func TestGetS(t *testing.T) {
	ports := GetS(2)
	require.Len(t, ports, 2)
	for _, s := range ports {
		p, err := strconv.Atoi(s)
		require.NoError(t, err)
		assert.Greater(t, p, 0)
	}
}

func TestGetSWithErr(t *testing.T) {
	ports, err := GetSWithErr(2)
	require.NoError(t, err)
	require.Len(t, ports, 2)
}

func TestGetSWithErrZero(t *testing.T) {
	ports, err := GetSWithErr(0)
	require.NoError(t, err)
	require.Empty(t, ports)
}

func TestConcurrentGetProducesDistinctPorts(t *testing.T) {
	const goroutines = 8
	const perCall = 4

	var (
		wg  sync.WaitGroup
		mu  sync.Mutex
		all = make(map[int]int, goroutines*perCall)
	)

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ports := Get(perCall)
			mu.Lock()
			for _, p := range ports {
				all[p]++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	for p, count := range all {
		assert.Equal(t, 1, count, "port %d observed %d times across concurrent Get calls", p, count)
	}
	assert.Len(t, all, goroutines*perCall)
}

func TestReserveBoth(t *testing.T) {
	port, tcp, udp, err := reserveBoth()
	require.NoError(t, err)
	defer tcp.Close()
	defer udp.Close()

	assert.Equal(t, port, tcp.Addr().(*net.TCPAddr).Port)
	assert.Equal(t, port, udp.LocalAddr().(*net.UDPAddr).Port)
}
