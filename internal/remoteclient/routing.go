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

package remoteclient

import (
	"path"
	"strings"

	"github.com/tochemey/goakt/v4/internal/internalpb"
)

const (
	fnvOffset32 = 2166136261
	fnvPrime32  = 16777619
)

// hierarchicalPath extracts the hierarchical actor path from a canonical
// GoAkt address. It excludes the scheme, system name, host, and port.
// Allocation-free: scans for '@' then the following '/'.
func hierarchicalPath(addr string) string {
	at := strings.IndexByte(addr, '@')
	if at < 0 {
		return ""
	}
	rest := addr[at+1:]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return ""
	}
	return rest[slash:]
}

// matchesLargeDestination reports whether path matches a configured large-lane
// destination pattern. Leading slashes are ignored so patterns do not need URI
// host or port information.
func matchesLargeDestination(actorPath string, patterns []string) bool {
	normalizedPath := strings.TrimPrefix(actorPath, "/")
	for _, pattern := range patterns {
		normalizedPattern := strings.TrimPrefix(pattern, "/")
		if normalizedPattern == "" {
			continue
		}

		if matched, err := path.Match(normalizedPattern, normalizedPath); err == nil && matched {
			return true
		}
	}
	return false
}

// routeUser selects the user traffic lane for receiverCanonical. Large
// destination patterns take priority; all other receivers are assigned to a
// stable ordinary lane by FNV-1a of the canonical address.
func routeUser(receiverCanonical string, ordinaryLanes uint32, patterns []string) (internalpb.LaneRole, uint32) {
	if matchesLargeDestination(hierarchicalPath(receiverCanonical), patterns) {
		return internalpb.LaneRole_LANE_ROLE_LARGE, 0
	}

	if ordinaryLanes == 0 {
		ordinaryLanes = 1
	}
	return internalpb.LaneRole_LANE_ROLE_ORDINARY, fnv32a(receiverCanonical) % ordinaryLanes
}

// fnv32a computes FNV-1a over s without heap allocation.
func fnv32a(s string) uint32 {
	h := uint32(fnvOffset32)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= fnvPrime32
	}
	return h
}
