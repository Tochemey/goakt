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

package lsm

import "github.com/tochemey/goakt/v2/log"

type Option interface {
	Apply(*LSMTree)
}

// enforce compilation error
var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(*LSMTree)

func (f OptionFunc) Apply(t *LSMTree) {
	f(t)
}

// WithSkipListMaxLevel sets the custom SkipList max level
func WithSkipListMaxLevel(maxLevel int) Option {
	return OptionFunc(func(l *LSMTree) {
		l.maxLevel = maxLevel
	})
}

// WithProbability sets the custom SkipList probability
func WithProbability(probability float64) Option {
	return OptionFunc(func(l *LSMTree) {
		l.probability = probability
	})
}

// WithMemTableSizeThreshold sets the custom MemTable size threshold
func WithMemTableSizeThreshold(threshold int) Option {
	return OptionFunc(func(l *LSMTree) {
		l.memTableSizeThreshold = threshold
	})
}

// WithDataBlockByteThreshold sets the custom data block byte threshold
func WithDataBlockByteThreshold(threshold int) Option {
	return OptionFunc(func(l *LSMTree) {
		l.datablockByteThreshold = threshold
	})
}

// WithLogger sets a custom logger
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(l *LSMTree) {
		l.logger = logger
	})
}
