package discovery

import (
	"errors"
	"fmt"
)

// Meta represents the meta information to pass to the discovery engine
type Meta map[string]any

// NewMeta initializes meta
func NewMeta() Meta {
	return Meta{}
}

// GetString returns the string value of a given key which value is a string
// If the key value is not a string then an error is return
func (m Meta) GetString(key string) (string, error) {
	// let us check whether the given key is in the map
	val, ok := m[key]
	if !ok {
		return "", fmt.Errorf("key=%s not found", key)
	}
	// let us check the type of val
	switch x := val.(type) {
	case string:
		return x, nil
	default:
		return "", errors.New("the key value is not a string")
	}
}

// GetInt returns the int value of a given key which value is an integer
// If the key value is not an integer then an error is return
func (m Meta) GetInt(key string) (int, error) {
	// let us check whether the given key is in the map
	val, ok := m[key]
	if !ok {
		return -1, fmt.Errorf("key=%s not found", key)
	}
	// let us check the type of val
	switch x := val.(type) {
	case int:
		return x, nil
	default:
		return -1, errors.New("the key value is not an integer")
	}
}

// GetBool returns the int value of a given key which value is a boolean
// If the key value is not a boolean then an error is return
func (m Meta) GetBool(key string) (*bool, error) {
	// let us check whether the given key is in the map
	val, ok := m[key]
	if !ok {
		return nil, fmt.Errorf("key=%s not found", key)
	}
	// let us check the type of val
	switch x := val.(type) {
	case bool:
		return &x, nil
	default:
		return nil, errors.New("the key value is not an integer")
	}
}

// GetMapString returns the map of string value of a given key which value is a map of string
// Map of string means that the map key value pair are both string
func (m Meta) GetMapString(key string) (map[string]string, error) {
	// let us check whether the given key is in the map
	val, ok := m[key]
	if !ok {
		return nil, fmt.Errorf("key=%s not found", key)
	}
	// assert the type of val
	switch x := val.(type) {
	case map[string]string:
		return x, nil
	default:
		return nil, errors.New("the key value is not a map[string]string")
	}
}
