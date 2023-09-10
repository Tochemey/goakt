bench:
	cd bench && go test -bench=. -benchmem -benchtime 2s -count 5 -run=^#


