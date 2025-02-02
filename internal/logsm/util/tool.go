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

package util

import (
	"io"

	"github.com/klauspost/compress/s2"
)

func CommonPrefixLength(a, b string) int {
	n := min(len(a), len(b))
	var i int
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

func Pow(x, n int) int {
	res := 1
	for range n {
		res *= x
	}
	return res
}

func Compress(src io.Reader, dst io.Writer) error {
	enc := s2.NewWriter(dst)
	_, err := io.Copy(enc, src)
	if err != nil {
		_ = enc.Close()
		return err
	}
	return enc.Close()
}

func Decompress(src io.Reader, dst io.Writer) error {
	dec := s2.NewReader(src)
	_, err := io.Copy(dst, dec)
	return err
}
