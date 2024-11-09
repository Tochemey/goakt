/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package cluster

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/reugn/go-quartz/logger"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/tochemey/goakt/v2/discovery"
	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/log"
)

// eventsHandler handles cluster topology events
// returned by memberlist
type eventsHandler struct {
	events chan *Event
	host   *discovery.Node
	logger log.Logger
}

// enforce compilation error
var _ memberlist.EventDelegate = (*eventsHandler)(nil)

// newEventsHandler creates an instance of eventsHandler
func (n *Node) newEventsHandler() *eventsHandler {
	return &eventsHandler{
		events: n.events,
		host:   n.node,
		logger: n.logger,
	}
}

// NotifyJoin is executed when a node joined the cluster
func (handler *eventsHandler) NotifyJoin(node *memberlist.Node) {
	discoNode, err := handler.toNode(node)
	if err != nil {
		logger.Error(err)
		return
	}

	if discoNode == nil {
		return
	}

	payload, _ := anypb.New(&goaktpb.NodeJoined{
		Address:   discoNode.PeersAddress(),
		Timestamp: timestamppb.New(time.Now().UTC()),
	})

	handler.events <- &Event{payload, NodeJoined}
}

// NotifyLeave is executed when a node leaves the cluster
func (handler *eventsHandler) NotifyLeave(node *memberlist.Node) {
	discoNode, err := handler.toNode(node)
	if err != nil {
		logger.Error(err)
		return
	}

	if discoNode == nil {
		return
	}

	payload, _ := anypb.New(&goaktpb.NodeLeft{
		Address:   discoNode.PeersAddress(),
		Timestamp: timestamppb.New(time.Now().UTC()),
	})

	handler.events <- &Event{payload, NodeLeft}
	close(handler.events)
}

// NotifyUpdate is executed when a node is updated in the cluster
func (handler *eventsHandler) NotifyUpdate(*memberlist.Node) {
	// no-op
}

// toNode returns a discovery node from the cluster node information
func (handler *eventsHandler) toNode(node *memberlist.Node) (*discovery.Node, error) {
	if node == nil {
		return nil, nil
	}

	var discoNode *discovery.Node
	if err := json.Unmarshal(node.Meta, &discoNode); err != nil {
		return nil, fmt.Errorf("failed to unpack GoAkt cluster node:(%s) meta: %w", node.Address(), err)
	}

	if discoNode.PeersAddress() == handler.host.PeersAddress() {
		return nil, nil
	}
	return discoNode, nil
}
