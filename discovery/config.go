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
func (m Config) GetInt(key string) (int, error) {
	// let us check whether the given key is in the map
	val, ok := m[key]
	if !ok {
		return 0, fmt.Errorf("key=%s not found", key)
	}
	// let us check the type of val
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
		// parse the string value
		res, err := strconv.ParseBool(val.(string))
		// return the possible error
		if err != nil {
			return nil, err
		}
		return &res, nil
	}
}

// GetMapString returns the map of string value of a given key which value is a map of string
// Map of string means that the map key value pair are both string
func (m Config) GetMapString(key string) (map[string]string, error) {
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
