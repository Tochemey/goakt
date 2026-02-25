## How to run the benchmark

At the root the project just run:

`go test -run=^$ -bench=BenchmarkTell -cpu=4,8,16,32 -count=10 ./bench/`