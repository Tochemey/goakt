#!/usr/bin/env bash
# unit-test.sh runs the test suite as the CI shards, each in a pristine container, in parallel.
# It is invoked by `make unit-test`; scripts/test-shard.sh defines what each shard runs. Every
# run first discards whatever a previous run left behind: goakt-test containers and volumes,
# logs, status files and coverage profiles.
#
# Environment:
#   IMAGE        tools image to run in (default goakt-tools)
#   DOCKER_SOCK  Docker socket shared with the containers for testcontainers (default /var/run/docker.sock)
#   SHARD        run this shard only (default: all)
#   V            non-empty streams every test into the shard logs
set -uo pipefail

image="${IMAGE:-goakt-tools}"
sock="${DOCKER_SOCK:-/var/run/docker.sock}"
all_shards="memory core infra actor-0 actor-1 actor-2"
shards="${SHARD:-$all_shards}"
logs=.unit-test
volume="goakt-test-$$"
start=$(date +%s)

# elapsed prints the time since the run started as mm:ss.
elapsed() {
	local t=$(($(date +%s) - start))
	printf '%02d:%02d' $((t / 60)) $((t % 60))
}

# say prints a progress line stamped with the elapsed time.
say() { echo "[$(elapsed)] $*"; }

# pending prints the shards that have not written a status file yet.
pending() {
	for s in $shards; do
		[ -f "$logs/$s.status" ] || printf ' %s' "$s"
	done
}

# cleanup removes every goakt-test container and volume, whether from this run or one that was
# interrupted before it could clean up after itself.
cleanup() {
	for c in $(docker ps -aq --filter name=goakt-test-); do
		docker rm -f "$c" >/dev/null 2>&1
	done

	for v in $(docker volume ls -q --filter name=goakt-test-); do
		docker volume rm -f "$v" >/dev/null 2>&1
	done
}

# reset discards everything a previous run left behind before this one starts: containers,
# volumes, logs, status files and coverage profiles.
reset() {
	cleanup
	rm -rf "$logs" coverage-*.out
	mkdir -p "$logs"
}
trap cleanup EXIT INT TERM

# socket_group prints the group that owns the Docker socket on this host: the docker group on
# Linux, and 0 on Docker Desktop, where the socket inside the VM belongs to root.
socket_group() {
	stat -c %g "$sock" 2>/dev/null || echo 0
}

# run executes a command in a pristine container named goakt-test-<name>: the checkout is the
# only thing shared with the host, modules come from the image, the build cache lives on the
# run's volume, and the environment matches CI. The Docker socket is shared because some tests
# start consul and etcd containers; the root group and the socket's group grant access to it.
run() {
	local name=$1
	shift
	docker run --rm --name "goakt-test-$name" \
		--user "$(id -u):$(id -g)" \
		--group-add 0 \
		--group-add "$(socket_group)" \
		--add-host host.docker.internal:host-gateway \
		-v "$sock:/var/run/docker.sock" \
		-v "$volume:/cache" \
		-e HOME=/cache/home \
		-e GOCACHE=/cache/go-build \
		-e TZ=UTC \
		-e CGO_ENABLED=1 \
		-e GOTOOLCHAIN=local \
		-e GOFLAGS="-mod=readonly -tags=hashicorpmetrics" \
		-e TESTCONTAINERS_HOST_OVERRIDE=host.docker.internal \
		-e GOTEST_VERBOSE="${V:-}" \
		-v "$PWD:/src" \
		-w /src \
		"$image" "$@"
}

reset
docker volume create "$volume" >/dev/null

say "compiling every test binary with the race detector; this takes a few minutes"
(
	run build go test -race -exec true -run '^$' ./... >"$logs/build.log" 2>&1
	echo $? >"$logs/build.status"
) &
tick=0
while [ ! -f "$logs/build.status" ]; do
	sleep 5
	tick=$((tick + 1))
	[ $((tick % 6)) = 0 ] && [ ! -f "$logs/build.status" ] && say "still compiling"
done
if [ "$(cat "$logs/build.status")" != 0 ]; then
	tail -n 60 "$logs/build.log"
	exit 1
fi
say "compiled; running shards:$(pending) in parallel, logs in $logs/"

for s in $shards; do
	(
		t0=$(date +%s)
		run "$s" bash scripts/test-shard.sh "$s" >"$logs/$s.log" 2>&1
		rc=$?
		echo "$rc $(($(date +%s) - t0))" >"$logs/$s.status"
		if [ "$rc" = 0 ]; then say "PASS $s"; else say "FAIL $s, see $logs/$s.log"; fi
	) &
done
tick=0
while [ -n "$(pending)" ]; do
	sleep 5
	tick=$((tick + 1))
	running=$(pending)
	[ $((tick % 6)) = 0 ] && [ -n "$running" ] && say "running:$running"
done
wait

failed=0
for s in $shards; do
	read -r rc _ <"$logs/$s.status"
	[ "$rc" = 0 ] || failed=1
done
if [ "$failed" = 0 ]; then say "all shards passed, coverage in coverage-<shard>.out"; else say "some shards failed"; fi
exit "$failed"
