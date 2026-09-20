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

// Package main reproduces github.com/Tochemey/goakt/issues/1374: a wildcard
// bind address (0.0.0.0 or ::) used to make the remoting server listen on one
// guessed private IP instead of every interface, so connections through
// loopback were refused, a host with no private or public address could not
// start, and :: was advertised verbatim. With the fix, this sample prints OK
// for the IPv4 wildcard and, when the host supports IPv6, for the IPv6 one.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	goaktlog "github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
)

// echo is the actor looked up over remoting; it only needs to exist.
type echo struct{}

func (e *echo) PreStart(*actor.Context) error { return nil }
func (e *echo) PostStop(*actor.Context) error { return nil }
func (e *echo) Receive(ctx *actor.ReceiveContext) {
	ctx.Unhandled()
}

func main() {
	ctx := context.Background()

	host := check(ctx, "0.0.0.0", "127.0.0.1")
	fmt.Printf("OK: 0.0.0.0 listens on every interface and advertises %s\n", host)

	// the IPv6 wildcard must also serve IPv4 peers, because the address advertised for it is usually IPv4
	probe, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		fmt.Println("SKIP: IPv6 is not available on this host")
		return
	}

	_ = probe.Close()
	host = check(ctx, "::", "127.0.0.1", "::1")
	fmt.Printf("OK: :: listens for IPv4 and IPv6 peers and advertises %s\n", host)
}

// check starts an actor system bound to bindAddr and verifies the fix: the
// system advertises a concrete address, the remoting server accepts connections
// through every peer address given as well as through the advertised one, and a
// remote lookup addressed to the advertised host succeeds. It returns the
// advertised host and exits with a REPRO message on the first failure.
func check(ctx context.Context, bindAddr string, peers ...string) string {
	port := inet.Get(1)[0]

	// before the fix, a host with no private or public address fails here with
	// "no private IP address found"
	system, err := actor.NewActorSystem("issue1374",
		actor.WithLogger(goaktlog.DiscardLogger),
		actor.WithRemote(remote.NewConfig(bindAddr, port)),
	)
	if err != nil {
		log.Fatalf("REPRO (broken): a system bound to %s cannot be created: %v", bindAddr, err)
	}

	if err := system.Start(ctx); err != nil {
		log.Fatalf("REPRO (broken): a system bound to %s cannot start: %v", bindAddr, err)
	}

	defer func() { _ = system.Stop(ctx) }()

	// before the fix, :: was advertised as is
	host := system.Host()
	if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
		log.Fatalf("REPRO (broken): a system bound to %s advertises %q instead of a concrete address", bindAddr, host)
	}

	if _, err := system.Spawn(ctx, "echo", &echo{}); err != nil {
		log.Fatal(err)
	}

	// before the fix, only the advertised address accepted connections
	for _, peer := range append(peers, host) {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(peer, strconv.Itoa(port)), time.Second)
		if err != nil {
			log.Fatalf("REPRO (broken): a system bound to %s refuses connections through %s: %v", bindAddr, peer, err)
		}

		_ = conn.Close()
	}

	// what peers are told is unchanged: remote operations use the advertised host
	client := remoteclient.NewClient()
	defer client.Close()

	addr, err := client.RemoteLookup(ctx, host, port, "echo")
	if err != nil {
		log.Fatalf("REPRO (broken): remote lookup through the advertised host %s failed: %v", host, err)
	}

	if addr.Host() != host {
		log.Fatalf("REPRO (broken): the actor is addressed by %s instead of the advertised host %s", addr.Host(), host)
	}

	return host
}
