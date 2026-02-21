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

package cluster

import (
	"fmt"
	"time"
)

type EventType int

const (
	NodeJoined EventType = iota
	NodeLeft
)

func (x EventType) String() string {
	switch x {
	case NodeJoined:
		return "NodeJoined"
	case NodeLeft:
		return "NodeLeft"
	default:
		return fmt.Sprintf("%d", int(x))
	}
}

// Event defines the cluster event
type Event struct {
	Payload any
	Type    EventType
}

// NodeJoined defines the node joined event
type NodeJoinedEvent struct {
	// Specifies the node address
	Address string
	// Specifies the timestamp
	Timestamp time.Time
}

// NodeLeft defines the node left event
type NodeLeftEvent struct {
	// Specifies the node address
	Address string
	// Specifies the timestamp
	Timestamp time.Time
}
