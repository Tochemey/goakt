package eventbus

import (
	"context"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	bus := New()
	if bus == nil {
		t.Log("New EventBus not created!")
		t.Fail()
	}
}

func TestHasCallback(t *testing.T) {
	ctx := context.TODO()
	bus := New()
	_ = bus.Subscribe(ctx, "topic", func() {})
	if bus.HasCallback("topic_topic") {
		t.Fail()
	}
	if !bus.HasCallback("topic") {
		t.Fail()
	}
}

func TestSubscribe(t *testing.T) {
	ctx := context.TODO()
	bus := New()
	if bus.Subscribe(ctx, "topic", func() {}) != nil {
		t.Fail()
	}
	if bus.Subscribe(ctx, "topic", "String") == nil {
		t.Fail()
	}
}

func TestSubscribeOnce(t *testing.T) {
	ctx := context.TODO()
	bus := New()
	if bus.SubscribeOnce(ctx, "topic", func() {}) != nil {
		t.Fail()
	}
	if bus.SubscribeOnce(ctx, "topic", "String") == nil {
		t.Fail()
	}
}

func TestSubscribeOnceAndManySubscribe(t *testing.T) {
	ctx := context.TODO()
	bus := New()
	event := "topic"
	flag := 0
	fn := func() { flag++ }
	_ = bus.SubscribeOnce(ctx, event, fn)
	_ = bus.Subscribe(ctx, event, fn)
	_ = bus.Subscribe(ctx, event, fn)
	bus.Publish(ctx, event)

	if flag != 3 {
		t.Fail()
	}
}

func TestUnsubscribe(t *testing.T) {
	bus := New()
	ctx := context.TODO()
	handler := func() {}
	_ = bus.Subscribe(ctx, "topic", handler)
	if bus.Unsubscribe(ctx, "topic", handler) != nil {
		t.Fail()
	}
	if bus.Unsubscribe(ctx, "topic", handler) == nil {
		t.Fail()
	}
}

type handler struct {
	val int
}

func (h *handler) Handle() {
	h.val++
}

func TestUnsubscribeMethod(t *testing.T) {
	bus := New()
	h := &handler{val: 0}
	ctx := context.TODO()
	_ = bus.Subscribe(ctx, "topic", h.Handle)
	bus.Publish(ctx, "topic")
	if bus.Unsubscribe(ctx, "topic", h.Handle) != nil {
		t.Fail()
	}
	if bus.Unsubscribe(ctx, "topic", h.Handle) == nil {
		t.Fail()
	}
	bus.Publish(ctx, "topic")
	bus.WaitAsync()

	if h.val != 1 {
		t.Fail()
	}
}

func TestPublish(t *testing.T) {
	bus := New()
	ctx := context.TODO()
	_ = bus.Subscribe(ctx, "topic", func(a int, err error) {
		if a != 10 {
			t.Fail()
		}

		if err != nil {
			t.Fail()
		}
	})
	bus.Publish(ctx, "topic", 10, nil)
}

func TestSubscribeOnceAsync(t *testing.T) {
	results := make([]int, 0)
	ctx := context.TODO()
	bus := New()
	_ = bus.SubscribeOnceAsync(ctx, "topic", func(a int, out *[]int) {
		*out = append(*out, a)
	})

	bus.Publish(ctx, "topic", 10, &results)
	bus.Publish(ctx, "topic", 10, &results)

	bus.WaitAsync()

	if len(results) != 1 {
		t.Fail()
	}

	if bus.HasCallback("topic") {
		t.Fail()
	}
}

func TestSubscribeAsyncTransactional(t *testing.T) {
	results := make([]int, 0)
	ctx := context.TODO()
	bus := New()
	_ = bus.SubscribeAsync(ctx, "topic", func(a int, out *[]int, dur string) {
		sleep, _ := time.ParseDuration(dur)
		time.Sleep(sleep)
		*out = append(*out, a)
	}, true)

	bus.Publish(ctx, "topic", 1, &results, "1s")
	bus.Publish(ctx, "topic", 2, &results, "0s")

	bus.WaitAsync()

	if len(results) != 2 {
		t.Fail()
	}

	if results[0] != 1 || results[1] != 2 {
		t.Fail()
	}
}

func TestSubscribeAsync(t *testing.T) {
	results := make(chan int)
	ctx := context.TODO()
	bus := New()
	_ = bus.SubscribeAsync(ctx, "topic", func(a int, out chan<- int) {
		out <- a
	}, false)

	bus.Publish(ctx, "topic", 1, results)
	bus.Publish(ctx, "topic", 2, results)

	numResults := 0

	go func() {
		for range results {
			numResults++
		}
	}()

	bus.WaitAsync()

	time.Sleep(10 * time.Millisecond)

	// todo race detected during execution of test
	//if numResults != 2 {
	//	t.Fail()
	//}
}
