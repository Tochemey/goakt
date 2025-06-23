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

package bench

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/bench/benchpb"
)

type Grain struct{}

var _ actor.Grain = (*Grain)(nil)

func NewGrain() *Grain {
	return &Grain{}
}

func (x *Grain) OnActivate(*actor.GrainContext) error {
	return nil
}

func (x *Grain) OnDeactivate(*actor.GrainContext) error {
	return nil
}

func (x *Grain) ReceiveSync(_ context.Context, message proto.Message) (proto.Message, error) {
	switch msg := message.(type) {
	case *benchpb.BenchRequest:
		return &benchpb.BenchResponse{}, nil
	default:
		return nil, fmt.Errorf("unhandled message type %T", msg)
	}
}

func (x *Grain) ReceiveAsync(_ context.Context, message proto.Message) error {
	switch msg := message.(type) {
	case *benchpb.BenchTell:
		return nil
	default:
		return fmt.Errorf("unhandled message type %T", msg)
	}
}
