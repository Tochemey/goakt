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

package router

// RoutingStrategy defines the routing strategy
type RoutingStrategy int

const (
	// RoundRobinStrategy rotates over the set of routeesMap making sure that if there are n routeesMap,
	// then for n messages sent through the router, each actor is forwarded one message.
	RoundRobinStrategy RoutingStrategy = iota
	// RandomStrategy selects a routee when a message is sent through the router.
	RandomStrategy
	// ConsistentHashingStrategy uses consistent hashing to select a routee based on the sent message.
	// Consistent hashing delivers messages with the same hash to the same routee as long as the set of routeesMap stays the same.
	// When the set of routeesMap changes, consistent hashing tries to make sure,
	// but does not guarantee, that messages with the same hash are routed to the same routee.
	ConsistentHashingStrategy
)
