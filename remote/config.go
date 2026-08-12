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

package remote

import (
	"maps"
	"math"
	"net"
	"path"
	"reflect"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/size"
	"github.com/tochemey/goakt/v4/internal/validation"
	gtls "github.com/tochemey/goakt/v4/tls"
)

// DefaultMaxIdleConns is the default number of pooled idle connections
// retained per remote endpoint, as reported by [Config.MaxIdleConns] when no
// override is applied. Idle connections beyond the pool's idle timeout are
// closed, so this bounds the peak idle sockets kept per peer.
const DefaultMaxIdleConns = inet.DefaultMaxIdleConns

// DefaultMaxConcurrentLargeTransfers is the default HELLO / config cap on
// concurrent large-message transfers per duplex connection.
const DefaultMaxConcurrentLargeTransfers uint32 = 4

// DefaultCreditWindow is the default unreclaimed in-flight byte budget per
// duplex connection (16 MiB). See [WithCreditWindow].
const DefaultCreditWindow uint64 = 16 * size.MB

// DefaultOrdinaryLanes is the default number of ordinary duplex lanes per
// peer. One lane preserves per-destination-node FIFO for user traffic.
const DefaultOrdinaryLanes uint32 = 1

// DefaultChunkSize is the default logical-frame size above which duplex
// senders emit CHUNK frames (256 KiB).
const DefaultChunkSize uint32 = inet.DefaultChunkSize

// DefaultMaxMessageSize is the default cap on a reassembled logical duplex
// frame (16 MiB). It may be raised above [Config.MaxFrameSize].
const DefaultMaxMessageSize uint64 = inet.DefaultMaxMessageSize

// Config defines the remote config.
//
// BindAddr must be provided as a physical IP address rather than a DNS name so
// GoAkt can bind to a deterministic network interface without relying on
// external name resolution. When BindAddr is set to 0.0.0.0 the runtime will
// attempt to discover an appropriate private IP address to publish to other
// nodes, only falling back to a public IP when no private candidate exists.
// The design favors predictable intra-cluster connectivity in multi-homed or
// containerized deployments where DNS entries may be unavailable or resolve to
// unintended interfaces.
type Config struct {
	maxFrameSize      uint32
	writeTimeout      time.Duration
	readIdleTimeout   time.Duration
	idleTimeout       time.Duration
	bindAddr          string
	bindPort          int
	compression       Compression
	contextPropagator ContextPropagator
	// serializers holds all per-type and per-interface serializer entries
	// evaluated in registration order by [Config.Serializer].
	// Populated only during construction; never mutated afterwards.
	serializers                 map[reflect.Type]Serializer
	maxIdleConns                int
	dialTimeout                 time.Duration
	keepAlive                   time.Duration
	tlsInfo                     *gtls.Info
	protocolPin                 ProtocolPin
	ordinaryLanes               uint32
	largeMessageDestinations    []string
	maxConcurrentLargeTransfers uint32
	chunkSize                   uint32
	maxMessageSize              uint64
	creditWindow                uint64
}

var _ validation.Validator = (*Config)(nil)

// NewConfig returns a Config initialized with the supplied bind address, port,
// and any functional options. The bind address must be a concrete IP (not a
// hostname); if it is 0.0.0.0, GoAkt will resolve it to a suitable private IP
// and fall back to a public address only when necessary. Callers can further
// tailor transport behaviour through Option values such as frame size and
// timeout tuning.
func NewConfig(bindAddr string, bindPort int, opts ...Option) *Config {
	cfg := &Config{
		maxFrameSize:                16 * size.MB,
		writeTimeout:                10 * time.Second,
		readIdleTimeout:             10 * time.Second,
		idleTimeout:                 1200 * time.Second,
		bindAddr:                    bindAddr,
		bindPort:                    bindPort,
		compression:                 NoCompression,
		serializers:                 make(map[reflect.Type]Serializer, 10),
		maxIdleConns:                DefaultMaxIdleConns, // pooled connections retained per endpoint
		dialTimeout:                 5 * time.Second,     // 5s dial timeout
		keepAlive:                   15 * time.Second,    // 15s TCP keep-alive
		protocolPin:                 ProtocolPinAuto,
		ordinaryLanes:               DefaultOrdinaryLanes,
		maxConcurrentLargeTransfers: DefaultMaxConcurrentLargeTransfers,
		chunkSize:                   DefaultChunkSize,
		maxMessageSize:              DefaultMaxMessageSize,
		creditWindow:                DefaultCreditWindow,
	}

	// Register the default proto serializer for all proto.Message implementations.
	cfg.serializers[reflect.TypeFor[proto.Message]()] = NewProtoSerializer()

	// apply the options
	for _, opt := range opts {
		opt.Apply(cfg)
	}

	return cfg
}

// DefaultConfig returns the default remote config
func DefaultConfig() *Config {
	cfg := &Config{
		maxFrameSize:                16 * size.MB,
		writeTimeout:                10 * time.Second,
		readIdleTimeout:             10 * time.Second,
		idleTimeout:                 1200 * time.Second,
		bindAddr:                    "127.0.0.1",
		bindPort:                    0,
		compression:                 NoCompression,
		serializers:                 make(map[reflect.Type]Serializer, 10),
		maxIdleConns:                DefaultMaxIdleConns, // pooled connections retained per endpoint
		dialTimeout:                 5 * time.Second,     // 5s dial timeout
		keepAlive:                   15 * time.Second,    // 15s TCP keep-alive
		protocolPin:                 ProtocolPinAuto,
		ordinaryLanes:               DefaultOrdinaryLanes,
		maxConcurrentLargeTransfers: DefaultMaxConcurrentLargeTransfers,
		chunkSize:                   DefaultChunkSize,
		maxMessageSize:              DefaultMaxMessageSize,
		creditWindow:                DefaultCreditWindow,
	}

	// Register the default proto serializer for all proto.Message implementations.
	cfg.serializers[reflect.TypeFor[proto.Message]()] = NewProtoSerializer()

	return cfg
}

// IdleTimeout specifies how long until idle clients should be
// closed with a GOAWAY frame. PING frames are not considered
// activity for the purposes of IdleTimeout.
// If zero or negative, there is no timeout.
func (x *Config) IdleTimeout() time.Duration {
	return x.idleTimeout
}

// MaxFrameSize specifies the largest frame
// this server is willing to read. A valid value is between
// 16k and 16M, inclusive. If zero or otherwise invalid, an error will be thrown.
func (x *Config) MaxFrameSize() uint32 {
	return x.maxFrameSize
}

// WriteTimeout is the timeout after which a connection will be
// closed if no data can be written to it. The timeout begins when data is
// available to write, and is extended whenever any bytes are written.
// If zero or negative, there is no timeout.
func (x *Config) WriteTimeout() time.Duration {
	return x.writeTimeout
}

// ReadIdleTimeout is the timeout after which a health check using a ping
// frame will be carried out if no frame is received on the connection.
// If zero, no health check is performed.
func (x *Config) ReadIdleTimeout() time.Duration {
	return x.readIdleTimeout
}

// BindAddr returns the bind addr
func (x *Config) BindAddr() string {
	return x.bindAddr
}

// BindPort returns the bind port
func (x *Config) BindPort() int {
	return x.bindPort
}

// Compression returns the compression algorithm to use
func (x *Config) Compression() Compression {
	return x.compression
}

// ContextPropagator returns the context propagator
func (x *Config) ContextPropagator() ContextPropagator {
	return x.contextPropagator
}

// Serializer returns the [Serializer] registered for the given message, using
// the same dispatch order as [Remoting.Serializer]:
//
//  1. Exact concrete type — the entry registered with the message's dynamic type.
//  2. Interface match — the first registered interface the message implements.
//
// Returns nil when message is nil or no entry matches.
func (x *Config) Serializer(msg any) Serializer {
	if msg == nil {
		return nil
	}

	msgType := reflect.TypeOf(msg)
	if msgType == nil {
		return nil
	}

	for typ, serializer := range x.serializers {
		if typ.Kind() == reflect.Interface {
			if msgType.Implements(typ) {
				return serializer
			}
		} else if msgType == typ {
			return serializer
		}
	}

	return nil
}

// Serializers returns a copy of the registered serializer map keyed by
// reflect.Type. The map contains all entries added via [WithSerializers],
// including the default [proto.Message] entry registered by [NewConfig] and
// [DefaultConfig].
//
// The returned map is a defensive copy; callers may iterate or read it freely
// without affecting the Config.
func (x *Config) Serializers() map[reflect.Type]Serializer {
	result := make(map[reflect.Type]Serializer, len(x.serializers))
	maps.Copy(result, x.serializers)
	return result
}

// Sanitize the configuration
func (x *Config) Sanitize() error {
	var err error
	// combine host and port into an hostPort string
	hostPort := net.JoinHostPort(x.bindAddr, strconv.Itoa(int(x.bindPort)))
	x.bindAddr, err = inet.GetBindIP(hostPort)
	if err != nil {
		return err
	}
	return nil
}

// MaxIdleConns returns the max idle connections
func (x *Config) MaxIdleConns() int {
	return x.maxIdleConns
}

// DialTimeout returns the dial timeout
func (x *Config) DialTimeout() time.Duration {
	return x.dialTimeout
}

// KeepAlive returns the keep alive
func (x *Config) KeepAlive() time.Duration {
	return x.keepAlive
}

// TLS returns the TLS settings set with WithTLS. A nil return value means
// the remoting transport runs in plaintext.
func (x *Config) TLS() *gtls.Info {
	return x.tlsInfo
}

// ProtocolPin returns the remoting wire-protocol pin set with
// [WithProtocolPin]. The default is [ProtocolPinAuto]. See [WithProtocolPin]
// for dial/accept behavior and when to pin away from auto.
func (x *Config) ProtocolPin() ProtocolPin {
	return x.protocolPin
}

// OrdinaryLanes returns the number of ordinary duplex lanes dialed per peer.
// The default is [DefaultOrdinaryLanes]. Raising the count narrows the
// per-destination-node FIFO domain for user traffic.
func (x *Config) OrdinaryLanes() uint32 {
	return x.ordinaryLanes
}

// LargeMessageDestinations returns the hierarchical actor-path glob patterns
// that route user tell/ask traffic onto the large lane. Patterns match the
// path suffix after host:port (for example orders/*), not the full goakt://
// URI. An empty list matches nothing.
func (x *Config) LargeMessageDestinations() []string {
	if len(x.largeMessageDestinations) == 0 {
		return nil
	}
	out := make([]string, len(x.largeMessageDestinations))
	copy(out, x.largeMessageDestinations)
	return out
}

// MaxConcurrentLargeTransfers returns the HELLO-advertised cap on concurrent
// large-message transfers per duplex connection. The value is negotiated and
// enforced by the chunk reassembler and the sender-side gate.
func (x *Config) MaxConcurrentLargeTransfers() uint32 {
	return x.maxConcurrentLargeTransfers
}

// ChunkSize returns the logical-frame size above which duplex senders split
// into CHUNK frames. The value is local and not negotiated.
func (x *Config) ChunkSize() uint32 {
	return x.chunkSize
}

// MaxMessageSize returns the HELLO-advertised cap on a reassembled logical
// frame. The value is negotiated as the pairwise minimum.
func (x *Config) MaxMessageSize() uint64 {
	return x.maxMessageSize
}

// CreditWindow returns the unreclaimed in-flight byte budget per duplex
// connection. See [WithCreditWindow] for the purpose of credit and how the
// value is negotiated.
func (x *Config) CreditWindow() uint64 {
	return x.creditWindow
}

func (x *Config) Validate() error {
	v := validation.
		New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("bindAddr", x.bindAddr)).
		AddAssertion(x.maxFrameSize >= 16*size.KB && x.maxFrameSize <= 16*size.MB, "maxFrameSize must be between 16KB and 16MB").
		AddAssertion(x.chunkSize >= 16*size.KB && x.chunkSize <= 4*size.MB, "chunkSize must be between 16KB and 4MB").
		AddAssertion(x.maxFrameSize >= x.chunkSize, "maxFrameSize must be at least chunkSize").
		AddAssertion(x.maxMessageSize >= uint64(x.maxFrameSize), "maxMessageSize must be greater than or equal to maxFrameSize").
		AddAssertion(x.maxMessageSize <= uint64(math.MaxUint32), "maxMessageSize must fit the 32-bit frame length").
		AddAssertion(x.creditWindow > 0, "creditWindow must be greater than 0").
		AddAssertion(x.creditWindow >= uint64(x.chunkSize), "creditWindow must be at least chunkSize").
		AddAssertion(x.bindPort >= 0 && x.bindPort <= 65535, "invalid bindPort").
		AddAssertion(x.readIdleTimeout >= 0, "invalid server read idle timeout").
		AddAssertion(x.writeTimeout >= 0, "invalid server write timeout").
		AddAssertion(len(x.serializers) > 0, "at least one serializer is required").
		AddAssertion(x.maxIdleConns > 0, "maxIdleConns must be greater than 0").
		AddAssertion(x.dialTimeout > 0, "dialTimeout must be greater than 0").
		AddAssertion(x.keepAlive > 0, "keepAlive must be greater than 0").
		AddAssertion(x.protocolPin.Valid(), "invalid protocol pin").
		AddAssertion(x.ordinaryLanes >= 1 && x.ordinaryLanes <= inet.MaxOrdinaryLanes, "ordinaryLanes must be between 1 and 254").
		AddAssertion(x.maxConcurrentLargeTransfers >= 1, "maxConcurrentLargeTransfers must be greater than 0")

	if x.readIdleTimeout > 0 && x.idleTimeout > 0 {
		v = v.AddAssertion(x.readIdleTimeout < x.idleTimeout, "readIdleTimeout must be less than idleTimeout when both are set")
	}

	for _, pattern := range x.largeMessageDestinations {
		p := pattern
		v = v.AddAssertion(p != "", "largeMessageDestinations patterns must be non-empty").
			AddAssertion(isValidLargeDestinationPattern(p), "invalid largeMessageDestinations pattern: "+p)
	}

	return v.Validate()
}

// isValidLargeDestinationPattern reports whether pattern is accepted by the
// hierarchical-path matcher used for large-lane routing.
func isValidLargeDestinationPattern(pattern string) bool {
	normalized := normalizeHierarchicalPath(pattern)
	if normalized == "" {
		return false
	}
	_, err := path.Match(normalized, "/probe")
	return err == nil
}

// normalizeHierarchicalPath strips a leading slash so path.Match patterns and
// receiver path suffixes compare in the same shape.
func normalizeHierarchicalPath(p string) string {
	return strings.TrimPrefix(strings.TrimSpace(p), "/")
}
