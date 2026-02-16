# TLS Configuration

Transport Layer Security (TLS) provides encryption and authentication for remoting communication in GoAkt. This guide covers how to configure and use TLS for secure actor-to-actor communication across networks.

## Overview

TLS in GoAkt:

- **Encrypts** all remoting traffic between actor systems
- **Authenticates** nodes using certificates
- **Supports mTLS**: Mutual TLS for bidirectional authentication
- **Integrates** with existing PKI infrastructure
- **Requires shared root CA**: **All nodes must use certificates signed by the same root Certificate Authority** - this is a fundamental requirement

**Critical Requirement**: TLS in GoAkt will **only work if all communicating nodes share the same root CA**. This cannot be emphasized enough - nodes using different CAs or self-signed certificates from different authorities will fail to establish connections.

## When to Use TLS

### Production Requirements

Use TLS when:

- **Communicating over untrusted networks**: Internet, shared networks, cloud environments
- **Compliance requirements**: PCI-DSS, HIPAA, SOC 2, or other security standards
- **Sensitive data**: Messages contain PII, financial data, or confidential information
- **Multi-tenant environments**: Isolating traffic between different tenants or services
- **Zero-trust architectures**: Verifying identity of all communicating parties

### Development vs Production

**Development:** TLS is optional

```go
system, _ := actor.NewActorSystem(
    "dev-system",
    actor.WithRemote("localhost", 3321), // No TLS
)
```

**Production:** TLS is strongly recommended

```go
system, _ := actor.NewActorSystem(
    "prod-system",
    actor.WithRemote(remotingConfig),
    actor.WithTLS(tlsInfo), // TLS enabled
)
```

## TLS Configuration

### Critical Requirement: Shared Root CA

**IMPORTANT**: For TLS to work properly between GoAkt nodes, **all nodes must share the same root Certificate Authority (CA)**. This is a fundamental requirement:

- **Both ServerConfig and ClientConfig** must reference the same root CA
- **All cluster nodes** must use certificates signed by the same CA
- **Communication will fail** if nodes use different CAs or don't trust each other's CA

The shared root CA enables:

1. **Server verification**: Clients validate that servers are trusted
2. **Client verification** (mTLS): Servers validate that clients are trusted
3. **Mutual authentication**: Both parties prove their identity
4. **Trust establishment**: All nodes in the cluster can securely communicate

### Required Components

GoAkt requires both server and client TLS configurations:

```go
import (
    "crypto/tls"
    gtls "github.com/tochemey/goakt/v3/tls"
)

tlsInfo := &gtls.Info{
    ServerConfig: serverTLSConfig, // Required - must trust the shared root CA
    ClientConfig: clientTLSConfig, // Required - must trust the shared root CA
}
```

### Why Both Configs?

- **ServerConfig**: Used when this node accepts incoming remoting connections
- **ClientConfig**: Used when this node initiates connections to other nodes
- In a cluster, every node acts as both client and server
- **Both configs must reference the same root CA** to enable bidirectional trust

## Creating TLS Certificates

### Using OpenSSL

**Important**: Create the root CA certificate **first** and use it to sign all node certificates. All nodes must use certificates signed by this same CA.

#### Generate Certificate Authority (CA)

**This CA will be shared across all nodes in your deployment:**

```bash
# Generate CA private key
openssl genrsa -out ca.key 4096

# Generate CA certificate (valid for 10 years)
# This is the root CA that ALL nodes will trust
openssl req -new -x509 -days 3650 -key ca.key -out ca.cert \
    -subj "/C=US/ST=State/L=City/O=Organization/CN=GoAkt CA"
```

#### Generate Server Certificate

**Sign this certificate with the shared root CA:**

```bash
# Generate server private key
openssl genrsa -out server.key 4096

# Generate server certificate signing request
openssl req -new -key server.key -out server.csr \
    -subj "/C=US/ST=State/L=City/O=Organization/CN=node1.example.com"

# Sign server certificate with the SHARED ROOT CA
# This CA must be the same one used for all nodes
openssl x509 -req -in server.csr -CA ca.cert -CAkey ca.key \
    -CAcreateserial -out server.cert -days 365
```

#### Generate Client Certificate (for mTLS)

**Sign this certificate with the same shared root CA:**

```bash
# Generate client private key
openssl genrsa -out client.key 4096

# Generate client certificate signing request
openssl req -new -key client.key -out client.csr \
    -subj "/C=US/ST=State/L=City/O=Organization/CN=client"

# Sign client certificate with the SAME SHARED ROOT CA
# Critical: Use the same ca.cert and ca.key as server certificates
openssl x509 -req -in client.csr -CA ca.cert -CAkey ca.key \
    -CAcreateserial -out client.cert -days 365
```

### Using Go Code

Generate certificates programmatically:

```go
package main

import (
    "crypto/rand"
    "crypto/rsa"
    "crypto/x509"
    "crypto/x509/pkix"
    "encoding/pem"
    "math/big"
    "os"
    "time"
)

func generateCA() (*x509.Certificate, *rsa.PrivateKey, error) {
    ca := &x509.Certificate{
        SerialNumber: big.NewInt(2023),
        Subject: pkix.Name{
            Organization: []string{"GoAkt"},
            CommonName:   "GoAkt CA",
        },
        NotBefore:             time.Now(),
        NotAfter:              time.Now().AddDate(10, 0, 0),
        IsCA:                  true,
        KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
        BasicConstraintsValid: true,
    }

    caPrivKey, _ := rsa.GenerateKey(rand.Reader, 4096)
    caBytes, _ := x509.CreateCertificate(rand.Reader, ca, ca, &caPrivKey.PublicKey, caPrivKey)

    return x509.ParseCertificate(caBytes)
}

func generateServerCert(ca *x509.Certificate, caPrivKey *rsa.PrivateKey, host string) ([]byte, []byte, error) {
    cert := &x509.Certificate{
        SerialNumber: big.NewInt(2024),
        Subject: pkix.Name{
            Organization: []string{"GoAkt"},
            CommonName:   host,
        },
        DNSNames:    []string{host},
        NotBefore:   time.Now(),
        NotAfter:    time.Now().AddDate(1, 0, 0),
        KeyUsage:    x509.KeyUsageDigitalSignature,
        ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
    }

    certPrivKey, _ := rsa.GenerateKey(rand.Reader, 4096)
    certBytes, _ := x509.CreateCertificate(rand.Reader, cert, ca, &certPrivKey.PublicKey, caPrivKey)

    certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certBytes})
    certPrivKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(certPrivKey)})

    return certPEM, certPrivKeyPEM, nil
}
```

## Configuring TLS in GoAkt

### Server-Side TLS Configuration

```go
import (
    "crypto/tls"
    "crypto/x509"
    "os"
)

// Load the SHARED ROOT CA certificate
// This CA must be the same across all nodes
caCert, err := os.ReadFile("ca.cert")
if err != nil {
    log.Fatal(err)
}

caCertPool := x509.NewCertPool()
caCertPool.AppendCertsFromPEM(caCert)

// Load server certificate and key (signed by the shared root CA)
serverCert, err := tls.LoadX509KeyPair("server.cert", "server.key")
if err != nil {
    log.Fatal(err)
}

// Server TLS configuration (with client authentication)
serverTLSConfig := &tls.Config{
    Certificates: []tls.Certificate{serverCert},
    ClientAuth:   tls.RequireAndVerifyClientCert, // Enforce mTLS
    ClientCAs:    caCertPool,                      // Verify clients against the SHARED ROOT CA
    MinVersion:   tls.VersionTLS12,
}
```

### Client-Side TLS Configuration

```go
// Load client certificate and key (for mTLS, signed by the shared root CA)
clientCert, err := tls.LoadX509KeyPair("client.cert", "client.key")
if err != nil {
    log.Fatal(err)
}

// Client TLS configuration
// CRITICAL: RootCAs must reference the SAME CA as the server's ClientCAs
clientTLSConfig := &tls.Config{
    Certificates:       []tls.Certificate{clientCert}, // Client cert for mTLS
    RootCAs:            caCertPool,                     // Verify server against the SHARED ROOT CA
    InsecureSkipVerify: false,                          // NEVER true in production
    MinVersion:         tls.VersionTLS12,
}
```

### Applying TLS to Actor System

```go
import gtls "github.com/tochemey/goakt/v3/tls"

tlsInfo := &gtls.Info{
    ServerConfig: serverTLSConfig,
    ClientConfig: clientTLSConfig,
}

system, err := actor.NewActorSystem(
    "secure-system",
    actor.WithRemote("0.0.0.0", 3321),
    actor.WithTLS(tlsInfo),
)
```

## Complete TLS Example

### Single Node with TLS

```go
package main

import (
    "context"
    "crypto/tls"
    "crypto/x509"
    "log"
    "os"

    "github.com/tochemey/goakt/v3/actor"
    gtls "github.com/tochemey/goakt/v3/tls"
)

func loadTLSConfig() (*gtls.Info, error) {
    // Load CA certificate
    caCert, err := os.ReadFile("certs/ca.cert")
    if err != nil {
        return nil, err
    }

    caCertPool := x509.NewCertPool()
    caCertPool.AppendCertsFromPEM(caCert)

    // Load server certificate
    serverCert, err := tls.LoadX509KeyPair("certs/server.cert", "certs/server.key")
    if err != nil {
        return nil, err
    }

    // Load client certificate
    clientCert, err := tls.LoadX509KeyPair("certs/client.cert", "certs/client.key")
    if err != nil {
        return nil, err
    }

    // Server configuration
    serverConfig := &tls.Config{
        Certificates: []tls.Certificate{serverCert},
        ClientAuth:   tls.RequireAndVerifyClientCert,
        ClientCAs:    caCertPool,
        MinVersion:   tls.VersionTLS12,
    }

    // Client configuration
    clientConfig := &tls.Config{
        Certificates:       []tls.Certificate{clientCert},
        RootCAs:            caCertPool,
        InsecureSkipVerify: false,
        MinVersion:         tls.VersionTLS12,
    }

    return &gtls.Info{
        ServerConfig: serverConfig,
        ClientConfig: clientConfig,
    }, nil
}

func main() {
    ctx := context.Background()

    // Load TLS configuration
    tlsInfo, err := loadTLSConfig()
    if err != nil {
        log.Fatal(err)
    }

    // Create actor system with TLS
    system, err := actor.NewActorSystem(
        "secure-system",
        actor.WithRemote("0.0.0.0", 3321),
        actor.WithTLS(tlsInfo),
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    log.Println("Secure actor system started")
    select {}
}
```

### Cluster with TLS

**CRITICAL**: All nodes in a cluster **must** use certificates signed by the **same shared root CA**. This is a non-negotiable requirement for cluster TLS to work:

```go
package main

import (
    "context"
    "log"

    "github.com/tochemey/goakt/v3/actor"
    "github.com/tochemey/goakt/v3/discovery/nats"
    gtls "github.com/tochemey/goakt/v3/tls"
)

func main() {
    ctx := context.Background()

    // Load TLS configuration
    // IMPORTANT: All nodes must use certificates signed by the SAME root CA
    tlsInfo, err := loadTLSConfig()
    if err != nil {
        log.Fatal(err)
    }

    // Discovery
    disco := nats.NewDiscovery(&nats.Config{
        NatsServer:    "nats://localhost:4222",
        NatsSubject:   "goakt.cluster",
        Host:          "localhost",
        DiscoveryPort: 3320,
    })

    // Cluster configuration
    clusterConfig := actor.NewClusterConfig().
        WithDiscovery(disco).
        WithDiscoveryPort(3320).
        WithPeersPort(3322).
        WithMinimumPeersQuorum(2).
        WithKinds(new(MyActor))

    // Create system with TLS
    system, err := actor.NewActorSystem(
        "secure-cluster",
        actor.WithCluster(clusterConfig),
        actor.WithRemote("localhost", 3321),
        actor.WithTLS(tlsInfo), // CRITICAL: All nodes must trust the same root CA
    )
    if err != nil {
        log.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer system.Stop(ctx)

    log.Println("Secure cluster node started")
    select {}
}
```

## Mutual TLS (mTLS)

Mutual TLS requires both server and client to present and verify certificates.

### Server Configuration for mTLS

```go
serverConfig := &tls.Config{
    Certificates: []tls.Certificate{serverCert},
    ClientAuth:   tls.RequireAndVerifyClientCert, // Enforce client cert
    ClientCAs:    caCertPool,                      // CA to verify clients
    MinVersion:   tls.VersionTLS12,
}
```

### Client Configuration for mTLS

```go
clientConfig := &tls.Config{
    Certificates:       []tls.Certificate{clientCert}, // Present client cert
    RootCAs:            caCertPool,                     // CA to verify server
    InsecureSkipVerify: false,                          // Always verify in production
    MinVersion:         tls.VersionTLS12,
}
```

### Client Authentication Modes

```go
// No client certificate required
ClientAuth: tls.NoClientCert

// Request client cert but don't enforce
ClientAuth: tls.RequestClientCert

// Require client cert but don't verify
ClientAuth: tls.RequireAnyClientCert

// Require and verify client cert (recommended for mTLS)
ClientAuth: tls.RequireAndVerifyClientCert
```

## TLS Best Practices

### Certificate Management

1. **Use a proper CA**: Don't use self-signed certificates in production
2. **Set appropriate validity periods**: Balance security (shorter) vs operational overhead (longer)
3. **Rotate certificates regularly**: Implement automated cert rotation
4. **Secure private keys**: Use file permissions (0600) and key management systems
5. **Monitor expiration**: Alert before certificates expire

### Configuration

1. **Use the same root CA everywhere**: **This is the most critical requirement** - all nodes must use certificates signed by the same root CA
2. **Enforce minimum TLS version**: Use `MinVersion: tls.VersionTLS12` or higher
3. **Use strong cipher suites**: Let Go defaults handle this (secure by default)
4. **Enable mTLS**: Use `RequireAndVerifyClientCert` for both-way authentication
5. **Never skip verification**: `InsecureSkipVerify: false` in production

### Operations

1. **Test TLS before production**: Verify handshake works between all nodes
2. **Monitor TLS errors**: Track handshake failures and certificate issues
3. **Document certificate locations**: Maintain clear documentation for cert paths
4. **Plan for cert renewal**: Implement zero-downtime cert updates
5. **Backup certificates and keys**: Securely store in version control or secret management

## Using cert-manager (Kubernetes)

### Install cert-manager

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.13.0/cert-manager.yaml
```

### Create ClusterIssuer

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: goakt-ca-issuer
spec:
  ca:
    secretName: goakt-ca-secret
```

### Request Certificates

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: goakt-node-cert
  namespace: default
spec:
  secretName: goakt-node-tls
  duration: 2160h # 90 days
  renewBefore: 360h # 15 days before expiry
  subject:
    organizations:
      - goakt
  commonName: goakt-node
  isCA: false
  privateKey:
    algorithm: RSA
    size: 2048
  usages:
    - server auth
    - client auth
  dnsNames:
    - goakt-cluster
    - goakt-cluster.default
    - goakt-cluster.default.svc
    - goakt-cluster.default.svc.cluster.local
    - "*.goakt-cluster.default.svc.cluster.local"
  issuerRef:
    name: goakt-ca-issuer
    kind: ClusterIssuer
```

### Mount Certificates in Pods

```yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: goakt-cluster
spec:
  template:
    spec:
      containers:
        - name: app
          volumeMounts:
            - name: tls-certs
              mountPath: /etc/tls
              readOnly: true
      volumes:
        - name: tls-certs
          secret:
            secretName: goakt-node-tls
```

### Load Certificates in Go

```go
func loadK8sTLSConfig() (*gtls.Info, error) {
    certPath := "/etc/tls"

    caCert, err := os.ReadFile(filepath.Join(certPath, "ca.crt"))
    if err != nil {
        return nil, err
    }

    caCertPool := x509.NewCertPool()
    caCertPool.AppendCertsFromPEM(caCert)

    cert, err := tls.LoadX509KeyPair(
        filepath.Join(certPath, "tls.crt"),
        filepath.Join(certPath, "tls.key"),
    )
    if err != nil {
        return nil, err
    }

    serverConfig := &tls.Config{
        Certificates: []tls.Certificate{cert},
        ClientAuth:   tls.RequireAndVerifyClientCert,
        ClientCAs:    caCertPool,
        MinVersion:   tls.VersionTLS12,
    }

    clientConfig := &tls.Config{
        Certificates:       []tls.Certificate{cert},
        RootCAs:            caCertPool,
        InsecureSkipVerify: false,
        MinVersion:         tls.VersionTLS12,
    }

    return &gtls.Info{
        ServerConfig: serverConfig,
        ClientConfig: clientConfig,
    }, nil
}
```

## Testing TLS Configuration

### Verify Certificates

```bash
# Check certificate details
openssl x509 -in server.cert -text -noout

# Verify certificate chain
openssl verify -CAfile ca.cert server.cert

# Test TLS connection
openssl s_client -connect localhost:3321 -CAfile ca.cert
```

### Test TLS in Go

```go
package main

import (
    "context"
    "crypto/tls"
    "log"
    "testing"

    "github.com/tochemey/goakt/v3/actor"
    gtls "github.com/tochemey/goakt/v3/tls"
)

func TestTLSConnection(t *testing.T) {
    ctx := context.Background()

    // Load TLS config
    tlsInfo, err := loadTLSConfig()
    if err != nil {
        t.Fatal(err)
    }

    // Start system with TLS
    system, err := actor.NewActorSystem(
        "tls-test",
        actor.WithRemote("localhost", 3321),
        actor.WithTLS(tlsInfo),
    )
    if err != nil {
        t.Fatal(err)
    }

    if err := system.Start(ctx); err != nil {
        t.Fatal(err)
    }
    defer system.Stop(ctx)

    // Spawn test actor
    pid, err := system.Spawn(ctx, "test-actor", new(TestActor))
    if err != nil {
        t.Fatal(err)
    }

    log.Printf("TLS test passed - actor spawned: %s", pid.Name())
}
```

## Advanced TLS Scenarios

### Different Certificates per Node

In a cluster, you can use **different server certificates for each node** as long as they're **all signed by the same root CA**. This is a common pattern for production deployments:

```go
// Node 1 - uses node1.cert/node1.key
tlsInfo1 := loadNodeTLSConfig("node1")

system1, _ := actor.NewActorSystem(
    "cluster",
    actor.WithRemote("node1.example.com", 3321),
    actor.WithTLS(tlsInfo1),
)

// Node 2 - uses node2.cert/node2.key
tlsInfo2 := loadNodeTLSConfig("node2")

system2, _ := actor.NewActorSystem(
    "cluster",
    actor.WithRemote("node2.example.com", 3321),
    actor.WithTLS(tlsInfo2),
)
```

**Key Point**: All nodes **must** trust the same root CA in both their `RootCAs` (client config) and `ClientCAs` (server config), but each node can have a unique server certificate as long as all certificates are signed by that shared root CA.

### Certificate Rotation

Implement zero-downtime certificate rotation:

```go
func rotateCertificates(system actor.ActorSystem, newTLSInfo *gtls.Info) error {
    // 1. Load new certificates
    // 2. Create new actor system with new certs
    // 3. Migrate actors to new system
    // 4. Stop old system
    // 5. Update DNS/load balancer to point to new system

    // This is application-specific and requires careful orchestration
    return nil
}
```

**Recommended approach:**

- Use cert-manager or similar tools for automatic renewal
- Roll out new certificates gradually (one node at a time)
- Monitor TLS handshake errors during rollout

### TLS with Let's Encrypt

```go
import (
    "crypto/tls"
    "golang.org/x/crypto/acme/autocert"
)

// Auto-certificate manager
certManager := &autocert.Manager{
    Prompt:     autocert.AcceptTOS,
    Cache:      autocert.DirCache("/etc/letsencrypt/cache"),
    HostPolicy: autocert.HostWhitelist("node.example.com"),
}

// TLS configuration using Let's Encrypt
serverConfig := &tls.Config{
    GetCertificate: certManager.GetCertificate,
    MinVersion:     tls.VersionTLS12,
}

// Note: Client config still needs to trust Let's Encrypt CA
clientConfig := &tls.Config{
    MinVersion: tls.VersionTLS12,
}

tlsInfo := &gtls.Info{
    ServerConfig: serverConfig,
    ClientConfig: clientConfig,
}
```

## Troubleshooting

### Handshake Failures

**Symptoms:**

- `tls: handshake failure` errors
- Cannot connect to remote nodes

**Common Causes:**

1. **CA mismatch** (MOST COMMON): Client and server don't trust the same root CA - this is the primary cause of TLS failures in GoAkt
2. **Expired certificates**: Check certificate validity dates
3. **Wrong certificate**: Server presenting wrong cert for the hostname
4. **Missing client cert**: mTLS requires client certificate
5. **TLS version mismatch**: MinVersion settings incompatible

**Solutions:**

```bash
# Verify certificate chain
openssl verify -CAfile ca.cert server.cert

# Check certificate dates
openssl x509 -in server.cert -noout -dates

# Test TLS connection
openssl s_client -connect host:port -CAfile ca.cert
```

### Certificate Verification Failed

**Symptoms:**

- `x509: certificate signed by unknown authority`
- Connection rejected during handshake

**Root Cause**: The client and server are not using the same root CA.

**Solutions:**

1. **Verify the same root CA is used everywhere**:
   - Client's `RootCAs` must include the shared root CA
   - Server's `ClientCAs` must include the same shared root CA
   - All node certificates must be signed by this same CA
2. **Check CA certificate format**: Verify CA certificate is in PEM format
3. **Compare CA fingerprints**: Ensure all nodes reference the exact same CA certificate

```bash
# Get CA fingerprint on each node to verify they match
openssl x509 -in ca.cert -noout -fingerprint
```

### Invalid Configuration Error

**Symptoms:**

- `ErrInvalidTLSConfiguration` when starting actor system

**Cause:**

- Either `ServerConfig` or `ClientConfig` is nil
- Both must be set when using `WithTLS()`

**Solution:**

```go
// Incorrect - missing ServerConfig
tlsInfo := &gtls.Info{
    ClientConfig: clientTLSConfig,
}

// Correct - both configs present
tlsInfo := &gtls.Info{
    ServerConfig: serverTLSConfig,
    ClientConfig: clientTLSConfig,
}
```

### InsecureSkipVerify in Production

**Never do this in production:**

```go
// DANGEROUS - disables certificate verification
clientConfig := &tls.Config{
    InsecureSkipVerify: true, // Vulnerable to MITM attacks!
}
```

**Only acceptable for:**

- Local development
- Internal testing
- Explicitly documented test environments

## TLS and Performance

### Handshake Overhead

TLS handshakes add latency to new connections:

- **First connection**: ~100-200ms for full handshake
- **Session resumption**: ~10-20ms for abbreviated handshake
- **Connection pooling**: Amortizes handshake cost

**Mitigation:**

- Enable connection pooling (default in GoAkt)
- Use session resumption (automatic in TLS 1.2+)
- Increase `WithRemotingIdleTimeout` to keep connections alive longer

### CPU Impact

TLS encryption/decryption uses CPU:

- **Modern CPUs**: Minimal impact with AES-NI hardware acceleration
- **High throughput**: May see 5-10% CPU increase
- **Consider**: Use larger machines or hardware acceleration if CPU becomes bottleneck

**Optimization:**

- Use TLS 1.3 for faster handshakes (set `MinVersion: tls.VersionTLS13`)
- Enable connection pooling and reuse
- Monitor CPU usage and scale accordingly

## Security Considerations

### Private Key Protection

```bash
# Secure file permissions
chmod 600 server.key client.key
chown app:app server.key client.key

# Use secret management
# - Kubernetes Secrets
# - HashiCorp Vault
# - AWS Secrets Manager
# - Azure Key Vault
```

### Certificate Validation

Always validate certificates in production:

```go
clientConfig := &tls.Config{
    RootCAs:            caCertPool,
    InsecureSkipVerify: false,        // Enforce validation
    ServerName:         "node.example.com", // Verify server identity
    MinVersion:         tls.VersionTLS12,
}
```

### Network Security

1. **Use firewalls**: Restrict remoting ports to known nodes
2. **Network segmentation**: Isolate actor system networks
3. **VPN/private networks**: Use VPCs or private links in cloud
4. **Monitor traffic**: Detect anomalous connections

## Next Steps

- [Remoting Overview](overview.md): Learn about remoting fundamentals
- [Context Propagation](context_propagation.md): Distributed tracing with TLS
- [Cluster Overview](../cluster/overview.md): Build clusters with secure remoting
