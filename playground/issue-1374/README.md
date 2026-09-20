# Issue 1374: a wildcard bind address listened on one guessed interface

`remote.NewConfig("0.0.0.0", port)`, the bind address every remoting example uses, used to be replaced at start with one guessed private IP, and that single value served both as the address the remoting server listens on and as the address advertised to peers. So the server listened on one interface only and refused connections through loopback or any other interface, a host with no private or public address could not start at all, and the IPv6 wildcard `::` was advertised verbatim.

## Expected vs actual

- **Expected**: a wildcard bind address listens on every interface, while peers keep being told the same concrete address as before (the first private IP, then the first public IP, then loopback). `::` is a wildcard too and serves IPv4 and IPv6 peers. A concrete bind address keeps listening on that address only.
- **Actual (before the fix)**: `0.0.0.0` listens on the guessed IP only, so a dial through `127.0.0.1` is refused; an offline host fails with `no private IP address found`; `::` is advertised as `::`.

## Run

```bash
go run ./playground/issue-1374
```

Prints `OK: 0.0.0.0 listens on every interface and advertises <ip>` and, when the host supports IPv6, `OK: :: listens for IPv4 and IPv6 peers and advertises <ip>` with the fix (the actor system keeps the configured wildcard as the listen address and only resolves the advertised one). Before the fix it exits with a `REPRO (broken)` fatal on the first dial through loopback.
