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

package eventstream

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

// benchSubscriber is a minimal Subscriber implementation used for benchmarks.
// It avoids allocations and I/O so the benchmark focuses on stream delivery cost.
type benchSubscriber struct {
	id     string
	active atomic.Bool
	seen   atomic.Uint64
}

func newBenchSubscriber(id string) *benchSubscriber {
	s := &benchSubscriber{id: id}
	s.active.Store(true)
	return s
}

func (s *benchSubscriber) ID() string              { return s.id }
func (s *benchSubscriber) Active() bool            { return s.active.Load() }
func (s *benchSubscriber) Topics() []string        { return nil }
func (s *benchSubscriber) Iterator() chan *Message { ch := make(chan *Message); close(ch); return ch }
func (s *benchSubscriber) Shutdown()               { s.active.Store(false) }
func (s *benchSubscriber) signal(_ *Message)       { s.seen.Add(1) }
func (s *benchSubscriber) subscribe(_ string)      {}
func (s *benchSubscriber) unsubscribe(_ string)    {}

func newBenchmarkStream(topic string, subs int) *EventsStream {
	es := &EventsStream{
		subscribers: make(map[string]Subscriber, subs),
		topics:      make(map[string]map[string]Subscriber, 1),
	}
	es.topics[topic] = make(map[string]Subscriber, subs)
	for i := 0; i < subs; i++ {
		sub := newBenchSubscriber(strconv.Itoa(i))
		es.subscribers[sub.ID()] = sub
		es.topics[topic][sub.ID()] = sub
	}
	return es
}

// publishToTopicAsyncWait replicates the previous goroutine-per-subscriber delivery,
// but waits for all deliveries to complete so we can compare end-to-end cost.
func publishToTopicAsyncWait(es *EventsStream, topic string, msg any) {
	es.topicsMu.RLock()
	subs := es.topics[topic]
	if len(subs) == 0 {
		es.topicsMu.RUnlock()
		return
	}
	snapshot := make([]Subscriber, 0, len(subs))
	for _, sub := range subs {
		snapshot = append(snapshot, sub)
	}
	es.topicsMu.RUnlock()

	message := NewMessage(topic, msg)

	var wg sync.WaitGroup
	wg.Add(len(snapshot))
	for _, sub := range snapshot {
		sub := sub
		if !sub.Active() {
			wg.Done()
			continue
		}
		go func() {
			defer wg.Done()
			sub.signal(message)
		}()
	}
	wg.Wait()
}

func benchmarkPublishSync(b *testing.B, subs int) {
	const topic = "t"
	es := newBenchmarkStream(topic, subs)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		es.publishToTopic(topic, i)
	}
}

func benchmarkPublishAsyncWait(b *testing.B, subs int) {
	const topic = "t"
	es := newBenchmarkStream(topic, subs)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		publishToTopicAsyncWait(es, topic, i)
	}
}

func BenchmarkPublishSync_Fanout1(b *testing.B)    { benchmarkPublishSync(b, 1) }
func BenchmarkPublishSync_Fanout10(b *testing.B)   { benchmarkPublishSync(b, 10) }
func BenchmarkPublishSync_Fanout100(b *testing.B)  { benchmarkPublishSync(b, 100) }
func BenchmarkPublishSync_Fanout1000(b *testing.B) { benchmarkPublishSync(b, 1000) }

func BenchmarkPublishAsyncWait_Fanout1(b *testing.B)    { benchmarkPublishAsyncWait(b, 1) }
func BenchmarkPublishAsyncWait_Fanout10(b *testing.B)   { benchmarkPublishAsyncWait(b, 10) }
func BenchmarkPublishAsyncWait_Fanout100(b *testing.B)  { benchmarkPublishAsyncWait(b, 100) }
func BenchmarkPublishAsyncWait_Fanout1000(b *testing.B) { benchmarkPublishAsyncWait(b, 1000) }
