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
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
)

func TestHandleSignals(t *testing.T) {
	t.Run("With signals", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Skipping on windows")
		}
		// let us send some signals
		signals := []syscall.Signal{
			syscall.SIGINT,
			syscall.SIGTERM,
		}

		for _, sig := range signals {
			callCount := 0
			RegisterExitHook(func() error {
				callCount++
				return nil
			})

			sigCh := make(chan os.Signal, 2)
			signal.Notify(sigCh, sig)

			HandleSignals(log.DiscardLogger, nil)
			err := syscall.Kill(syscall.Getpid(), sig)
			require.NoError(t, err)

			// two signals are expected to be received
			waitForSignals(t, sigCh, sig)
			waitForSignals(t, sigCh, sig)

			require.EqualValues(t, 1, callCount)
			exitHook = nil
			signalLocker.Unlock()
		}
	})
	t.Run("With cancellation", func(t *testing.T) { // nolint
		cancelCh := make(chan types.Unit, 1)
		HandleSignals(log.DiscardLogger, cancelCh)
		close(cancelCh)
	})
}

func waitForSignals(t *testing.T, ch <-chan os.Signal, sig os.Signal) {
	select {
	case s := <-ch:
		require.Equal(t, s, sig)
	case <-time.After(1 * time.Second):
		t.Fatalf("timeout waiting for %v", sig)
	}
}
