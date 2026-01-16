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

package mathx

import "testing"

func TestMax_Int(t *testing.T) {
	tests := []struct {
		name string
		a, b int
		want int
	}{
		{"a greater", 10, 3, 10},
		{"b greater", 2, 9, 9},
		{"equal", 7, 7, 7},
		{"negative", -1, -5, -1},
		{"mixed signs", -3, 4, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := max(tt.a, tt.b); got != tt.want {
				t.Fatalf("max(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestMax_Uint(t *testing.T) {
	tests := []struct {
		name string
		a, b uint
		want uint
	}{
		{"a greater", 10, 3, 10},
		{"b greater", 2, 9, 9},
		{"equal", 7, 7, 7},
		{"zero", 0, 5, 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := max(tt.a, tt.b); got != tt.want {
				t.Fatalf("max(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestMax_Float64(t *testing.T) {
	tests := []struct {
		name string
		a, b float64
		want float64
	}{
		{"a greater", 10.5, 3.2, 10.5},
		{"b greater", 2.1, 9.9, 9.9},
		{"equal", 7.25, 7.25, 7.25},
		{"negative", -1.1, -5.4, -1.1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := max(tt.a, tt.b); got != tt.want {
				t.Fatalf("max(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestMax_String(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want string
	}{
		{"lex a greater", "zebra", "apple", "zebra"},
		{"lex b greater", "apple", "zebra", "zebra"},
		{"equal", "same", "same", "same"},
		{"prefix", "app", "apple", "apple"}, // "apple" > "app"
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := max(tt.a, tt.b); got != tt.want {
				t.Fatalf("max(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestMax_CustomUnderlyingType(t *testing.T) {
	// Ensure the "~int" etc. constraints allow user-defined types.
	type MyInt int

	tests := []struct {
		name string
		a, b MyInt
		want MyInt
	}{
		{"a greater", MyInt(10), MyInt(3), MyInt(10)},
		{"b greater", MyInt(2), MyInt(9), MyInt(9)},
		{"equal", MyInt(7), MyInt(7), MyInt(7)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := max(tt.a, tt.b); got != tt.want {
				t.Fatalf("max(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
