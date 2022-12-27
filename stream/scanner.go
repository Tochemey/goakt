package stream

import (
	"time"
)

// ScannerOption defines the Scanner option
type ScannerOption func(reader *Scanner)

// WithDedupe tells the reader to ignore duplicate
func WithDedupe() ScannerOption {
	return func(reader *Scanner) {
		reader.dedupe = true
	}
}

// Scanner helps drain messages from a message channel
type Scanner struct {
	// pipe defines the messages channel where to read the messages from
	pipe <-chan *Message
	// limit defines the number of message to read
	limit int
	// timeout defines how long the reader will read message
	timeout time.Duration
	// dedupe states whether to ignore duplicate
	dedupe bool
}

// NewScanner creates an instance of Scanner
func NewScanner(pipe <-chan *Message, limit int, timeout time.Duration, opts ...ScannerOption) *Scanner {
	reader := &Scanner{
		pipe:    pipe,
		limit:   limit,
		timeout: timeout,
		dedupe:  false,
	}
	// set the custom options to override the default values
	for _, opt := range opts {
		opt(reader)
	}

	return reader
}

// Scan drains messages from a given source
func (r Scanner) Scan() (messages []*Message, ok bool) {
	// loop until timeout or limit is reached
loop:
	for len(messages) < r.limit {
		select {
		case msg, ok := <-r.pipe:
			if !ok {
				break loop
			}
			messages = append(messages, msg)
			msg.Accept()
		case <-time.After(r.timeout):
			break loop
		}
	}

	if !r.dedupe {
		return messages, len(messages) == r.limit
	}

	dedupelist := make([]*Message, 0, r.limit)
	if r.dedupe {
		seen := make(map[string]struct{})
		for _, message := range messages {
			if _, ok := seen[message.ID()]; !ok {
				seen[message.ID()] = struct{}{}
				dedupelist = append(dedupelist, message)
			}
		}
	}

	return dedupelist, len(dedupelist) == r.limit
}
