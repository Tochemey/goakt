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

package actor

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPriorityIntake(t *testing.T) {
	t.Run("With empty intake draining to nil", func(t *testing.T) {
		var s priorityIntake
		require.Nil(t, s.drain())
	})

	t.Run("With drain returning arrival order", func(t *testing.T) {
		var s priorityIntake
		a := &ReceiveContext{}
		b := &ReceiveContext{}
		c := &ReceiveContext{}

		s.push(a)
		s.push(b)
		s.push(c)

		var got []*ReceiveContext
		for n := s.drain(); n != nil; n = chainNext(n) {
			got = append(got, n)
		}

		require.Equal(t, []*ReceiveContext{a, b, c}, got)
		// a second drain yields nothing
		require.Nil(t, s.drain())
	})

	t.Run("With concurrent producers losing no message", func(t *testing.T) {
		var s priorityIntake
		const (
			producers   = 8
			perProducer = 1000
		)

		var wg sync.WaitGroup
		wg.Add(producers)
		for range producers {
			go func() {
				defer wg.Done()
				for range perProducer {
					s.push(&ReceiveContext{})
				}
			}()
		}
		wg.Wait()

		count := 0
		for n := s.drain(); n != nil; n = chainNext(n) {
			count++
		}

		require.Equal(t, producers*perProducer, count)
	})
}
