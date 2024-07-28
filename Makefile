.PHONY: bench
bench:
	cd bench && rm -f bench.txt && go test -run=^# -cpu=1,2,4,8,16,32,64 -bench . -count=30 -timeout=0 | tee bench.txt

.PHONY: stats
stats:
	cd bench && rm -f benchstat.txt && benchstat bench.txt | tee benchstat.txt


