package discovery

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	goaktpb "github.com/tochemey/goakt/internal/goaktpb/v1"
)

// Discovery helps discover other running actor system in a cloud environment
type Discovery interface {
	// Start the discovery engine
	Start(ctx context.Context, meta Meta) error
	// Nodes returns the list of Nodes at a given time
	Nodes(ctx context.Context) ([]*goaktpb.Node, error)
	// Watch returns event based upon node lifecycle
	Watch(ctx context.Context) (chan *goaktpb.Event, error)
	// Stop shutdown the discovery provider
	Stop() error
}

// Meta represents the meta information to pass to the discovery engine
type Meta map[string]any

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
