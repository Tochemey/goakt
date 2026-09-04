# Issue 1340: how fast a killed node is reported gone

Docker Compose reproduction for [issue #1340](https://github.com/Tochemey/goakt/issues/1340).

## Scenario

Three containers run the same binary and join one cluster through DNS discovery. Compose starts them one after the other, so `node1` is the oldest member: the cluster leader and the host of the relocatable singleton. The two survivors keep asking the singleton and thirty grains every 200ms with a 2 second timeout, and record when those requests fail and when they succeed again.

Once both survivors have answered ten requests in a row on both streams, the demo kills `node1` with `docker kill --signal KILL` and does not restart it. It then reads each survivor's report and measures, from the kill:

| Issue step | In the demo |
| --- | --- |
| Start a cluster of at least three nodes | `docker compose up`, one node at a time |
| Run a relocatable singleton on one node | `node1` spawns it once it sees the whole cluster |
| Send requests through the other members | The driver on `node2` and `node3`, every 200ms |
| Confirm the requests succeed | `GET /ready` answers after ten successes in a row per stream |
| Kill the singleton host, do not restart it | `docker kill --signal KILL node1`, `restart: "no"` |
| Measure the time to `NodeLeft` and to the singleton | The table the demo prints |

The cluster configuration is the one the issue was reported against: the discovery provider, the ports, the kinds, a 10 second bootstrap timeout and a peers quorum of one. Nothing tunes replication, the state sync interval, the balancer interval, the network profile or the convergence timeout, so the measurement describes what a stock cluster does.

## Expected vs actual

Before the fix, `NodeLeft` was not released by the cluster converging on the departure. A survivor compared its own observation of the departure with the convergence the cluster announced, and those two never lined up, so the event waited out the whole fallback of 30 seconds and arrived about 35 seconds after the kill. The singleton stayed unreachable for that whole window, because the leader only starts crash recovery once the departure is published.

After the fix, the survivors release `NodeLeft` as soon as the cluster converges on the departure, which follows failure confirmation within a second. On this three node cluster the measured numbers are:

| Measurement | Before | After |
| --- | --- | --- |
| `NodeLeft` after the kill | about 35s | 5.2s to 7.4s |
| Singleton reachable again after the kill | about 35s | 8.9s to 12.6s |
| Grains reachable again after the kill | about 35s | 3.5s to 6.0s |

The ranges come from seven runs of the default configuration on the same machine. The demo asserts the weaker bounds that hold with margin: `NodeLeft` within 15 seconds of the kill, the singleton reachable again within 25 seconds, the grain outage under 25 seconds, and no relocation failure.

Grains come back before the singleton because a grain is activated by the next request that names it, while the singleton is re-established by the leader's crash recovery once the departure is published.

## Layout

| File | Purpose |
| --- | --- |
| `main.go` | Wiring: DNS discovery, the cluster configuration, the singleton or the driver |
| `scenario.go` | The two optional cluster settings the scenarios vary |
| `matchmaker.go` | The relocatable singleton, the matchmaking service of the issue, which answers with the node hosting it |
| `worker.go` | The grain, which answers with its identity and the node hosting it |
| `driver.go` | The two request streams and the timeline they record |
| `recorder.go` | Event stream recorder for `NodeJoined`, `NodeLeft`, `LeaderChanged`, `RelocationStarted`, `RelocationFailed` |
| `server.go` | The HTTP surface and the JSON report |
| `docker-compose.yaml` | Three nodes and the DNS server on a private network |
| `coredns.Corefile` | The A records of the discovery domain |
| `Dockerfile` | Builds the reproduction from the local working tree (vendored deps) |
| `Dockerfile.dockerignore` | Keeps the local build caches out of the build context |
| `Makefile` | Image build, cluster lifecycle, the scenarios |
| `scripts/demo.sh` | The self-validating kill and measurement |
| `scripts/compare.sh` | Runs the three scenarios and prints the comparison table |
| `scripts/lib.sh` | Reading JSON and rendering durations, shared by both scripts |

## HTTP surface

Every node exposes the same endpoints on port 8080. The survivors publish theirs on the host, `node2` on 18082 and `node3` on 18083.

| Endpoint | Purpose |
| --- | --- |
| `GET /health` | Answers once the node joined the cluster. Compose starts the nodes one after the other on this probe, which makes `node1` the oldest member |
| `GET /ready` | Answers once the singleton is spawned (`node1`) or both request streams answered ten times in a row (survivors) |
| `GET /report` | The observed cluster events and the measured timeline, as JSON |

Every timestamp in the report is UTC, RFC3339 with milliseconds, and carries a companion field in epoch milliseconds so the demo can compare it with the time it killed the node without parsing dates. The report also carries a `config` object with the settings the run used, so a measurement is always read together with the configuration that produced it.

## Prerequisites

- Docker and Docker Compose
- `curl`
- `jq` or `python3`, whichever is present: the demo reads the JSON reports with it

## Run it

```bash
cd playground/issue-1340
make build   # builds the image from the local working tree
make demo
```

The demo is self-validating and always tears the cluster down when it ends. `make clean` also removes the image.

Two optional environment variables change the cluster configuration of every node, and both are unset in the run above, which is the configuration the issue was reported against:

| Variable | Effect |
| --- | --- |
| `CONVERGENCE_TIMEOUT` | A Go duration applied with `WithConvergenceTimeout` |
| `NETWORK_PROFILE` | `lan`, `local` or `wan`, applied with `WithNetworkProfile` |

## Measured output

From a passing run of the default scenario, starting at the point where the cluster is up:

```
==> waiting for both survivors to serve steady requests (up to 120s)
==> the singleton answers from node1; killing it
==> node1 killed at 2026-09-04T17:26:32.750Z (it is not restarted)
==> observing the survivors for 40s

node2
  cluster settings             convergence default        profile default
  kill (host clock)            2026-09-04T17:26:32.750Z
  singleton first failure      2026-09-04T17:26:34.867Z   2.117s after the kill
  departure confirmed          2026-09-04T17:26:38.584Z   5.834s after the kill
  NodeLeft 172.29.0.11:3320    2026-09-04T17:26:39.715Z   6.965s after the kill
  convergence wait             1.131s                     bounded by the convergence timeout
  singleton recovered          2026-09-04T17:26:44.722Z   11.972s after the kill
  grain first failure          2026-09-04T17:26:34.103Z   1.353s after the kill
  grain recovered              2026-09-04T17:26:36.714Z   3.964s after the kill
  singleton outage             9.855s
  grain outage                 2.611s
  convergence fallback used    no
  singleton host now           node2
  requests                     singleton 154 ok / 11 failed grains 183 ok / 5 failed

node3
  cluster settings             convergence default        profile default
  kill (host clock)            2026-09-04T17:26:32.750Z
  singleton first failure      2026-09-04T17:26:34.867Z   2.117s after the kill
  departure confirmed          2026-09-04T17:26:38.583Z   5.833s after the kill
  NodeLeft 172.29.0.11:3320    2026-09-04T17:26:39.716Z   6.966s after the kill
  convergence wait             1.133s                     bounded by the convergence timeout
  singleton recovered          2026-09-04T17:26:43.912Z   11.162s after the kill
  grain first failure          2026-09-04T17:26:34.103Z   1.353s after the kill
  grain recovered              2026-09-04T17:26:36.719Z   3.969s after the kill
  singleton outage             9.045s
  grain outage                 2.616s
  convergence fallback used    no
  singleton host now           node2
  requests                     singleton 158 ok / 7 failed grains 186 ok / 4 failed

reports: /var/folders/4m/gr659d112lg6fxk8h1f0xzb00000gn/T/tmp.J1XClpqYkp

PASS: scenario default: both survivors reported the departure within 15.000s of the kill, reached the singleton again within 25.000s, and never used the convergence fallback
```

The first failure lands one to two seconds after the kill, which is the request that was in flight when the node died plus the retry that followed it. `departure confirmed` and `convergence wait` split the delay into its two parts, and `singleton host now` shows where the singleton was re-established: `node2`, the new oldest member.

## How the fix works

A departure travels in two steps. The cluster first has to confirm that the node is gone, and that time is set by the network profile: the default profile confirms an abrupt failure on a small cluster in about six seconds, which is the bulk of the delay measured above. The survivors then wait for the cluster state to converge on the new member set before they publish `NodeLeft`, so the event describes a topology that already excludes the departed node.

The fix is in the second step. The cluster coordinator now announces the membership change itself, and every node places the departure against the announced convergences with that announcement rather than with its own local observation. The two line up, so the wait ends as soon as the state converges, normally within a second of the failure being confirmed. The wait is still bounded, by `WithConvergenceTimeout`, so a convergence that never completes cannot suppress the event: the bound is 10 seconds by default, down from the 30 seconds that used to be the only thing releasing the event on a survivor.

Both knobs are on the cluster configuration. `WithNetworkProfile` picks how aggressively peers are probed, and with it how quickly a failure is confirmed. `WithConvergenceTimeout` bounds the wait that follows.

## Why not a shorter convergence timeout

A departure moves through two clocks, and the convergence timeout is only the second one.

The first clock is failure confirmation: the cluster has to establish that the node is really gone and not just briefly slow. How long that takes is set by the network profile on the cluster configuration, and on the default profile it accounts for most of the delay measured above. The convergence timeout does not start until that clock has stopped, so lowering it cannot make a failure visible sooner. `make demo-short-timeout` measures exactly that: a bound of 5 seconds instead of 10 leaves `NodeLeft` where it was.

The second clock is the convergence that follows. The survivors wait for the cluster state to reflect the new member set before they publish `NodeLeft`, so the event describes a topology that already excludes the departed node. That wait is normally well under a second. It grows when the departed node was the oldest member, because the new membership then has to be worked out and sent around again, and it grows further on large clusters, on slow networks, and under load. The demo measures the two clocks apart: `departure confirmed` is the first one, `convergence wait` is the second, and the runs below, which kill the oldest member of a three node cluster, land between 30 milliseconds and 1.2 seconds on the second.

The timeout is the bound on that wait, not a target for it. Whenever it expires first, the event goes out while some node may still route to the departed one: relocation and actor lookups then run against a stale view, and the cluster logs a warning naming the overdue event. Ten seconds is about three times the slow case, which is the customary margin for a window whose job is to absorb the slow case rather than to cut it short. Five seconds leaves roughly half that margin, and every convergence that overruns it turns a correct wait into a stale publication.

Two things follow. Raise the timeout on large clusters and slow networks, where convergence itself is slower: a bound that is never reached costs nothing, since the event goes out as soon as the state converges. And to see failures sooner, change the network profile with `WithNetworkProfile`, which is the clock that decides when a departure becomes visible at all. `make demo-local-profile` measures that.

### Running the scenarios

| Command | What it changes |
| --- | --- |
| `make demo` | Nothing: the framework defaults |
| `make demo-short-timeout` | `CONVERGENCE_TIMEOUT=5s` |
| `make demo-local-profile` | `NETWORK_PROFILE=local` |
| `make compare` | Runs the three in sequence and prints the comparison table |

Every scenario asserts that the convergence fallback stayed out of it, so a run that only looks healthy because the event was published on the bound fails instead of passing quietly.

### Comparison

```
scenario        node2 NodeLeft   node3 NodeLeft   node2 singleton back   node3 singleton back   fallback used
default         6.965s           6.966s           11.972s                11.162s                no
short-timeout   5.938s           5.855s           11.122s                11.124s                no
local-profile   5.263s           5.261s           8.934s                 8.932s                 no
```

Every delay is counted from the kill, and no scenario ever published `NodeLeft` on the bound. The same run splits each of those delays in two, which is the whole argument in one table:

| Scenario | Departure confirmed | Convergence wait | `NodeLeft` |
| --- | --- | --- | --- |
| default | 5.834s | 1.131s | 6.965s |
| short-timeout | 5.829s | 0.109s | 5.938s |
| local-profile | 5.212s | 0.051s | 5.263s |

Confirmation is the same with a 5 second bound as with the default one, because the bound has no say in it. What moved between those two runs is the convergence, which varies on its own from a few tens of milliseconds to about a second, and the Local profile is the only setting that shortens confirmation itself.

One run is not enough to read a difference of a second, so the three scenarios were run four times. `NodeLeft` on node2 landed at:

| Scenario | Run 1 | Run 2 | Run 3 | Run 4 |
| --- | --- | --- | --- | --- |
| default | 5.282s | 6.958s | 6.964s | 6.965s |
| short-timeout | 6.929s | 5.286s | 5.277s | 5.938s |
| local-profile | 5.872s | 4.275s | 6.284s | 5.263s |

The spread inside one scenario is as wide as the difference between scenarios, because a crash only becomes visible on the cluster's next round of health checks and the kill falls at a different point of that cycle every time. Two things come out of it anyway. The convergence timeout does not move `NodeLeft`: the 5 second runs draw from the same band as the default ones, which is what the two clocks predict, and the fallback stayed unused in every one of them. The network profile does move it: the Local profile is the only setting that pulls the band down, by about a second on average at three nodes, and it is the lever to reach for when a departure has to be seen sooner.
