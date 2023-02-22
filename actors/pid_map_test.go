package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPIDMap(t *testing.T) {
	// create the actor path
	actorPath := NewPath("Test", NewAddress(protocol, "TestSys", "host", 444))
	// create the PID
	actorRef := &pid{actorPath: actorPath}
	// create a new PID map
	pidMap := newPIDMap(5)
	// add to the map
	pidMap.Set(actorRef)
	// assert the length of the map
	assert.EqualValues(t, 1, pidMap.Len())
	// list the map
	lst := pidMap.List()
	assert.Len(t, lst, 1)
	// fetch the inserted pid back
	actual, ok := pidMap.Get(actorPath)
	assert.True(t, ok)
	assert.NotNil(t, actual)
	assert.IsType(t, new(pid), actual)
	// remove the pid from the map
	pidMap.Delete(actorPath)
	// list the map
	lst = pidMap.List()
	assert.Len(t, lst, 0)
}
