package http

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/http2"
)

func TestClient(t *testing.T) {
	cl := Client()
	assert.IsType(t, new(http.Client), cl)
	assert.IsType(t, new(http2.Transport), cl.Transport)
	tr := cl.Transport.(*http2.Transport)
	assert.True(t, tr.AllowHTTP)
	assert.Equal(t, 30*time.Second, tr.PingTimeout)
	assert.Equal(t, 30*time.Second, tr.ReadIdleTimeout)
}

func TestURL(t *testing.T) {
	host := "localhost"
	port := 123

	url := URL(host, port)
	assert.Equal(t, "http://localhost:123", url)
}
