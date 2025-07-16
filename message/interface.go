/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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

package message

import (
	"encoding"
)

// Message represents a serializable unit of communication exchanged between actors.
//
// In actor-based systems, messages are the primary means of interaction between
// isolated, concurrent entities. This interface defines the contract for any
// message type that can be marshaled into a binary form for transmission across
// process boundaries, network channels, or persistent storage, and subsequently
// unmarshaled back into a usable message structure.
//
// All Message implementations must satisfy the Serializable interface, ensuring
// they can be reliably encoded and decoded for transport or persistence.
//
// Concrete message types or wrappers (e.g., for JSON, Protobuf, Avro, or custom binary formats)
// should implement this interface to integrate seamlessly into a distributed or
// concurrent actor runtime. This allows for diverse serialization strategies
// while maintaining a consistent message contract.
type Message interface {
	Serializable
}

// Serializable defines the contract for types that can be reliably marshaled
// into a binary format and unmarshaled back from it.
//
// This interface combines encoding.BinaryMarshaler and encoding.BinaryUnmarshaler
// to provide a complete two-way serialization capability. Any type that needs
// to be transmitted across process boundaries, persisted to disk, or sent over
// a network within an actor system must implement Serializable.
//
// Implementations should be mindful of versioning when defining their binary
// formats to ensure forward and backward compatibility. For example, adding new
// fields should be done in a way that older versions of the unmarshaler can
// gracefully ignore them, and vice-versa.
type Serializable interface {
	encoding.BinaryMarshaler
	encoding.BinaryUnmarshaler
}
