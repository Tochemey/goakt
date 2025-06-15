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
	"strings"
	"time"
)

// Format returns a human-readable string for a time.Duration,
// supporting years, months, weeks, days, hours, minutes, seconds, milliseconds, microseconds, and nanoseconds.
//
// Note: Months and years are approximated as 30 days and 365 days respectively.
//
// Examples:
//   - 400 * 24 * time.Hour => "1y 1m 5d"
//   - 90 * time.Second => "1m 30s"
//   - 2 * time.Hour + 15 * time.Minute => "2h 15m"
//   - 5 * time.Second => "5s"
func Format(d time.Duration) string {
	if d < 0 {
		return "0s"
	}

	const (
		nanosecond  = uint64(1)
		microsecond = 1000 * nanosecond
		millisecond = 1000 * microsecond
		second      = 1000 * millisecond
		minute      = 60 * second
		hour        = 60 * minute
		day         = 24 * hour
		week        = 7 * day
		month       = 30 * day  // Approximate month as 30 days
		year        = 365 * day // Approximate year as 365 days
	)

	units := []struct {
		name  string
		value uint64
	}{
		{"y", year},
		{"mo", month},
		{"w", week},
		{"d", day},
		{"h", hour},
		{"m", minute},
		{"s", second},
		{"ms", millisecond},
		{"us", microsecond},
		{"ns", nanosecond},
	}

	u := uint64(d)
	parts := []string{}

	for _, unit := range units {
		if u >= unit.value {
			val := u / unit.value
			parts = append(parts, formatUint(val)+unit.name)
			u -= val * unit.value
		}
	}

	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, " ")
}

// formatUint returns the string representation of a uint64.
func formatUint(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = '0' + byte(v%10)
		v /= 10
	}
	return string(buf[i:])
}
