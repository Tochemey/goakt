package actor

import (
	cheaps "container/heap"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPassivationHeapPopClearsBackingArrayReference(t *testing.T) {
	entry := &passivationEntry{id: "retained"}
	queue := passivationHeap{entry}
	backing := queue[:cap(queue)]

	popped := cheaps.Pop(&queue)

	require.Same(t, entry, popped)
	require.Empty(t, queue)
	require.Nil(t, backing[0])
}
