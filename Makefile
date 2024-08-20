.PHONY: bench
bench:
	cd bench && rm -f bench.txt && go test -run=^# -bench . -benchmem -count=30 -timeout=0 | tee bench.txt

.PHONY: bench-stats
bench-stats:
	cd bench && rm -f benchstat.txt && benchstat bench.txt | tee benchstat.txt


