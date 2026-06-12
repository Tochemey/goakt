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

// Reproduction for https://github.com/Tochemey/goakt/issues/1201
//
// NewSlogFrom(logger, level) stored the level argument but never used it for
// filtering: Enabled delegated straight to the wrapped slog handler, so a
// GoAkt logger "floored" at WARNING still emitted every INFO record the
// shared handler allowed.
//
// This is the exact example from the issue description, made self-validating:
// the application logger runs at INFO, the GoAkt logger is floored at
// WARNING, and the INFO line that used to be printed must now be suppressed.
package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"strings"

	goaktlog "github.com/tochemey/goakt/v4/log"
)

func main() {
	// shared application logger at INFO; output is captured so the sample can
	// assert on it
	buf := new(bytes.Buffer)
	appLogger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// goakt logger floored at WARNING
	l := goaktlog.NewSlogFrom(appLogger, goaktlog.WarningLevel)

	// before the fix this line was printed despite the WARNING floor
	l.Info("this should be suppressed but is printed")

	if got := buf.String(); got != "" {
		fmt.Printf("FAIL: INFO record leaked past the WARNING floor: %s", got)
		os.Exit(1)
	}
	fmt.Println("INFO suppressed by the WARNING floor")

	// the floor must not suppress WARNING and above
	l.Warn("warning passes the floor")
	if !strings.Contains(buf.String(), "warning passes the floor") {
		fmt.Println("FAIL: WARNING record was filtered out; the floor must only suppress lower levels")
		os.Exit(1)
	}
	fmt.Println("WARNING passes the floor")

	fmt.Println("PASS: NewSlogFrom level floor is enforced (issue #1201 fixed)")
}
