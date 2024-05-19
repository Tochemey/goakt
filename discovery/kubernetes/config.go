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

package kubernetes

import (
	"github.com/tochemey/goakt/internal/validation"
)

// Config defines the kubernetes discovery configuration
type Config struct {
	// Namespace specifies the kubernetes namespace
	Namespace string
	// ApplicationName specifies the application name
	ApplicationName string
	// GossipPortName specifies the gossip port name
	GossipPortName string
	// RemotingPortName specifies the remoting port name
	RemotingPortName string
	// PeersPortName specifies the cluster port name
	PeersPortName string
	// ActorSystemName specifies the given actor system name
	ActorSystemName string
}

// Validate checks whether the given discovery configuration is valid
func (x Config) Validate() error {
	return validation.New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("Namespace", x.Namespace)).
		AddValidator(validation.NewEmptyStringValidator("ApplicationName", x.ApplicationName)).
		AddValidator(validation.NewEmptyStringValidator("GossipPortName", x.GossipPortName)).
		AddValidator(validation.NewEmptyStringValidator("PeersPortName", x.PeersPortName)).
		AddValidator(validation.NewEmptyStringValidator("RemotingPortName", x.RemotingPortName)).
		AddValidator(validation.NewEmptyStringValidator("ActorSystemName", x.ActorSystemName)).
		Validate()
}
