# Cluster Client

The GoAkt client package provides a lightweight library for external applications to interact with GoAkt actor system clusters without running a full actor system themselves. It enables remote actor communication, spawning, and management through a simple, load-balanced interface.

## Overview

The cluster client is designed for scenarios where you need to communicate with actors in a GoAkt cluster from:

- **External services**: Microservices, web servers, or other applications not running GoAkt
- **Client applications**: Command-line tools, monitoring systems, or administrative interfaces
- **Gateways and proxies**: API gateways that route requests to actor systems
- **Testing tools**: Integration test frameworks that interact with actor clusters

### Key Features

- **No actor system required**: Lightweight client without the overhead of running an actor system
- **Load balancing**: Automatic distribution of requests across cluster nodes
- **Connection pooling**: Efficient connection management to cluster nodes
- **Multiple balancing strategies**: Round-robin, random, or least-load balancing
- **Actor operations**: Spawn, tell, ask, stop, and lookup actors remotely
- **Grain support**: Interact with grain actors across the cluster
- **Thread-safe**: Safe for concurrent use from multiple goroutines

## Installation

```bash
go get github.com/tochemey/goakt/v3/client
```

## Quick Start

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/tochemey/goakt/v3/client"
)

func main() {
    ctx := context.Background()

    // Define cluster nodes (remoting addresses)
    nodes := []*client.Node{
        client.NewNode("node1.example.com:3321"),
        client.NewNode("node2.example.com:3321"),
        client.NewNode("node3.example.com:3321"),
    }

    // Create client
    cl, err := client.New(ctx, nodes)
    if err != nil {
        log.Fatal(err)
    }
    defer cl.Close()

    // Send a message to an actor
    if err := cl.Tell(ctx, "user-session-123", &MyMessage{}); err != nil {
        log.Fatal(err)
    }

    // Request-response with an actor
    response, err := cl.Ask(ctx, "user-session-123", &QueryRequest{}, 5*time.Second)
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Response: %v", response)
}
```

## Creating a Client

### Basic Client

Create a client with default settings (round-robin balancing):

```go
import "github.com/tochemey/goakt/v3/client"

ctx := context.Background()

nodes := []*client.Node{
    client.NewNode("node1:3321"),
    client.NewNode("node2:3321"),
    client.NewNode("node3:3321"),
}

cl, err := client.New(ctx, nodes)
if err != nil {
    log.Fatal(err)
}
defer cl.Close()
```

### Client with Custom Balancing

Specify a balancing strategy:

```go
cl, err := client.New(
    ctx,
    nodes,
    client.WithBalancerStrategy(client.LeastLoadStrategy),
)
```

### Client with Node Refresh

Enable automatic node health checking and weight updates:

```go
cl, err := client.New(
    ctx,
    nodes,
    client.WithBalancerStrategy(client.LeastLoadStrategy),
    client.WithRefresh(10 * time.Second), // Check node health every 10s
)
```

## Node Configuration

### Creating Nodes

A `Node` represents a cluster member with its remoting address:

```go
// Basic node
node := client.NewNode("node1.example.com:3321")

// Node with initial weight
node := client.NewNode(
    "node1.example.com:3321",
    client.WithWeight(100.0),
)

// Node with custom remoting instance
node := client.NewNode(
    "node1.example.com:3321",
    client.WithRemoting(myRemoting),
)
```

### Node Format

Nodes must be specified as `host:port` where the port is the **remoting port** (not discovery or peers port):

```go
// Correct - remoting port
node := client.NewNode("192.168.1.10:3321")

// Incorrect - discovery port
node := client.NewNode("192.168.1.10:3320") // Wrong!
```

## Configuration Options

### WithBalancerStrategy

Set the load balancing strategy:

```go
cl, err := client.New(
    ctx,
    nodes,
    client.WithBalancerStrategy(client.RoundRobinStrategy), // Default
)
```

**Available Strategies:**

| Strategy             | Description                   | Use Case                                   |
|----------------------|-------------------------------|--------------------------------------------|
| `RoundRobinStrategy` | Cycles through nodes evenly   | Uniform distribution, stateless operations |
| `RandomStrategy`     | Selects random nodes          | Quick distribution without state           |
| `LeastLoadStrategy`  | Selects node with lowest load | Heterogeneous clusters, load optimization  |

### WithRefresh

Enable periodic node health checking:

```go
cl, err := client.New(
    ctx,
    nodes,
    client.WithRefresh(10 * time.Second),
)
```

**Behavior:**
- Queries each node's metrics every interval
- Updates node weights based on current load
- Automatically adjusts load balancing based on node health
- Disabled by default (set to `-1`)

**When to use:**
- Heterogeneous clusters with varying node capacity
- When using `LeastLoadStrategy`
- For automatic failure detection

## Client Operations

### Kinds

List all registered actor kinds in the cluster:

```go
kinds, err := cl.Kinds(ctx)
if err != nil {
    log.Fatal(err)
}

for _, kind := range kinds {
    fmt.Printf("Available actor kind: %s\n", kind)
}
```

**Returns:** Slice of actor type names registered with `WithKinds()` in the cluster configuration.

### Spawn

Create and start a new actor on a cluster node:

```go
import "github.com/tochemey/goakt/v3/remote"

spawnRequest := &remote.SpawnRequest{
    Name: "user-session-456",
    Kind: "SessionActor",
}

err := cl.Spawn(ctx, spawnRequest)
if err != nil {
    log.Fatal(err)
}
```

**Features:**
- Uses default balancing strategy
- Actor is placed on an available node
- Actor kind must be registered on target node

#### SpawnRequest Fields

```go
spawnRequest := &remote.SpawnRequest{
    Name:                "actor-name",           // Required: unique actor name
    Kind:                "ActorType",            // Required: registered actor kind
    Singleton:           nil,                    // Optional: singleton configuration
    Relocatable:         true,                   // Whether actor can relocate
    Dependencies:        []interface{}{},        // Actor dependencies
    PassivationStrategy: strategy,               // Passivation configuration
    EnableStashing:      false,                  // Enable message stashing
    Role:                "worker",               // Target role for placement
    Reentrancy:          reentrancyConfig,       // Reentrancy configuration
    Supervisor:          supervisorConfig,       // Supervision strategy
}
```

### SpawnBalanced

Spawn an actor with a specific balancing strategy:

```go
err := cl.SpawnBalanced(
    ctx,
    spawnRequest,
    client.LeastLoadStrategy, // Override default strategy
)
```

**Use case:** When you need different balancing for specific actors (e.g., place compute-intensive actors on least-loaded nodes).

### Tell

Send a fire-and-forget message to an actor:

```go
message := &ProcessDataRequest{
    UserID: "user-123",
    Data:   []byte("payload"),
}

err := cl.Tell(ctx, "data-processor", message)
if err != nil {
    log.Fatal(err)
}
```

**Behavior:**
- Asynchronous (does not wait for response)
- Returns error if actor not found
- Message must be a protobuf message

### Ask

Send a request and wait for a response:

```go
request := &GetUserRequest{UserID: "user-123"}

response, err := cl.Ask(
    ctx,
    "user-manager",
    request,
    5 * time.Second, // Timeout
)
if err != nil {
    log.Fatal(err)
}

// Type assert the response
userResponse, ok := response.(*GetUserResponse)
if !ok {
    log.Fatal("unexpected response type")
}

fmt.Printf("User: %s\n", userResponse.Name)
```

**Behavior:**
- Blocks until response received or timeout
- Returns error if actor not found or timeout exceeded
- Response is a protobuf message

### Stop

Stop an actor gracefully:

```go
err := cl.Stop(ctx, "user-session-123")
if err != nil {
    log.Fatal(err)
}
```

**Behavior:**
- Triggers actor's `PostStop` lifecycle hook
- Returns no error if actor doesn't exist
- Actor is removed from cluster routing table

### ReSpawn

Restart an actor (stop then spawn):

```go
err := cl.ReSpawn(ctx, "data-processor")
if err != nil {
    log.Fatal(err)
}
```

**Behavior:**
- Stops the actor if running
- Spawns a new instance with same name
- State is not preserved unless actor implements persistence

### Whereis

Look up an actor's address:

```go
addr, err := cl.Whereis(ctx, "user-session-123")
if err != nil {
    log.Fatal(err)
}

fmt.Printf("Actor is at: %s:%d\n", addr.Host(), addr.Port())
```

**Returns:** Actor's current address (host and port of the node hosting it).

### Reinstate

Resume a suspended actor:

```go
err := cl.Reinstate(ctx, "suspended-actor")
if err != nil {
    log.Fatal(err)
}
```

**Behavior:**
- Resumes message processing for a suspended actor
- Returns no error if actor doesn't exist

## Grain Operations

### TellGrain

Send a fire-and-forget message to a grain:

```go
import "github.com/tochemey/goakt/v3/remote"

grainRequest := &remote.GrainRequest{
    Name: "user-grain-123",
    Kind: "UserGrain",
}

message := &UpdateUserRequest{Name: "John Doe"}

err := cl.TellGrain(ctx, grainRequest, message)
if err != nil {
    log.Fatal(err)
}
```

### AskGrain

Send a request to a grain and wait for response:

```go
grainRequest := &remote.GrainRequest{
    Name: "user-grain-123",
    Kind: "UserGrain",
}

request := &GetUserRequest{}

response, err := cl.AskGrain(
    ctx,
    grainRequest,
    request,
    5 * time.Second,
)
if err != nil {
    log.Fatal(err)
}
```

**Note:** Grain kinds must be registered on the cluster using `WithGrains()` in cluster configuration.

## Load Balancing Strategies

### Round-Robin Strategy

Distributes requests evenly across all nodes:

```go
cl, _ := client.New(
    ctx,
    nodes,
    client.WithBalancerStrategy(client.RoundRobinStrategy),
)
```

**Characteristics:**
- Cycles through nodes in order
- Each node receives equal number of requests
- No consideration for node load or health
- Good for homogeneous clusters

**Example sequence:**
```
Request 1 -> Node1
Request 2 -> Node2
Request 3 -> Node3
Request 4 -> Node1
Request 5 -> Node2
...
```

### Random Strategy

Selects a random node for each request:

```go
cl, _ := client.New(
    ctx,
    nodes,
    client.WithBalancerStrategy(client.RandomStrategy),
)
```

**Characteristics:**
- Random node selection
- Simple and fast
- Statistical distribution across nodes
- No state maintenance

**Use case:** Quick distribution without predictability requirements.

### Least-Load Strategy

Selects the node with the lowest current load:

```go
cl, _ := client.New(
    ctx,
    nodes,
    client.WithBalancerStrategy(client.LeastLoadStrategy),
    client.WithRefresh(10 * time.Second), // Recommended for load tracking
)
```

**Characteristics:**
- Considers node weight/load
- Routes to least-loaded node
- Requires periodic refresh for accurate load data
- Best for heterogeneous clusters

**Example:**
```
Node1: Weight 150 (high load)
Node2: Weight 50  (low load)
Node3: Weight 100 (medium load)

Next request -> Node2 (lowest weight)
```

## Complete Examples

### Web Service Integration

```go
package main

import (
    "context"
    "encoding/json"
    "log"
    "net/http"
    "time"

    "github.com/tochemey/goakt/v3/client"
)

var clusterClient *client.Client

func init() {
    ctx := context.Background()
    nodes := []*client.Node{
        client.NewNode("actor-node1:3321"),
        client.NewNode("actor-node2:3321"),
        client.NewNode("actor-node3:3321"),
    }

    var err error
    clusterClient, err = client.New(ctx, nodes)
    if err != nil {
        log.Fatal(err)
    }
}

func handleUserRequest(w http.ResponseWriter, r *http.Request) {
    userID := r.URL.Query().Get("user_id")

    // Ask actor for user data
    request := &GetUserRequest{UserID: userID}
    response, err := clusterClient.Ask(
        r.Context(),
        "user-manager",
        request,
        5*time.Second,
    )
    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    userResponse := response.(*GetUserResponse)
    json.NewEncoder(w).Encode(userResponse)
}

func main() {
    http.HandleFunc("/user", handleUserRequest)
    log.Fatal(http.ListenAndServe(":8080", nil))
}
```

### Command-Line Tool

```go
package main

import (
    "context"
    "flag"
    "fmt"
    "log"
    "time"

    "github.com/tochemey/goakt/v3/client"
)

func main() {
    nodes := flag.String("nodes", "localhost:3321", "Comma-separated list of cluster nodes")
    actorName := flag.String("actor", "", "Actor name to query")
    flag.Parse()

    ctx := context.Background()

    // Parse nodes
    nodeAddrs := strings.Split(*nodes, ",")
    var clusterNodes []*client.Node
    for _, addr := range nodeAddrs {
        clusterNodes = append(clusterNodes, client.NewNode(addr))
    }

    // Create client
    cl, err := client.New(ctx, clusterNodes)
    if err != nil {
        log.Fatal(err)
    }
    defer cl.Close()

    // Look up actor
    addr, err := cl.Whereis(ctx, *actorName)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("Actor '%s' is at: %s:%d\n", *actorName, addr.Host(), addr.Port())
}
```

### Batch Processing

```go
package main

import (
    "context"
    "log"
    "sync"
    "time"

    "github.com/tochemey/goakt/v3/client"
)

func main() {
    ctx := context.Background()

    nodes := []*client.Node{
        client.NewNode("node1:3321"),
        client.NewNode("node2:3321"),
        client.NewNode("node3:3321"),
    }

    cl, err := client.New(
        ctx,
        nodes,
        client.WithBalancerStrategy(client.RoundRobinStrategy),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer cl.Close()

    // Process batch of items
    items := []string{"item1", "item2", "item3", "item4", "item5"}

    var wg sync.WaitGroup
    for _, item := range items {
        wg.Add(1)
        go func(itemID string) {
            defer wg.Done()

            message := &ProcessItemRequest{ItemID: itemID}
            if err := cl.Tell(ctx, "batch-processor", message); err != nil {
                log.Printf("Failed to process %s: %v", itemID, err)
            }
        }(item)
    }

    wg.Wait()
    log.Println("Batch processing complete")
}
```

### Admin Tool with Multiple Strategies

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/tochemey/goakt/v3/client"
    "github.com/tochemey/goakt/v3/remote"
)

type AdminClient struct {
    client *client.Client
}

func NewAdminClient(nodes []string) (*AdminClient, error) {
    ctx := context.Background()

    var clusterNodes []*client.Node
    for _, addr := range nodes {
        clusterNodes = append(clusterNodes, client.NewNode(addr))
    }

    cl, err := client.New(
        ctx,
        clusterNodes,
        client.WithBalancerStrategy(client.LeastLoadStrategy),
        client.WithRefresh(5 * time.Second),
    )
    if err != nil {
        return nil, err
    }

    return &AdminClient{client: cl}, nil
}

func (a *AdminClient) ListActorKinds(ctx context.Context) ([]string, error) {
    return a.client.Kinds(ctx)
}

func (a *AdminClient) SpawnActor(ctx context.Context, name, kind string) error {
    request := &remote.SpawnRequest{
        Name: name,
        Kind: kind,
    }
    return a.client.Spawn(ctx, request)
}

func (a *AdminClient) StopActor(ctx context.Context, name string) error {
    return a.client.Stop(ctx, name)
}

func (a *AdminClient) FindActor(ctx context.Context, name string) (string, error) {
    addr, err := a.client.Whereis(ctx, name)
    if err != nil {
        return "", err
    }
    return fmt.Sprintf("%s:%d", addr.Host(), addr.Port()), nil
}

func (a *AdminClient) Close() {
    a.client.Close()
}

func main() {
    admin, err := NewAdminClient([]string{"node1:3321", "node2:3321"})
    if err != nil {
        log.Fatal(err)
    }
    defer admin.Close()

    ctx := context.Background()

    // List kinds
    kinds, _ := admin.ListActorKinds(ctx)
    fmt.Printf("Available kinds: %v\n", kinds)

    // Spawn actor
    admin.SpawnActor(ctx, "worker-1", "WorkerActor")

    // Find actor
    location, _ := admin.FindActor(ctx, "worker-1")
    fmt.Printf("Actor location: %s\n", location)

    // Stop actor
    admin.StopActor(ctx, "worker-1")
}
```

## Best Practices

### Client Lifecycle

1. **Reuse clients**: Create once, use for the lifetime of your application
2. **Close properly**: Always call `Close()` to release connections
3. **Handle errors**: Check for actor-not-found and timeout errors

```go
// Good - single client instance
var globalClient *client.Client

func init() {
    globalClient, _ = client.New(ctx, nodes)
}

func cleanup() {
    if globalClient != nil {
        globalClient.Close()
    }
}
```

### Load Balancing

1. **Choose appropriate strategy**: Match strategy to workload characteristics
2. **Enable refresh for LeastLoad**: Keep load information up-to-date
3. **Consider node health**: Use refresh to detect and avoid unhealthy nodes

### Error Handling

1. **Distinguish error types**: Handle not-found vs. timeout vs. network errors
2. **Implement retries**: Retry transient failures with backoff
3. **Circuit breakers**: Protect against cascading failures

```go
response, err := cl.Ask(ctx, actorName, request, timeout)
if err != nil {
    switch {
    case errors.Is(err, gerrors.NewErrActorNotFound(actorName)):
        // Actor doesn't exist - spawn it?
    case errors.Is(err, context.DeadlineExceeded):
        // Timeout - retry with longer timeout?
    default:
        // Network or other error - retry with backoff
    }
}
```

### Performance

1. **Concurrent requests**: Client is thread-safe; use concurrently
2. **Connection pooling**: Connections are pooled automatically
3. **Batch when possible**: Group related operations
4. **Set appropriate timeouts**: Balance responsiveness with reliability

## Troubleshooting

### Connection Failures

**Symptoms:**
- Cannot create client
- All operations fail with connection errors

**Solutions:**
- Verify node addresses are correct (remoting port, not discovery port)
- Check network connectivity to cluster nodes
- Ensure remoting is enabled on cluster nodes
- Verify firewall rules allow connections

### Actor Not Found

**Symptoms:**
- `Tell` and `Ask` return `ErrActorNotFound`
- `Whereis` fails

**Solutions:**
- Verify actor was spawned successfully
- Check actor name for typos
- Ensure actor hasn't been stopped
- Confirm you're querying the correct cluster

### Load Balancing Issues

**Symptoms:**
- All requests go to one node
- Uneven distribution

**Solutions:**
- Verify balancing strategy is set correctly
- Enable `WithRefresh` for LeastLoad strategy
- Check node weights are being updated
- Ensure all nodes are reachable

### Timeout Errors

**Symptoms:**
- `Ask` operations timeout frequently
- Slow responses

**Solutions:**
- Increase timeout duration
- Check network latency to cluster
- Verify cluster nodes aren't overloaded
- Consider using `Tell` for async operations

## Next Steps

- [Cluster Overview](overview.md): Learn about clustering fundamentals
- [Discovery](discovery.md): Configure cluster discovery
- [Remoting](../remoting/overview.md): Understand remoting configuration
- [Grains](../grains/overview.md): Learn about grain actors
