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

package mdns

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/mdns"
	"go.uber.org/atomic"

	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/internal/locker"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
)

const (
	// browseTimeout is how long one DiscoverPeers call listens for answers before it
	// returns what it has collected.
	browseTimeout = 5 * time.Second
	// instanceSuffixLength is the number of hexadecimal characters of the random suffix
	// appended to ServiceName to build the instance name this node registers.
	instanceSuffixLength = 8
	// entriesBuffer is the buffer of the channel the query streams the discovered entries into.
	// The library drops entries when the channel is full, so the channel is drained concurrently.
	entriesBuffer = 32
	// txtNameKey prefixes the TXT record entry carrying the cluster name. The ServiceName
	// follows the prefix, which is how a node tells its own cluster apart from any other
	// cluster answering on the same service and domain.
	txtNameKey = "name="
)

// Discovery defines the mDNS discovery provider
type Discovery struct {
	_      locker.NoCopy
	config *Config
	mu     sync.Mutex

	stopChan chan struct{}

	initialized *atomic.Bool

	// server answers mDNS queries for this node while it is registered.
	server *mdns.Server

	// logger receives the provider's own messages and the mDNS library's log output.
	logger log.Logger
}

// enforce compilation error
var _ discovery.Provider = &Discovery{}

// NewDiscovery returns an instance of the mDNS discovery provider
func NewDiscovery(config *Config, opts ...Option) *Discovery {
	x := &Discovery{
		mu:          sync.Mutex{},
		stopChan:    make(chan struct{}, 1),
		initialized: atomic.NewBool(false),
		config:      config,
		logger:      log.DiscardLogger,
	}

	// apply the various options
	for _, opt := range opts {
		opt.Apply(x)
	}

	return x
}

// ID returns the discovery provider identifier
func (x *Discovery) ID() string {
	return discovery.ProviderMDNS
}

// Initialize the discovery provider
func (x *Discovery) Initialize() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.initialized.Load() {
		return discovery.ErrAlreadyInitialized
	}

	return x.config.Validate()
}

// Register registers this node to a service discovery directory. The node advertises a unique
// instance of the configured service and carries the cluster name in its TXT record, which is
// what lets several nodes of the same cluster coexist on the network.
func (x *Discovery) Register() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if x.initialized.Load() {
		return discovery.ErrAlreadyRegistered
	}

	instance := x.config.ServiceName + "-" + uuid.NewString()[:instanceSuffixLength]
	domain := fqdn(x.config.Domain)

	// build the host name from the instance name so that no operating system host name
	// lookup is needed and host names never collide between nodes
	hostName := fqdn(instance + "." + strings.Trim(domain, "."))

	ips := advertisedAddresses()
	if len(ips) == 0 {
		return errors.New("no address to advertise for mDNS discovery")
	}

	txt := []string{txtNameKey + x.config.ServiceName}

	service, err := mdns.NewMDNSService(instance, x.config.Service, domain, hostName, x.config.Port, ips, txt)
	if err != nil {
		return fmt.Errorf("failed to create the mDNS service: %w", err)
	}

	server, err := mdns.NewServer(&mdns.Config{Zone: service, Logger: x.logger.StdLogger()})
	if err != nil {
		return fmt.Errorf("failed to start the mDNS server: %w", err)
	}

	x.server = server
	x.initialized.Store(true)

	return nil
}

// Deregister removes this node from a service discovery directory.
func (x *Discovery) Deregister() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	if !x.initialized.Load() {
		return discovery.ErrNotInitialized
	}

	x.initialized.Store(false)

	if x.server != nil {
		if err := x.server.Shutdown(); err != nil {
			return err
		}

		x.server = nil
	}

	close(x.stopChan)
	return nil
}

// Close closes the provider
func (x *Discovery) Close() error {
	return nil
}

// DiscoverPeers returns a list of known nodes.
func (x *Discovery) DiscoverPeers() ([]string, error) {
	if !x.initialized.Load() {
		return nil, discovery.ErrNotInitialized
	}

	entries, err := x.queryEntries()
	if err != nil {
		return nil, err
	}

	return x.addresses(entries), nil
}

// queryEntries runs one browse for the configured service and returns the entries that belong
// to this cluster.
func (x *Discovery) queryEntries() ([]*mdns.ServiceEntry, error) {
	ch := make(chan *mdns.ServiceEntry, entriesBuffer)
	done := make(chan types.Unit)

	// the library never blocks on the entries channel and drops what it cannot hand over,
	// so the channel is drained while the query runs
	var collected []*mdns.ServiceEntry

	go func() {
		defer close(done)

		for entry := range ch {
			collected = append(collected, entry)
		}
	}()

	// the query travels over IPv4 multicast only: the library sends every query over both
	// families and gives up on the first failure, and an IPv6 multicast send needs an
	// interface scope it only sets when pinned to a single interface. IPv6 peer addresses
	// are still discovered, since nodes answer with their AAAA records over IPv4.
	params := &mdns.QueryParam{
		Service:     x.config.Service,
		Domain:      fqdn(x.config.Domain),
		Timeout:     browseTimeout,
		Entries:     ch,
		Logger:      x.logger.StdLogger(),
		DisableIPv6: true,
	}

	err := mdns.Query(params)
	close(ch)
	<-done

	if err != nil {
		return nil, fmt.Errorf("failed to browse mDNS services: %w", err)
	}

	var entries []*mdns.ServiceEntry
	for _, entry := range collected {
		if x.matches(entry) {
			entries = append(entries, entry)
		}
	}

	return entries, nil
}

// matches reports whether an entry was registered by a node of this cluster: same port, same
// service and domain, and a TXT record carrying this cluster's name.
func (x *Discovery) matches(entry *mdns.ServiceEntry) bool {
	serviceSuffix := "." + strings.Trim(x.config.Service, ".") + "." + strings.Trim(x.config.Domain, ".") + "."

	return entry.Port == x.config.Port &&
		strings.HasSuffix(entry.Name, serviceSuffix) &&
		slices.Contains(entry.InfoFields, txtNameKey+x.config.ServiceName)
}

// addresses maps entries to host:port strings on the discovery port, IPv4 always and IPv6 when
// the configuration asks for it, sorted with duplicates removed.
func (x *Discovery) addresses(entries []*mdns.ServiceEntry) []string {
	v6 := x.config.IPv6 != nil && *x.config.IPv6

	var addresses []string
	for _, entry := range entries {
		if entry.AddrV4 != nil {
			addresses = append(addresses, net.JoinHostPort(entry.AddrV4.String(), strconv.Itoa(entry.Port)))
		}

		if v6 && entry.AddrV6IPAddr != nil {
			addresses = append(addresses, net.JoinHostPort(entry.AddrV6IPAddr.String(), strconv.Itoa(entry.Port)))
		}
	}

	slices.Sort(addresses)
	return slices.Compact(addresses)
}

// advertisedAddresses returns the unicast addresses this node advertises: every non-loopback
// address of the interfaces that are up and multicast capable, with IPv4 first, then global
// IPv6, falling back to link-local IPv6 only when no global one exists.
func advertisedAddresses() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var v4, v6, v6Local []net.IP

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok || ipnet.IP.IsLoopback() {
				continue
			}

			switch {
			case ipnet.IP.To4() != nil:
				v4 = append(v4, ipnet.IP)
			case ipnet.IP.IsGlobalUnicast():
				v6 = append(v6, ipnet.IP)
			case ipnet.IP.IsLinkLocalUnicast():
				v6Local = append(v6Local, ipnet.IP)
			}
		}
	}

	// link-local addresses are advertised only when the node has no global IPv6 address
	if len(v6) == 0 {
		v6 = v6Local
	}

	return append(v4, v6...)
}

// fqdn returns the fully qualified form of a domain or host name: the name stripped of its
// surrounding dots followed by a single trailing dot, which is what the mDNS library requires.
func fqdn(name string) string {
	return strings.Trim(name, ".") + "."
}
