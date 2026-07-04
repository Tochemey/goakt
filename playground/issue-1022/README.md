# Issue-1022

For more context check the github [issue-1022](https://github.com/Tochemey/goakt/issues/1022)

This repro starts a two-node cluster and activates 10000 grains with random placement from both nodes.
Each grain records its activation in a process-local set; if the same grain is activated more than once
in the same process, the program prints `grain <name> already exists`, which signals a duplicate
activation. `OnActivate` sleeps briefly to widen the race window between concurrent activations.

To check that the issue has been fixed, run two terminals from this directory and watch the output:

Terminal 1:
```shell
go run ./main.go
```

Terminal 2:
```shell
go run ./main.go 127.0.0.1:42222
```
