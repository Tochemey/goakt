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

// Package workerpool provides a highly efficient and scalable worker pool implementation
// for concurrent task execution in Go applications.
package workerpool

import (
	"errors"
	"os"
	"time"

	"github.com/panjf2000/ants/v2"

	"github.com/tochemey/goakt/v3/log"
)

const (
	MaxPoolSize = 5e4
)

// ErrPoolClosed will be returned when submitting task to a closed pool.
var ErrPoolClosed = errors.New("this pool has been closed")

// Option is the interface that applies a WorkerPool option.
type Option interface {
	// Apply sets the Option value of a WorkerPool.
	Apply(pool *WorkerPool)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(pool *WorkerPool)

// Apply applies the Node's option
func (f OptionFunc) Apply(pool *WorkerPool) {
	f(pool)
}

// WithPassivateAfter sets the passivate after duration
func WithPassivateAfter(d time.Duration) Option {
	return OptionFunc(func(pool *WorkerPool) {
		pool.passivateAfter = d
	})
}

// WithPoolSize sets the pool size
func WithPoolSize(size int) Option {
	return OptionFunc(func(pool *WorkerPool) {
		if size > MaxPoolSize {
			size = MaxPoolSize
		}
		pool.poolSize = size
	})
}

// WithLogger sets the actor system custom log
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(pool *WorkerPool) {
		pool.logger = logger
	})
}

// WorkerPool manages a pool of workers across multiple shards for efficient
// concurrent task execution.
type WorkerPool struct {
	pool           *ants.Pool
	poolSize       int
	passivateAfter time.Duration
	logger         log.Logger
}

// New creates a new worker pool with the given options.
func New(opts ...Option) *WorkerPool {
	wp := &WorkerPool{
		poolSize:       MaxPoolSize,
		passivateAfter: time.Second,
		logger:         log.New(log.ErrorLevel, os.Stderr),
	}
	// Apply provided options
	for _, opt := range opts {
		opt.Apply(wp)
	}

	return wp
}

// Start initializes the worker pool
func (wp *WorkerPool) Start() error {
	pool, err := ants.NewPool(wp.poolSize,
		ants.WithExpiryDuration(wp.passivateAfter),
		ants.WithLogger(newLogger(wp.logger)),
	)
	if err != nil {
		return err
	}
	wp.pool = pool
	return nil
}

// Stop gracefully shuts down the worker pool by closing all worker channels
// and preventing new task submissions.
func (wp *WorkerPool) Stop() {
	wp.pool.Release()
}

// SubmitWork submits a task to be executed by an available worker.
// If the pool has not been started, the task will be discarded.
func (wp *WorkerPool) SubmitWork(task func()) {
	if !wp.pool.IsClosed() {
		_ = wp.pool.Submit(task)
	}
}

// ReStart restarts the worker pool
func (wp *WorkerPool) ReStart() {
	if wp.pool.IsClosed() {
		wp.pool.Reboot()
	}
}

// IsClosed indicates whether the pool is closed.
func (wp *WorkerPool) IsClosed() bool {
	return wp.pool != nil && wp.pool.IsClosed()
}

// logger implements the ants.Logger interface by delegating log messages
// to a provided structured logger (log.Logger) with support for log levels.
//
// It maps the ants.Logger Printf calls to the appropriate log level methods
// based on the current log level of the underlying logger.
//
// This allows integration of ants' internal logging with a custom logging
// system that supports different log levels.
//
// Example:
//
//	l := newLogger(myCustomLogger)
//	pool, _ := ants.NewPool(10, ants.WithLogger(l))
type logger struct {
	lg log.Logger
}

// Ensure logger implements the ants.Logger interface at compile time.
var _ ants.Logger = (*logger)(nil)

// newLogger creates a new ants-compatible logger using the given log.Logger.
func newLogger(lg log.Logger) *logger {
	return &logger{lg: lg}
}

// Printf implements the ants.Logger interface. It routes the formatted log message
// to the appropriate log level method of the underlying log.Logger based on its
// current log level.
func (l logger) Printf(format string, args ...any) {
	switch l.lg.LogLevel() {
	case log.DebugLevel:
		l.lg.Debugf(format, args...)
	case log.ErrorLevel:
		l.lg.Errorf(format, args...)
	case log.InfoLevel:
		l.lg.Infof(format, args...)
	case log.WarningLevel:
		l.lg.Warnf(format, args...)
	case log.PanicLevel:
		l.lg.Panicf(format, args...)
	case log.FatalLevel:
		l.lg.Fatalf(format, args...)
	default:
		l.lg.Debugf(format, args...)
	}
}
