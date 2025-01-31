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

package bloom

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNoFalseNegatives(t *testing.T) {
	n := 1000
	p := 0.01
	bf := New(n, p)

	for i := 0; i < n; i++ {
		bf.Add(strconv.Itoa(i))
	}

	for i := 0; i < n; i++ {
		assert.True(t, bf.Contains(strconv.Itoa(i)), "Expected Bloom Filter to contain '%d', but it did not", i)
	}
}

func TestFalsePositiveRate(t *testing.T) {
	n := 1000
	p := 0.01
	bf := New(n, p)

	for i := 0; i < n; i++ {
		bf.Add(strconv.Itoa(i))
	}

	falsePositives := 0
	testSize := 10000

	for i := n; i < n+testSize; i++ {
		if bf.Contains(strconv.Itoa(i)) {
			falsePositives++
		}
	}

	actualP := float64(falsePositives) / float64(testSize)
	t.Log(actualP)
}
