package stream

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tochemey/goakt/log"
)

func TestOptions(t *testing.T) {
	storage := NewMemStore(5, time.Second)
	logger := log.New(log.InfoLevel, os.Stderr)
	testCases := []struct {
		name     string
		option   Option
		expected *EventsStream
	}{
		{
			name:     "WithStorage",
			option:   WithStorage(storage),
			expected: &EventsStream{storage: storage},
		},
		{
			name:     "WithLogger",
			option:   WithLogger(logger),
			expected: &EventsStream{logger: logger},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			es := new(EventsStream)
			tc.option.Apply(es)
			assert.Equal(t, tc.expected, es)
		})
	}
}
