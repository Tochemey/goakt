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

package compression

import "io"

// compressionError is a sentinel type for a connect.Compressor
// which will return an error upon first use.
type compressionError struct {
	err error
}

func (c *compressionError) Write(_ []byte) (int, error) {
	return 0, c.err
}

func (c *compressionError) Reset(_ io.Writer) {}

func (c *compressionError) Close() error {
	return c.err
}

// decompressionError is a sentinel type for a connect.Decompressor
// which will return an error upon first use.
type decompressionError struct {
	err error
}

func (c *decompressionError) Read(_ []byte) (int, error) {
	return 0, c.err
}
func (c *decompressionError) Reset(_ io.Reader) error {
	return c.err
}
func (c *decompressionError) Close() error {
	return c.err
}
