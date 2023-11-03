/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
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

package discovery

import (
	"net"
	"os"
	"strconv"

	"github.com/caarlos0/env/v10"
	"github.com/pkg/errors"
)

// hostNodeConfig helps read the host Node settings
type hostNodeConfig struct {
	GossipPort   int    `env:"GOSSIP_PORT"`
	ClusterPort  int    `env:"CLUSTER_PORT"`
	RemotingPort int    `env:"REMOTING_PORT"`
	Name         string `env:"NODE_NAME" envDefault:""`
	Host         string `env:"NODE_IP" envDefault:""`
}

// Node represents a discovered Node
type Node struct {
	// Name specifies the discovered node's Name
	Name string
	// Host specifies the discovered node's Host
	Host string
	// GossipPort
	GossipPort int
	// ClusterPort
	ClusterPort int
	// RemotingPort
	RemotingPort int
}

// ClusterAddress returns address the node's peers will use to connect to
func (n Node) ClusterAddress() string {
	return net.JoinHostPort(n.Host, strconv.Itoa(n.ClusterPort))
}

// GossipAddress returns the node discovery address
func (n Node) GossipAddress() string {
	return net.JoinHostPort(n.Host, strconv.Itoa(n.GossipPort))
}

// HostNode returns the Node where the discovery provider is running
func HostNode() (*Node, error) {
	// load the host node configuration
	cfg := &hostNodeConfig{}
	opts := env.Options{RequiredIfNoDef: true, UseFieldNameByDefault: false}
	if err := env.ParseWithOptions(cfg, opts); err != nil {
		return nil, err
	}
	// check for empty host and name
	if cfg.Host == "" {
		// let us perform a host lookup
		host, err := os.Hostname()
		// handle the error
		if err != nil {
			return nil, errors.Wrap(err, "failed to get the hostname")
		}
		// set the host
		cfg.Host = host
	}

	// set the name as host if it is empty
	if cfg.Name == "" {
		cfg.Name = cfg.Host
	}

	// create the host node
	return &Node{
		Name:         cfg.Name,
		Host:         cfg.Host,
		GossipPort:   cfg.GossipPort,
		ClusterPort:  cfg.ClusterPort,
		RemotingPort: cfg.RemotingPort,
	}, nil
}
