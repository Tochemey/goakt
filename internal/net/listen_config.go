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

import "net"

// Listener config struct
type ListenConfig struct {
	underlying net.ListenConfig
	// Enable/disable SO_REUSEPORT (requires Linux >=2.4)
	SocketReusePort bool
	// Enable/disable TCP_FASTOPEN (requires Linux >=3.7 or Windows 10, version 1607)
	// For Linux:
	// - see https://lwn.net/Articles/508865/
	// - enable with "echo 3 >/proc/sys/net/ipv4/tcp_fastopen" for client and server
	// For Windows:
	// - enable with "netsh int tcp set global fastopen=enabled"
	SocketFastOpen bool
	// Queue length for TCP_FASTOPEN (default 256)
	SocketFastOpenQueueLen int
	// Enable/disable TCP_DEFER_ACCEPT (requires Linux >=2.4)
	SocketDeferAccept bool
}
