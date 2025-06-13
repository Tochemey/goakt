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

package actor

import (
	"fmt"
	"time"
)

// PassivationStrategy defines the contract for passivation strategies in the actor model.
// Implementations of this interface determine when an actor should be passivated
// based on specific conditions or events, such as inactivity or message count.
type PassivationStrategy interface {
	fmt.Stringer
	isStrategy()
}

// TimeBasedStrategy is a passivation strategy that triggers passivation
// after a specified period of actor inactivity.
type TimeBasedStrategy struct {
	timeout time.Duration
}

// ensure TimeBasedStrategy implements PassivationStrategy interface
var _ PassivationStrategy = (*TimeBasedStrategy)(nil)

// NewTimeBasedStrategy creates and returns a new TimeBasedStrategy with the specified timeout duration.
//
// The timeout parameter defines the period of inactivity after which the actor should be considered for passivation.
// This strategy is useful when you want to automatically passivate actors that have not received any messages
// for a certain duration.
//
// Example:
//
//	strategy := NewTimeBasedStrategy(5 * time.Minute)
//
// Parameters:
//   - timeout: The duration of inactivity after which the actor will be eligible for passivation.
//
// Returns:
//   - *TimeBasedStrategy: A pointer to the newly created TimeBasedStrategy instance.
func NewTimeBasedStrategy(timeout time.Duration) *TimeBasedStrategy {
	return &TimeBasedStrategy{
		timeout: timeout,
	}
}

// Timeout returns the timeout duration configured for the TimeBasedStrategy.
//
// This value represents the period of inactivity after which the actor will be eligible for passivation.
// It can be used to inspect or log the current inactivity threshold.
//
// Returns:
//   - time.Duration: The configured timeout duration for passivation.
func (t *TimeBasedStrategy) Timeout() time.Duration {
	return t.timeout
}

// String implements PassivationStrategy.
func (t *TimeBasedStrategy) String() string {
	return "TimeBasedStrategy"
}

// isStrategy is a marker method to satisfy the PassivationStrategy interface.
//
// This method is not intended to be called directly. It exists solely to ensure
// that TimeBasedStrategy implements the PassivationStrategy interface.
func (t *TimeBasedStrategy) isStrategy() {}

type MessagesCountBasedStrategy struct {
	maxMessages int
}

// ensure MessageBasedStrategy implements PassivationStrategy interface
var _ PassivationStrategy = (*MessagesCountBasedStrategy)(nil)

// NewMessageCountBasedStrategy creates and returns a new MessageBasedStrategy with the specified maximum number of messages.
//
// The maxMessages parameter defines the threshold of messages after which the actor should be considered for passivation.
// This strategy is useful when you want to limit the number of messages an actor processes before being passivated.
//
// Example:
//
//	strategy := NewMessageCountBasedStrategy(100)
//
// Parameters:
//   - maxMessages: The maximum number of messages to process before passivation.
//
// Returns:
//   - *MessageBasedStrategy: A pointer to the newly created MessageBasedStrategy instance.
func NewMessageCountBasedStrategy(maxMessages int) *MessagesCountBasedStrategy {
	return &MessagesCountBasedStrategy{
		maxMessages: maxMessages,
	}
}

// MaxMessages returns the maximum number of messages configured for the MessageBasedStrategy.
//
// This value represents the threshold of messages after which the actor will be eligible for passivation.
// It can be used to inspect or log the current message threshold.
//
// Returns:
//   - int: The configured maximum number of messages for passivation.
func (m *MessagesCountBasedStrategy) MaxMessages() int {
	return m.maxMessages
}

// String implements PassivationStrategy.
func (m *MessagesCountBasedStrategy) String() string {
	return "MessagesCountBasedStrategy"
}

// isStrategy is a marker method to satisfy the PassivationStrategy interface.
//
// This method is not intended to be called directly. It exists solely to ensure
// that MessageBasedStrategy implements the PassivationStrategy interface.
func (m *MessagesCountBasedStrategy) isStrategy() {}

// LongLivedStrategy is a passivation strategy that does not trigger passivation,
// allowing actors to remain active indefinitely.
// This strategy is useful for actors that are expected to be long-lived and do not require passivation.
type LongLivedStrategy struct{}

// ensure LongLivedStrategy implements PassivationStrategy interface
var _ PassivationStrategy = (*LongLivedStrategy)(nil)

// NewLongLivedStrategy creates and returns a new LongLivedStrategy.
//
// This strategy is used for actors that are expected to remain active indefinitely,
// without any passivation conditions. It is suitable for actors that handle long-running tasks
// or maintain persistent state.
func NewLongLivedStrategy() *LongLivedStrategy {
	return &LongLivedStrategy{}
}

// String implements PassivationStrategy.
func (l *LongLivedStrategy) String() string {
	return "LongLivedStrategy"
}

// isStrategy is a marker method to satisfy the PassivationStrategy interface.
func (l *LongLivedStrategy) isStrategy() {}
