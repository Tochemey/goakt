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

package actors

import (
	"context"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

// handleSignals handles os SIGINT or SIGTERM interrupts
func (x *actorSystem) handleSignals(ctx context.Context) {
	notifier := make(chan os.Signal, 1)
	signal.Notify(notifier, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-notifier
		x.logger.Infof("received an interrupt signal (%s) to shutdown", sig.String())
		x.stopping.Store(true)

		// start the shutdown process and wait for some time
		if err := x.shutdown(ctx); err != nil {
			x.logger.Error(err)
		}

		// wait for the shutdown to complete properly
		// if given that period the system cannot properly shut down then
		// we do have an issue
		time.Sleep(x.shutdownTimeout + time.Second)

		signal.Stop(notifier)
		pid := os.Getpid()
		// make sure if it is unix init process to exit
		if pid == 1 {
			os.Exit(0)
		}

		process, _ := os.FindProcess(pid)
		switch {
		case runtime.GOOS == "windows":
			_ = process.Kill()
		default:
			_ = process.Signal(sig.(syscall.Signal))
		}
	}()
}
