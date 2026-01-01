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

package brotli

import (
	"connectrpc.com/connect"
	"github.com/andybalholm/brotli"
)

// Brotli compression levels (re-exported for convenience).
const (
	BestSpeed          = brotli.BestSpeed
	BestCompression    = brotli.BestCompression
	DefaultCompression = brotli.DefaultCompression
)

// Name is the identifier for Brotli compression in Connect.
const Name = "br"

// WithCompression returns client and handler options using the default Brotli
// compression level.
func WithCompression() connect.Option {
	return WithCompressionLevel(DefaultCompression)
}

// WithCompressionLevel returns client and handler options for Brotli compression
// at the provided level. The caller is responsible for choosing an appropriate
// tradeoff between speed and compression ratio.
func WithCompressionLevel(level int) connect.Option {
	decompressor, compressor := brotliCompressions(level)
	return compressionOption{
		ClientOption:  connect.WithAcceptCompression(Name, decompressor, compressor),
		HandlerOption: connect.WithCompression(Name, decompressor, compressor),
	}
}

// brotliCompressions returns factory functions for Brotli compressors and decompressors.
func brotliCompressions(level int) (
	newDecompressor func() connect.Decompressor,
	newCompressor func() connect.Compressor,
) {
	writerPool := getWriterPool(level)

	newDecompressor = func() connect.Decompressor {
		reader := readerPool.Get().(*brotli.Reader)
		return &pooledBrotliDecompressor{
			Reader: reader,
		}
	}

	newCompressor = func() connect.Compressor {
		writer := writerPool.Get().(*brotli.Writer)
		return &pooledBrotliCompressor{
			Writer: writer,
			pool:   writerPool,
		}
	}

	return
}

// compressionOption bundles client and handler compression options.
type compressionOption struct {
	connect.ClientOption
	connect.HandlerOption
}
