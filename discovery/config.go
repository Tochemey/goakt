/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package discovery

import (
	"errors"
	"fmt"
	"strconv"
)

// Config represents the meta information to pass to the discovery engine
type Config map[string]any

// NewConfig initializes meta
func NewConfig() Config {
	return Config{}
}

// GetString returns the string value of a given key which value is a string
// If the key value is not a string then an error is return
func (m Config) GetString(key string) (string, error) {
	val, ok := m[key]
	if !ok {
		return "", fmt.Errorf("key=%s not found", key)
	}

	switch x := val.(type) {
	case string:
		return x, nil
	default:
		return "", errors.New("the key value is not a string")
	}
}

// GetInt returns the int value of a given key which value is an integer
// If the key value is not an integer then an error is return
func (m Config) GetInt(key string) (int, error) {
	val, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("key=%s not found", key)
	}

	switch x := val.(type) {
	case int:
		return x, nil
	default:
		// maybe it is string integer
		return strconv.Atoi(val.(string))
	}
}

// GetBool returns the int value of a given key which value is a boolean
// If the key value is not a boolean then an error is return
func (m Config) GetBool(key string) (*bool, error) {
	val, ok := m[key]
	if !ok {
		return nil, fmt.Errorf("key=%s not found", key)
	}

	switch x := val.(type) {
	case bool:
		return &x, nil
	default:
		res, err := strconv.ParseBool(val.(string))
		if err != nil {
			return nil, err
		}
		return &res, nil
	}
}

// GetMapString returns the map of string value of a given key which value is a map of string
// Map of string means that the map key value pair are both string
func (m Config) GetMapString(key string) (map[string]string, error) {
	val, ok := m[key]
	if !ok {
		return nil, fmt.Errorf("key=%s not found", key)
	}

	switch x := val.(type) {
	case map[string]string:
		return x, nil
	default:
		return nil, errors.New("the key value is not a map[string]string")
	}
}
