/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

package osutil

import (
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"

	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
)

var (
	signalLocker sync.Mutex
	hookLocker   sync.Mutex
	exitHook     ExitHook
)

// ExitHook is executed on receiving SIGTERM or SIGINT signal.
type ExitHook func() error

// RegisterExitHook registers the ExistHook in a thread-safe manner
func RegisterExitHook(hook ExitHook) {
	hookLocker.Lock()
	exitHook = hook
	hookLocker.Unlock()
}

// HandleSignals handles os SIGINT or SIGTERM signals
func HandleSignals(logger log.Logger, cancel <-chan types.Unit) {
	notifier := make(chan os.Signal, 1)
	signal.Notify(notifier, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case sig := <-notifier:
			hookLocker.Lock()
			hook := exitHook
			hookLocker.Unlock()

			// lock the exit process call
			signalLocker.Lock()
			logger.Infof("received an OS signal (%s) to shutdown", sig.String())

			if err := hook(); err != nil {
				logger.Error(err)
			}

			signal.Stop(notifier)
			if runtime.GOOS != "windows" {
				osid := syscall.Getpid()
				if osid == 1 {
					os.Exit(0)
				}
				_ = syscall.Kill(osid, sig.(syscall.Signal))
			}
		case <-cancel:
			return
		}
	}()
}
