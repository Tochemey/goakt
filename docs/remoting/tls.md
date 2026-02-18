# TLS

GoAkt supports TLS. When running in cluster mode, all nodes must share the same root Certificate Authority (CA) to ensure a successful handshake. 
TLS can be enabled when creating the actor system by using the option WithTLS method. This method allows you to configure both the TLS server and client settings.
TLS applies **only to remoting**: it encrypts and authenticates traffic between nodes. Local actor-to-actor messaging on the same node is not affected.

## How to enable TLS in GoAkt

Provide a `gtls.Info` with both **ServerConfig** and **ClientConfig** (standard `crypto/tls.Config`), and pass it when creating the actor system:

```go
import gtls "github.com/tochemey/goakt/v3/tls"

tlsInfo := &gtls.Info{
    ServerConfig: serverTLSConfig, // required
    ClientConfig: clientTLSConfig, // required
}

system, err := actor.NewActorSystem(
    "my-system",
    actor.WithRemote(...),
    actor.WithTLS(tlsInfo),
)
```

Both configs are required because a node both accepts incoming remoting connections (server) and initiates connections to other nodes (client). How you obtain or build `serverTLSConfig` and `clientTLSConfig` is up to you (PKI, OpenSSL, cert-manager, etc.).

## Shared root CA

**All nodes that communicate via remoting must use certificates signed by the same root Certificate Authority.** GoAkt does not define or manage your PKI; it only uses the `*tls.Config` you supply. If nodes use different root CAs (or self-signed certs from different issuers), TLS handshakes will fail and remoting between those nodes will not work. Ensure every node’s server and client configs trust the same root CA and that all node certificates are signed by that CA.
