# Issue-1017

For more context check the github [issue-1017](https://github.com/Tochemey/goakt/issues/1017)
The main check is there that child actors are not relocated when their parent is relocated during a rebalance.


to check that the issue has been fixed just run the following command and watch the output:

`go run ./playground/issue-1017`