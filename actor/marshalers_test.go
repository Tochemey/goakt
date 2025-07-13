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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/passivation"
)

func TestMarshalAndUnmarshalPassivationStrategy(t *testing.T) {
	t.Run("TimeBasedStrategy", func(t *testing.T) {
		duration := time.Second
		strategy := passivation.NewTimeBasedStrategy(duration)
		expected := &internalpb.PassivationStrategy{
			Strategy: &internalpb.PassivationStrategy_TimeBased{
				TimeBased: &internalpb.TimeBasedPassivation{
					PassivateAfter: durationpb.New(duration),
				},
			},
		}
		result := marshalPassivationStrategy(strategy)
		require.Equal(t, prototext.Format(expected), prototext.Format(result))

		unmarshalled := unmarshalPassivationStrategy(result)
		require.NotNil(t, unmarshalled)
		require.Equal(t, strategy.String(), unmarshalled.String())
	})
	t.Run("MessagesCountBasedStrategy", func(t *testing.T) {
		maxMessages := 10
		strategy := passivation.NewMessageCountBasedStrategy(maxMessages)
		expected := &internalpb.PassivationStrategy{
			Strategy: &internalpb.PassivationStrategy_MessagesCountBased{
				MessagesCountBased: &internalpb.MessagesCountBasedPassivation{
					MaxMessages: int64(maxMessages),
				},
			},
		}
		result := marshalPassivationStrategy(strategy)
		require.Equal(t, prototext.Format(expected), prototext.Format(result))
		unmarshalled := unmarshalPassivationStrategy(result)
		require.NotNil(t, unmarshalled)
		require.Equal(t, strategy.String(), unmarshalled.String())
	})
	t.Run("LongLivedStrategy", func(t *testing.T) {
		strategy := passivation.NewLongLivedStrategy()
		expected := &internalpb.PassivationStrategy{
			Strategy: &internalpb.PassivationStrategy_LongLived{
				LongLived: new(internalpb.LongLivedPassivation),
			},
		}
		result := marshalPassivationStrategy(strategy)
		require.Equal(t, prototext.Format(expected), prototext.Format(result))
		unmarshalled := unmarshalPassivationStrategy(result)
		require.NotNil(t, unmarshalled)
		require.Equal(t, strategy.String(), unmarshalled.String())
	})
}
