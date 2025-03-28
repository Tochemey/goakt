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

import (
	"io"
	"runtime"
	"sync"

	"connectrpc.com/connect"
	"github.com/klauspost/compress/zstd"
)

// Zstd is the name of the Zstandard compression algorithm.
// Reference: https://www.iana.org/assignments/http-parameters/http-parameters.xml#content-coding
const Zstd = "zstd"

var zstdEncodersPool = sync.Pool{
	New: func() any {
		enc, _ := zstd.NewWriter(nil)
		return enc
	},
}

func zstdEncoder() *zstd.Encoder {
	enc, ok := zstdEncodersPool.Get().(*zstd.Encoder)
	if !ok || enc == nil {
		enc, _ = zstd.NewWriter(nil)
		zstdEncodersPool.Put(enc)
	}
	return enc
}

func releaseZstdEncoder(enc *zstd.Encoder) {
	enc.Reset(nil)
	zstdEncodersPool.Put(enc)
}

// NewZstdCompressor creates a new Zstandard compressor.
func NewZstdCompressor() connect.Compressor {
	encoder := zstdEncoder()
	runtime.SetFinalizer(encoder, func(e *zstd.Encoder) {
		releaseZstdEncoder(e)
	})
	return encoder
}

// NewZstdDecompressor returns a new Zstd Decompressor.
func NewZstdDecompressor() connect.Decompressor {
	d, err := zstd.NewReader(nil)
	if err != nil {
		return &decompressionError{err: err}
	}
	return &zstdDecompressor{
		decoder: d,
	}
}

// zstdDecompressor is a thin wrapper around a zstd Decoder.
type zstdDecompressor struct {
	decoder *zstd.Decoder
}

func (c *zstdDecompressor) Read(bytes []byte) (int, error) {
	if c.decoder == nil {
		return 0, io.EOF
	}
	return c.decoder.Read(bytes)
}

func (c *zstdDecompressor) Reset(rdr io.Reader) error {
	if c.decoder == nil {
		var err error
		c.decoder, err = zstd.NewReader(rdr)
		return err
	}
	return c.decoder.Reset(rdr)
}

func (c *zstdDecompressor) Close() error {
	if c.decoder == nil {
		return nil
	}
	c.decoder.Close()
	// zstd.Decoder cannot be re-used after close, even via Reset
	c.decoder = nil
	return nil
}
