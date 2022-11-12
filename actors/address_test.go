package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetAddress(t *testing.T) {
	actorSys := &actorSystem{
		nodeAddr: "hostA:9000",
		name:     "TestSystem",
	}
	kind := "User"
	id := "john"

	addr := GetAddress(actorSys, kind, id)
	expected := "goakt://TestSystem@hostA:9000/user/john"
	assert.Equal(t, expected, string(addr))
}
