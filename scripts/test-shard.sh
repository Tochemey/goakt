#!/usr/bin/env bash
# test-shard.sh runs one test shard inside the tools container, the way the CI matrix in
# .github/workflows does. Keep the shard definitions in step with that matrix; this script is
# for local runs only and is invoked by scripts/unit-test.sh.
#
# Usage: scripts/test-shard.sh <shard>
#
# Shards:
#   actor-0, actor-1, actor-2  the actor package, split round-robin over its sorted top-level tests
#   infra                      packages that bind ports or start cluster, remoting or discovery infrastructure
#   core                       every other package
#   memory                     the memory package, without the race detector
#
# GOTEST_VERBOSE=1 adds -v to the race shards.
set -euo pipefail

shard="${1:?usage: test-shard.sh <actor-0|actor-1|actor-2|infra|core|memory>}"
cover="-race -covermode=atomic -coverprofile=coverage-${shard}.out"
verbose="${GOTEST_VERBOSE:+-v}"

case "$shard" in
actor-[0-2])
	index="${shard#actor-}"
	tests=$(go test ./actor -list '^Test' | grep '^Test' | sort | awk -v i="$index" 'NR % 3 == i' | paste -sd'|' -)
	if [ -z "$tests" ]; then
		echo "actor test slice $index is empty" >&2
		exit 1
	fi
	exec go test $verbose -p 1 -timeout 30m -run "^(${tests})$" $cover ./actor/...
	;;
infra)
	exec go test $verbose -p 1 -timeout 30m $cover \
		./internal/cluster/... ./internal/net/... ./internal/remoteclient/... ./remote/... \
		./client/... ./testkit/... ./discovery/... ./datacenter/...
	;;
core)
	packages=$(go list ./... | grep -vE '/(actor|internal/cluster|internal/net|internal/remoteclient|remote|client|testkit|discovery|datacenter|goaktpb|mocks|internal/internalpb|bench|playground)(/|$)')
	exec go test $verbose -timeout 30m $cover $packages
	;;
memory)
	exec go test -count=1 -timeout 5m -v ./memory/...
	;;
*)
	echo "unknown shard: $shard" >&2
	exit 2
	;;
esac
