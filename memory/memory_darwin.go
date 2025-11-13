//go:build darwin

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

package memory

import (
	"os/exec"
	"regexp"
	"strconv"
)

// Size returns the total memory of the system in bytes
// It uses the sysctl command to get the memory size
// and returns the value as an uint64.
func Size() (uint64, error) {
	s, err := sysctl("hw.memsize")
	if err != nil {
		return 0, nil
	}
	return s, nil
}

// Free returns the free memory of the system in bytes
// It uses the vm_stat command to get the memory size
// and returns the value as an uint64.
func Free() (uint64, error) {
	cmd := exec.Command("vm_stat")
	outBytes, err := cmd.Output()
	if err != nil {
		return 0, err
	}

	pageSizeRegex := regexp.MustCompile("page size of ([0-9]*) bytes")
	freePagesRegex := regexp.MustCompile(`Pages free: *([0-9]*)\.`)

	matches := pageSizeRegex.FindSubmatchIndex(outBytes)
	pageSize := uint64(4096)
	if len(matches) == 4 {
		pageSize, err = strconv.ParseUint(string(outBytes[matches[2]:matches[3]]), 10, 64)
		if err != nil {
			return 0, err
		}
	}

	matches = freePagesRegex.FindSubmatchIndex(outBytes)
	freePages := uint64(0)
	if len(matches) == 4 {
		freePages, err = strconv.ParseUint(string(outBytes[matches[2]:matches[3]]), 10, 64)
		if err != nil {
			return 0, err
		}
	}
	return freePages * pageSize, nil
}
