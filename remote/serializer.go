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

package remote

// Serializer is the extension point for plugging a custom wire format into
// GoAkt's remoting layer. Implement this interface when you need to send
// messages between actors running on different nodes using an encoding other
// than the built-in protobuf framing.
//
// # Responsibilities
//
// A Serializer implementation is responsible for two complementary operations:
//   - [Serializer.Serialize]: encode an arbitrary Go value into a self-describing
//     byte slice that can be safely transmitted over the network.
//   - [Serializer.Deserialize]: decode that byte slice back into a Go value
//     with the exact concrete type it had before serialization. The caller
//     uses a type assertion on the returned value, so the round-trip must
//     preserve the original dynamic type.
//
// # Self-description requirement
//
// Because GoAkt's remoting layer must reconstruct the correct Go type on the
// receiving end without out-of-band coordination, the encoded bytes must be
// self-describing. That is, the serialized payload must embed enough
// information (e.g. a fully-qualified type name or a registered numeric ID)
// for [Serializer.Deserialize] to determine which concrete type to instantiate.
//
// # Concurrency
//
// A single Serializer instance may be called from multiple goroutines
// concurrently. Implementations must be safe for concurrent use without
// external synchronization.
//
// # Error handling
//
// Both methods must return a non-nil error when encoding or decoding fails.
// Errors should be descriptive enough for operators to diagnose malformed
// payloads or unrecognized type identifiers. Returning a nil error alongside
// a nil or zero value is incorrect and may cause silent data loss.
//
// # Example implementation
//
//	type JSONSerializer struct{}
//
//	type envelope struct {
//	    Type    string          `json:"type"`
//	    Payload json.RawMessage `json:"payload"`
//	}
//
//	func (s *JSONSerializer) Serialize(message any) ([]byte, error) {
//	    payload, err := json.Marshal(message)
//	    if err != nil {
//	        return nil, err
//	    }
//	    env := envelope{
//	        Type:    fmt.Sprintf("%T", message),
//	        Payload: payload,
//	    }
//	    return json.Marshal(env)
//	}
//
//	func (s *JSONSerializer) Deserialize(data []byte) (any, error) {
//	    var env envelope
//	    if err := json.Unmarshal(data, &env); err != nil {
//	        return nil, err
//	    }
//	    // resolve the concrete type by name and unmarshal into it …
//	}
type Serializer interface {
	// Serialize encodes message into a byte slice suitable for transmission
	// over the network. The encoding must be self-describing so that
	// [Serializer.Deserialize] can reconstruct the original concrete type on
	// the remote node without additional context.
	//
	// message is guaranteed to be non-nil by the remoting layer, but
	// implementations should guard against unexpected nil values and return
	// an error rather than panic.
	//
	// Returns the encoded bytes and a nil error on success.
	// Returns a nil slice and a descriptive non-nil error on failure.
	Serialize(message any) ([]byte, error)

	// Deserialize decodes data produced by [Serializer.Serialize] and returns
	// the original Go value with its concrete type restored. The caller will
	// use a type assertion on the returned value, so the dynamic type must
	// match what was passed to Serialize.
	//
	// data is guaranteed to be non-empty by the remoting layer, but
	// implementations should validate the payload and return an error for
	// truncated, corrupted, or unrecognized input rather than panicking.
	//
	// Returns the decoded value and a nil error on success.
	// Returns nil and a descriptive non-nil error on failure.
	Deserialize(data []byte) (any, error)
}
