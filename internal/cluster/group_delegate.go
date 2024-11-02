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

package cluster

import (
	"encoding/json"

	"github.com/hashicorp/memberlist"
)

// groupDelegate implements memberlist Delegate
type groupDelegate struct {
	meta []byte
}

// newGroupDelegate creates an instance of groupDelegate
func (g *Group) newGroupDelegate() (*groupDelegate, error) {
	node := g.node
	bytea, err := json.Marshal(node)
	if err != nil {
		return nil, err
	}
	return &groupDelegate{bytea}, nil
}

func (deletgate *groupDelegate) NodeMeta(limit int) []byte {
	return deletgate.meta
}

func (deletgate *groupDelegate) NotifyMsg(bytes []byte) {
}

func (deletgate *groupDelegate) GetBroadcasts(overhead, limit int) [][]byte {
	return nil
}

func (deletgate *groupDelegate) LocalState(join bool) []byte {
	return nil
}

func (deletgate *groupDelegate) MergeRemoteState(buf []byte, join bool) {
}

var _ memberlist.Delegate = (*groupDelegate)(nil)
