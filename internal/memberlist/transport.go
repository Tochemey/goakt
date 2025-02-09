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

package memberlist

import (
	"bytes"
	"crypto/md5" // nolint
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/go-sockaddr"
	"github.com/hashicorp/memberlist"

	"github.com/tochemey/goakt/v3/log"
)

// TCPTransport is a memberlist.Transport implementation that uses TCP for both packet and stream
// operations ("packet" and "stream" are terms used by memberlist).
// It uses a new TCP connections for each operation. There is no connection reuse.
type TCPTransport struct {
	config       TCPTransportConfig
	logger       log.Logger
	packetCh     chan *memberlist.Packet
	connCh       chan net.Conn
	wg           sync.WaitGroup
	tcpListeners []net.Listener
	tlsConfig    *tls.Config

	shutdown atomic.Int32

	advertiseMu   sync.RWMutex
	advertiseAddr string
}

var _ memberlist.NodeAwareTransport = (*TCPTransport)(nil)

// NewTCPTransport returns new tcp-based transport with the given configuration. On
// success all the network listeners will be created and listening.
func NewTCPTransport(config TCPTransportConfig) (*TCPTransport, error) {
	if len(config.BindAddrs) == 0 {
		config.BindAddrs = []string{zeroZeroZeroZero}
	}

	// Build out the new transport.
	var ok bool
	t := TCPTransport{
		config:   config,
		logger:   log.New(log.InfoLevel, os.Stdout),
		packetCh: make(chan *memberlist.Packet),
		connCh:   make(chan net.Conn),
	}

	var err error
	if config.TLSEnabled {
		t.tlsConfig = config.TLS
	}

	// Clean up listeners if there's an error.
	defer func() {
		if !ok {
			_ = t.Shutdown()
		}
	}()

	// Build all the TCP and UDP listeners.
	port := config.BindPort
	for _, addr := range config.BindAddrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			return nil, fmt.Errorf("invalid address: %s", addr)
		}

		tcpAddr := &net.TCPAddr{IP: ip, Port: port}

		var tcpLn net.Listener
		switch {
		case config.TLSEnabled:
			tcpLn, err = tls.Listen("tcp", tcpAddr.String(), t.tlsConfig)
			if err != nil {
				return nil, fmt.Errorf("failed to start TLS TCP listener on %q port %d: %w", addr, port, err)
			}
		default:
			tcpLn, err = net.ListenTCP("tcp", tcpAddr)
			if err != nil {
				return nil, fmt.Errorf("failed to start TCP listener on %q port %d: %w", addr, port, err)
			}
		}

		t.tcpListeners = append(t.tcpListeners, tcpLn)

		// If the config port given was zero, use the first TCP listener
		// to pick an available port and then apply that to everything
		// else.
		if port == 0 {
			port = tcpLn.Addr().(*net.TCPAddr).Port
		}
	}

	// Fire them up now that we've been able to create them all.
	for i := 0; i < len(config.BindAddrs); i++ {
		t.wg.Add(1)
		go t.tcpListen(t.tcpListeners[i])
	}

	ok = true
	return &t, nil
}

// GetAutoBindPort returns the bind port that was automatically given by the
// kernel, if a bind port of 0 was given.
func (t *TCPTransport) GetAutoBindPort() int {
	// We made sure there's at least one TCP listener, and that one's
	// port was applied to all the others for the dynamic bind case.
	return t.tcpListeners[0].Addr().(*net.TCPAddr).Port
}

// FinalAdvertiseAddr is given the user's configured values (which
// might be empty) and returns the desired IP and port to advertise to
// the rest of the cluster.
// (Copied from memberlist' net_transport.go)
func (t *TCPTransport) FinalAdvertiseAddr(ip string, port int) (net.IP, int, error) {
	var advertiseAddr net.IP
	var advertisePort int
	switch {
	case ip != "":
		advertiseAddr = net.ParseIP(ip)
		if advertiseAddr == nil {
			return nil, 0, fmt.Errorf("failed to parse advertise address %q", ip)
		}
		if ip4 := advertiseAddr.To4(); ip4 != nil {
			advertiseAddr = ip4
		}
		advertisePort = port
	default:
		switch {
		case t.config.BindAddrs[0] == zeroZeroZeroZero:
			var err error
			ip, err = sockaddr.GetPrivateIP()
			if err != nil {
				return nil, 0, fmt.Errorf("failed to get interface addresses: %v", err)
			}
			if ip == "" {
				return nil, 0, fmt.Errorf("no private IP address found, and explicit IP not provided")
			}

			advertiseAddr = net.ParseIP(ip)
			if advertiseAddr == nil {
				return nil, 0, fmt.Errorf("failed to parse advertise address: %q", ip)
			}

		default:
			advertiseAddr = t.tcpListeners[0].Addr().(*net.TCPAddr).IP
		}
		advertisePort = t.GetAutoBindPort()
	}

	t.logger.Debugf("advertise address=(%s:%d)", advertiseAddr.String(), advertisePort)

	t.setAdvertisedAddr(advertiseAddr, advertisePort)
	return advertiseAddr, advertisePort, nil
}

// WriteTo is a packet-oriented interface that fires off the given
// payload to the given address.
func (t *TCPTransport) WriteTo(b []byte, addr string) (time.Time, error) {
	err := t.writeTo(b, addr)
	if err != nil {
		logger := t.logger
		if strings.Contains(err.Error(), "connection refused") {
			// The connection refused is a common error that could happen during normal operations when a node
			// shutdown (or crash). It shouldn't be considered a warning condition on the sender side.
			logger = t.debugLogger()
		}
		logger.Infof("failed to WriteTo %s: %v", addr, err)

		// WriteTo is used to send "UDP" packets. Since we use TCP, we can detect more errors,
		// but memberlist library doesn't seem to cope with that very well. That is why we return nil instead.
		return time.Now(), nil
	}

	return time.Now(), nil
}

func (t *TCPTransport) writeTo(b []byte, addr string) error {
	// Open connection, write packet header and data, data hash, close. Simple.
	c, err := t.getConnection(addr, t.config.PacketDialTimeout)
	if err != nil {
		return err
	}

	closed := false
	defer func() {
		if !closed {
			// If we still need to close, then there was another error. Ignore this one.
			_ = c.Close()
		}
	}()

	// Compute the digest *before* setting the deadline on the connection (so that the time
	// it takes to compute the digest is not taken in account).
	// We use md5 as quick and relatively short hash, not in cryptographic context.
	// It's also used to detect if the whole packet has been received on the receiver side.
	digest := md5.Sum(b) //nolint

	// Prepare the header *before* setting the deadline on the connection.
	headerBuf := bytes.Buffer{}
	headerBuf.WriteByte(byte(packet))

	// We need to send our address to the other side, otherwise other side can only see IP and port from TCP header.
	// But that doesn't match our node address (new TCP connection has new random port), which confuses memberlist.
	// So we send our advertised address, so that memberlist on the receiving side can match it with correct node.
	// This seems to be important for node probes (pings) done by memberlist.
	ourAddr := t.getAdvertisedAddr()
	if len(ourAddr) > 255 {
		return fmt.Errorf("local address too long")
	}

	headerBuf.WriteByte(byte(len(ourAddr)))
	headerBuf.WriteString(ourAddr)

	if t.config.PacketWriteTimeout > 0 {
		deadline := time.Now().Add(t.config.PacketWriteTimeout)
		err := c.SetDeadline(deadline)
		if err != nil {
			return fmt.Errorf("setting deadline: %v", err)
		}
	}

	_, err = c.Write(headerBuf.Bytes())
	if err != nil {
		return fmt.Errorf("sending local address: %v", err)
	}

	n, err := c.Write(b)
	if err != nil {
		return fmt.Errorf("sending data: %v", err)
	}
	if n != len(b) {
		return fmt.Errorf("sending data: short write")
	}

	// Append digest.
	n, err = c.Write(digest[:])
	if err != nil {
		return fmt.Errorf("digest: %v", err)
	}
	if n != len(digest) {
		return fmt.Errorf("digest: short write")
	}

	closed = true
	err = c.Close()
	if err != nil {
		return fmt.Errorf("close: %v", err)
	}

	t.debugLogger().Infof("sent packet to %s, size=(%d), hash=(%s)", addr, len(b), fmt.Sprintf("%x", digest))
	return nil
}

func (t *TCPTransport) WriteToAddress(b []byte, addr memberlist.Address) (time.Time, error) {
	return t.WriteTo(b, addr.Addr)
}

func (t *TCPTransport) DialAddressTimeout(addr memberlist.Address, timeout time.Duration) (net.Conn, error) {
	return t.DialTimeout(addr.Addr, timeout)
}

// PacketCh returns a channel that can be read to receive incoming
// packets from other peers.
func (t *TCPTransport) PacketCh() <-chan *memberlist.Packet {
	return t.packetCh
}

// DialTimeout is used to create a connection that allows memberlist to perform
// two-way communication with a peer.
func (t *TCPTransport) DialTimeout(addr string, timeout time.Duration) (net.Conn, error) {
	c, err := t.getConnection(addr, timeout)

	if err != nil {
		return nil, err
	}

	_, err = c.Write([]byte{byte(stream)})
	if err != nil {
		_ = c.Close()
		return nil, err
	}

	return c, nil
}

// StreamCh returns a channel that can be read to handle incoming stream
// connections from other peers.
func (t *TCPTransport) StreamCh() <-chan net.Conn {
	return t.connCh
}

// Shutdown is called when memberlist is shutting down; this gives the
// transport a chance to clean up any listeners.
func (t *TCPTransport) Shutdown() error {
	// This will avoid log spam about errors when we shut down.
	t.shutdown.Store(1)

	// Rip through all the connections and shut them down.
	for _, conn := range t.tcpListeners {
		_ = conn.Close()
	}

	// Block until all the listener threads have died.
	t.wg.Wait()
	return nil
}

// tcpListen is a long-running goroutine that accepts incoming TCP connections
// and spawns new go routine to handle each connection. This transport uses TCP connections
// for both packet sending and streams.
// (copied from Memberlist net_transport.go)
func (t *TCPTransport) tcpListen(tcpLn net.Listener) {
	defer t.wg.Done()

	// baseDelay is the initial delay after an AcceptTCP() error before attempting again
	const baseDelay = 5 * time.Millisecond

	// maxDelay is the maximum delay after an AcceptTCP() error before attempting again.
	// In the case that tcpListen() is error-looping, it will delay the shutdown check.
	// Therefore, changes to maxDelay may have an effect on the latency of shutdown.
	const maxDelay = 1 * time.Second

	var loopDelay time.Duration
	for {
		conn, err := tcpLn.Accept()
		if err != nil {
			if s := t.shutdown.Load(); s == 1 {
				break
			}

			switch {
			case loopDelay == 0:
				loopDelay = baseDelay
			default:
				loopDelay *= 2
			}

			if loopDelay > maxDelay {
				loopDelay = maxDelay
			}

			t.logger.Errorf("failed to accept tcp connection: %v", err)
			time.Sleep(loopDelay)
			continue
		}
		// No error, reset loop delay
		loopDelay = 0

		go t.handleConnection(conn)
	}
}

func (t *TCPTransport) debugLogger() log.Logger {
	if t.config.DebugEnabled {
		return log.DefaultLogger
	}
	return log.DiscardLogger
}

func (t *TCPTransport) handleConnection(conn net.Conn) {
	t.debugLogger().Debugf("New connection: %s", conn.RemoteAddr())

	closeConn := true
	defer func() {
		if closeConn {
			_ = conn.Close()
		}
	}()

	// let's read first byte, and determine what to do about this connection
	msgType := []byte{0}
	_, err := io.ReadFull(conn, msgType)
	if err != nil {
		t.logger.Errorf("failed to read remote=(%s) message type: %v", conn.RemoteAddr(), err)
		return
	}

	switch {
	case messageType(msgType[0]) == stream:
		closeConn = false
		t.connCh <- conn
	case messageType(msgType[0]) == packet:
		addrLengthBuf := []byte{0}
		_, err := io.ReadFull(conn, addrLengthBuf)

		if err != nil {
			t.logger.Errorf("error while reading node address length from packet remote=(%s) message type: %v", conn.RemoteAddr(), err)
			return
		}

		addrBuf := make([]byte, addrLengthBuf[0])
		_, err = io.ReadFull(conn, addrBuf)

		if err != nil {
			t.logger.Errorf("error while reading node address length from packet remote=(%s) message type: %v", conn.RemoteAddr(), err)
			return
		}

		buf, err := io.ReadAll(conn)
		if err != nil {
			t.logger.Errorf("error while reading node address length from packet remote=(%s) message type: %v", conn.RemoteAddr(), err)
			return
		}

		if len(buf) < md5.Size {
			t.logger.Warnf("not enough data received data_length=(%d) from packet remote=(%s) message type: %v", len(buf), conn.RemoteAddr(), err)
			return
		}

		receivedDigest := buf[len(buf)-md5.Size:]
		buf = buf[:len(buf)-md5.Size]
		expectedDigest := md5.Sum(buf) // nolint

		if !bytes.Equal(receivedDigest, expectedDigest[:]) {
			t.logger.Warnf("packet digest mismatch. expected=(%s), received=(%s), data_length=(%d), remote=(%s)", fmt.Sprintf("%x", expectedDigest), fmt.Sprintf("%x", receivedDigest), len(buf), conn.RemoteAddr())
		}

		t.debugLogger().Infof("Received packet addr=(%s), size=(%d), hash=(%s)", addr(addrBuf), len(buf), fmt.Sprintf("%x", receivedDigest))
		t.packetCh <- &memberlist.Packet{
			Buf:       buf,
			From:      addr(addrBuf),
			Timestamp: time.Now(),
		}
	default:
		t.logger.Errorf("unknown message type=(%s) remote=(%s)", string(msgType), conn.RemoteAddr())
	}
}

func (t *TCPTransport) setAdvertisedAddr(advertiseAddr net.IP, advertisePort int) {
	t.advertiseMu.Lock()
	defer t.advertiseMu.Unlock()
	addr := net.TCPAddr{IP: advertiseAddr, Port: advertisePort}
	t.advertiseAddr = addr.String()
}

func (t *TCPTransport) getAdvertisedAddr() string {
	t.advertiseMu.RLock()
	defer t.advertiseMu.RUnlock()
	return t.advertiseAddr
}

func (t *TCPTransport) getConnection(addr string, timeout time.Duration) (net.Conn, error) {
	if t.config.TLSEnabled {
		return tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", addr, t.tlsConfig)
	}
	return net.DialTimeout("tcp", addr, timeout)
}
