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

package selfmanaged

import (
	"bytes"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	protocolVersion = "goakt-v1"
	protocolSep     = "|"
	maxPacketSize   = 512
)

var (
	protocolVersionBytes = []byte(protocolVersion)
	protocolSepBytes     = []byte(protocolSep)
	multicastGroup       = net.IPv4(224, 0, 0, 1) // all-hosts, used for loopback on darwin
)

// broadcast handles UDP broadcast send and receive for peer discovery.
// Uses sync.Map for the peer cache to avoid lock contention and minimize
// allocations on the hot path (DiscoverPeers).
//
// Uses two UDP connections: one for receiving (ListenUDP) and one for sending
// (DialUDP to broadcast address). This avoids platform-specific socket options
// for SO_BROADCAST while ensuring broadcast packets reach all nodes on the LAN.
type broadcast struct {
	config           *Config
	recv             *net.UDPConn
	send             *net.UDPConn
	clusterNameBytes []byte // cached for handlePacket to avoid repeated []byte conversion

	peers   sync.Map // map[string]*int64 — address -> lastSeen Unix nano (ptr avoids boxing on updates)
	stopped atomic.Bool
	done    chan struct{}
	wg      sync.WaitGroup
}

// newBroadcast creates a broadcast instance. Call start to begin send/receive.
func newBroadcast(config *Config) *broadcast {
	return &broadcast{
		config:           config,
		clusterNameBytes: []byte(config.ClusterName),
		done:             make(chan struct{}),
	}
}

// start binds to the broadcast port and begins sending and receiving.
func (b *broadcast) start() error {
	port := b.config.broadcastPort()
	var recvConn *net.UDPConn
	var sendConn *net.UDPConn

	if b.config.isLoopbackBroadcast() {
		if mc, err := listenUDPMulticastLoopback(port); err == nil {
			recvConn = mc
			sendAddr := &net.UDPAddr{IP: multicastGroup, Port: port}
			sendConn, err = net.DialUDP("udp4", nil, sendAddr)
			if err != nil {
				_ = recvConn.Close()
				return err
			}
		}
	}

	if recvConn == nil {
		recvAddr := b.config.broadcastBindAddr(port)
		var err error
		recvConn, err = listenUDPReusePortToAddr(recvAddr)
		if err != nil {
			return err
		}
		sendAddr := &net.UDPAddr{IP: b.config.broadcastIP(), Port: port}
		sendConn, err = net.DialUDP("udp4", nil, sendAddr)
		if err != nil {
			_ = recvConn.Close()
			return err
		}
		if b.config.isLoopbackBroadcast() {
			if raw, err := sendConn.SyscallConn(); err == nil {
				_ = raw.Control(func(fd uintptr) {
					_ = setSOBroadcast(fd)
				})
			}
		}
	}

	b.recv = recvConn
	b.send = sendConn

	b.wg.Add(2)
	go b.sendLoop()
	go b.recvLoop()
	return nil
}

// stop closes the connections and waits for send/recv goroutines to exit.
func (b *broadcast) stop() {
	if !b.stopped.CompareAndSwap(false, true) {
		return
	}
	close(b.done)
	if b.recv != nil {
		_ = b.recv.Close()
	}
	if b.send != nil {
		_ = b.send.Close()
	}
	b.wg.Wait()
}

// getPeers returns addresses of peers seen within the expiry window, excluding self.
// Pre-allocates the result slice to avoid grow during append.
func (b *broadcast) getPeers(selfAddr string) []string {
	expiry := time.Now().Add(-b.config.peerExpiry()).UnixNano()
	out := make([]string, 0, 16)
	b.peers.Range(func(key, value any) bool {
		addr := key.(string)
		lastSeen := atomic.LoadInt64(value.(*int64))
		if addr != selfAddr && lastSeen > expiry {
			out = append(out, addr)
		}
		return true
	})
	return out
}

// encodePacket builds the announcement packet. Format: goakt-v1|clusterName|selfAddress
func (b *broadcast) encodePacket() []byte {
	var buf bytes.Buffer
	buf.Grow(maxPacketSize)
	buf.WriteString(protocolVersion)
	buf.WriteString(protocolSep)
	buf.WriteString(b.config.ClusterName)
	buf.WriteString(protocolSep)
	buf.WriteString(b.config.SelfAddress)
	return buf.Bytes()
}

func (b *broadcast) sendLoop() {
	defer b.wg.Done()
	ticker := time.NewTicker(b.config.broadcastInterval())
	defer ticker.Stop()
	packet := b.encodePacket()

	for {
		select {
		case <-b.done:
			return
		case <-ticker.C:
			if b.send != nil {
				_, _ = b.send.Write(packet)
			}
		}
	}
}

func (b *broadcast) recvLoop() {
	defer b.wg.Done()
	buf := make([]byte, maxPacketSize)
	for {
		select {
		case <-b.done:
			return
		default:
		}
		if b.recv != nil {
			_ = b.recv.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, _, err := b.recv.ReadFromUDP(buf)
			if err != nil {
				if !b.stopped.Load() && !isTimeout(err) {
					continue
				}
				return
			}
			if n > 0 {
				b.handlePacket(buf[:n])
			}
		}
	}
}

func (b *broadcast) handlePacket(data []byte) {
	parts := bytes.SplitN(data, protocolSepBytes, 3)
	if len(parts) != 3 || !bytes.Equal(parts[0], protocolVersionBytes) || !bytes.Equal(parts[1], b.clusterNameBytes) {
		return
	}
	addrBytes := bytes.TrimSpace(parts[2])
	if len(addrBytes) == 0 {
		return
	}
	idx := bytes.IndexByte(addrBytes, ':')
	if idx <= 0 || idx >= len(addrBytes)-1 {
		return
	}
	portBytes := addrBytes[idx+1:]
	for _, c := range portBytes {
		if c < '0' || c > '9' {
			return
		}
	}
	addrStr := string(addrBytes)
	ts := time.Now().UnixNano()
	if v, ok := b.peers.Load(addrStr); ok {
		atomic.StoreInt64(v.(*int64), ts)
		return
	}
	p := new(int64)
	atomic.StoreInt64(p, ts)
	b.peers.Store(addrStr, p)
}

func isTimeout(err error) bool {
	ne, ok := err.(net.Error)
	return ok && ne.Timeout()
}
