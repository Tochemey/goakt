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

package passivation

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/duration"
)

func TestStrategy(t *testing.T) {
	timeStrategy := NewTimeBasedStrategy(5 * time.Minute)
	require.Implements(t, (*Strategy)(nil), timeStrategy)
	require.Equal(t, 5*time.Minute, timeStrategy.Timeout())
	require.Equal(t, fmt.Sprintf("Timed-Based of Duration=[%s]", duration.Format(5*time.Minute)), timeStrategy.String())

	messageCountStrategy := NewMessageCountBasedStrategy(2)
	require.Implements(t, (*Strategy)(nil), messageCountStrategy)
	require.EqualValues(t, 2, messageCountStrategy.MaxMessages())

	longlivedStrategy := NewLongLivedStrategy()
	require.Implements(t, (*Strategy)(nil), longlivedStrategy)
	require.Equal(t, "Long Lived", longlivedStrategy.String())
}
