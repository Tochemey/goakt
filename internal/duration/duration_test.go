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

package duration

import (
	"testing"
	"time"
)

func TestFormat(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Duration
		expected string
	}{
		{"zero", 0, "0s"},
		{"negative", -1 * time.Second, "0s"},
		{"seconds", 5 * time.Second, "5s"},
		{"minutes and seconds", 1*time.Minute + 30*time.Second, "1m 30s"},
		{"hours and minutes", 2*time.Hour + 15*time.Minute, "2h 15m"},
		{"days, hours, minutes", 1*24*time.Hour + 3*time.Hour + 5*time.Minute, "1d 3h 5m"},
		{"weeks and days", 2*7*24*time.Hour + 3*24*time.Hour, "2w 3d"},
		{"months, weeks, days", 2*30*24*time.Hour + 1*7*24*time.Hour + 2*24*time.Hour, "2mo 1w 2d"},
		{"years, months, days", 1*365*24*time.Hour + 2*30*24*time.Hour + 5*24*time.Hour, "1y 2mo 5d"},
		{"all units", 1*365*24*time.Hour + 2*30*24*time.Hour + 1*7*24*time.Hour + 3*24*time.Hour + 4*time.Hour + 5*time.Minute + 6*time.Second + 700*time.Millisecond + 800*time.Microsecond + 900*time.Nanosecond,
			"1y 2mo 1w 3d 4h 5m 6s 700ms 800us 900ns"},
		{"milliseconds", 1234 * time.Millisecond, "1s 234ms"},
		{"microseconds", 1500 * time.Microsecond, "1ms 500us"},
		{"nanoseconds", 123456789 * time.Nanosecond, "123ms 456us 789ns"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Format(tt.input)
			if got != tt.expected {
				t.Errorf("Format(%v) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
