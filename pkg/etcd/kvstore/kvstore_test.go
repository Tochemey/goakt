package kvstore

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/coreos/etcd/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/etcd/embed"
	"github.com/travisjeffery/go-dynaport"
)

func TestKVStore(t *testing.T) {
	assert.NoError(t, os.RemoveAll("test"))
	// create a context
	ctx := context.TODO()

	// let us generate two ports
	ports := dynaport.Get(2)
	clientsPort := ports[0]
	peersPort := ports[1]

	// create the various URLs and cluster name
	clientURLs := types.MustNewURLs([]string{fmt.Sprintf("http://0.0.0.0:%d", clientsPort)})
	peerURLs := types.MustNewURLs([]string{fmt.Sprintf("http://0.0.0.0:%d", peersPort)})
	endpoints := clientURLs
	clusterName := "test"
	datadir := "test"

	logger := log.DefaultLogger

	// create an instance of the config
	config := embed.NewConfig(clusterName, clientURLs, peerURLs, endpoints,
		embed.WithDataDir(datadir),
		embed.WithLogger(logger))

	// create an instance of embed
	embed := embed.NewNode(config)

	// start the embed server
	require.NoError(t, embed.Start())

	// create an instance of KVStore
	store, err := New(&Config{
		logger:    logger,
		endPoints: endpoints.StringSlice(),
		client:    embed.Client(),
	})
	require.NoError(t, err)
	require.NotNil(t, store)

	// let us set some value and get value
	key := "hello"
	val := "world"
	resp, err := store.SetValue(ctx, key, val)
	assert.NoError(t, err)
	assert.NotNil(t, resp)

	// let us fetch some value
	getResponse, err := store.GetValue(ctx, key)
	assert.Nil(t, err)
	assert.NoError(t, err)
	assert.Equal(t, "world", string(getResponse.Kvs[0].Value))

	// let us remove the key
	delResp, err := store.Delete(ctx, key)
	assert.Nil(t, err)
	assert.NoError(t, err)
	assert.NotNil(t, delResp)
	assert.EqualValues(t, 1, delResp.Deleted)

	// wait for some time to sync
	time.Sleep(time.Second)

	getResponse, err = store.GetValue(ctx, key)
	assert.Nil(t, err)
	assert.NoError(t, err)
	assert.Zero(t, getResponse.Count)

	// stop the store
	assert.NoError(t, store.Shutdown())
	// stop the embed server
	assert.NoError(t, embed.Stop())
	assert.NoError(t, os.RemoveAll("test"))
}
