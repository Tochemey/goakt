package actors

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
	"google.golang.org/protobuf/proto"
)

func TestActorBehavior(t *testing.T) {
	ctx := context.TODO()
	// create a Ping actor
	opts := []pidOption{
		withInitMaxRetries(1),
		withCustomLogger(log.DiscardLogger),
	}

	// create the actor path
	actor := &UserActor{}
	actorPath := NewPath("User", NewAddress(protocol, "sys", "host", 1))
	pid := newPID(ctx, actorPath, actor, opts...)
	require.NotNil(t, pid)

	// send Login
	var expected proto.Message
	success, err := Ask(ctx, pid, new(testpb.TestLogin), receivingTimeout)
	require.NoError(t, err)
	require.NotNil(t, success)
	expected = &testpb.TestLoginSuccess{}
	require.True(t, proto.Equal(expected, success))

	// ask for readiness
	ready, err := Ask(ctx, pid, new(testpb.TestReadiness), receivingTimeout)
	require.NoError(t, err)
	require.NotNil(t, ready)
	expected = &testpb.TestReady{}
	require.True(t, proto.Equal(expected, ready))

	// send a message to create account
	created, err := Ask(ctx, pid, new(testpb.CreateAccount), receivingTimeout)
	require.NoError(t, err)
	require.NotNil(t, created)
	expected = &testpb.AccountCreated{}
	require.True(t, proto.Equal(expected, created))

	// credit account
	credited, err := Ask(ctx, pid, new(testpb.CreditAccount), receivingTimeout)
	require.NoError(t, err)
	require.NotNil(t, credited)
	expected = &testpb.AccountCredited{}
	require.True(t, proto.Equal(expected, credited))

	// debit account
	debited, err := Ask(ctx, pid, new(testpb.DebitAccount), receivingTimeout)
	require.NoError(t, err)
	require.NotNil(t, debited)
	expected = &testpb.AccountDebited{}
	require.True(t, proto.Equal(expected, debited))

	// send bye
	err = Tell(ctx, pid, new(testpb.TestBye))
	require.NoError(t, err)

	time.Sleep(time.Second)
	assert.False(t, pid.IsRunning())
}
