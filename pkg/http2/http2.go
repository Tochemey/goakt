package http2

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/http2"
)

// GetClient creates a http client use h2c
func GetClient() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
				// If you're also using this client for non-h2c traffic, you may want to
				// delegate to tls.Dial if the network isn't TCP or the addr isn't in an
				// allow-list.
				return net.Dial(network, addr)
			},
			PingTimeout:     30 * time.Second,
			ReadIdleTimeout: 30 * time.Second,
		},
	}
}

// GetURL create a http connection address
func GetURL(host string, port int) string {
	return fmt.Sprintf("https://%s:%d", host, port)
}
