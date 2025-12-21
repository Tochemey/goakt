# Issue-1022

For more context check the github [issue-1022](https://github.com/Tochemey/goakt/issues/1022)
The main check is there that child actors are not relocated when their parent is relocated during a rebalance.


to check that the issue has been fixed just run the following command and watch the output:

`go run ./playground/issue-1017`

Run two terminals:

Terminal 1:
```shell
go run ./playground/issue-1022
```

Terminal 2:
```shell
go run ./playground/issue-1022 127.0.0.1:42222
```