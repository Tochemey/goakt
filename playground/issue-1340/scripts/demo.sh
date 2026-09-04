#!/usr/bin/env bash
# Self-validating kill and recovery measurement for issue #1340.
#
# Flow:
#   1. start the three nodes (node1 first, so it is the oldest member: the
#      cluster leader and the host of the relocatable singleton)
#   2. wait until both survivors report a streak of successful requests to the
#      singleton and to the grains
#   3. kill node1 with SIGKILL and record the time, and do not restart it
#   4. wait, then read the report of each survivor and print what it measured
#   5. assert on the delay of NodeLeft, on the outage of the singleton and the
#      grains, and on the convergence fallback having stayed out of it
#
# The run measures the framework defaults unless CONVERGENCE_TIMEOUT or
# NETWORK_PROFILE is set, which the nodes then apply to their cluster
# configuration. SCENARIO names the run in the output and in the summary;
# SUMMARY_FILE, when set, receives one JSON line per run.
set -euo pipefail

cd "$(dirname "$0")/.."

# shellcheck source=scripts/lib.sh
. ./scripts/lib.sh

# Ports the survivors' HTTP surface is published on.
NODE2_URL="http://127.0.0.1:18082"
NODE3_URL="http://127.0.0.1:18083"

# The node the demo kills.
VICTIM="node1"

# The warning the cluster logs when it gives up waiting for the state to
# converge on a departure and publishes NodeLeft anyway. The whole point of the
# measurement is that this never happens.
FALLBACK_MARKER="overdue NodeLeft"

# Bounds: how long the cluster may take to serve steady requests, and how long
# the measurement waits after the kill before it reads the reports.
READY_TIMEOUT_SECONDS=120
OBSERVATION_SECONDS=40

# Assertions, all counted from the kill.
MAX_NODE_LEFT_DELAY_MS=15000
MAX_SINGLETON_RECOVERY_MS=25000
MAX_GRAIN_OUTAGE_MS=25000

REPORT_DIR="$(mktemp -d)"

cleanup() {
  echo ""
  echo "==> tearing the cluster down"
  docker compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT

# label names the run: the name the caller gave it, or one built from the
# settings this run varies.
label() {
  if [ -n "${SCENARIO:-}" ]; then
    echo "${SCENARIO}"
    return
  fi

  local out=""

  if [ -n "${CONVERGENCE_TIMEOUT:-}" ]; then
    out="convergence-timeout=${CONVERGENCE_TIMEOUT}"
  fi

  if [ -n "${NETWORK_PROFILE:-}" ]; then
    if [ -n "${out}" ]; then
      out="${out},"
    fi

    out="${out}network-profile=${NETWORK_PROFILE}"
  fi

  echo "${out:-default}"
}

# after renders a report timestamp in milliseconds as its distance from the
# kill, or a dash when the report holds no such timestamp.
after() {
  if [ "$1" -gt 0 ]; then
    millis $(( $1 - KILL_MS ))
    return
  fi

  printf -- '-'
}

# delay is after() for the summary file: milliseconds, or null.
delay() {
  if [ "$1" -gt 0 ]; then
    echo $(( $1 - KILL_MS ))
    return
  fi

  echo null
}

# row prints one line of a survivor's table.
row() {
  if [ -z "$3" ]; then
    printf '  %-28s %s\n' "$1" "$2"
    return
  fi

  printf '  %-28s %-26s %s\n' "$1" "$2" "$3"
}

# fetch stores the report of a survivor and prints the file it wrote.
fetch() {
  local url=$1 name=$2
  local file="${REPORT_DIR}/${name}.json"

  curl -fsS "${url}/report" -o "${file}"
  echo "${file}"
}

# fallback_used reports whether a node had to publish NodeLeft on the bounded
# wait instead of on the cluster converging on the departure. The logs are read
# into a variable rather than piped, so that a match cannot cut the reader off
# mid-stream.
fallback_used() {
  local node=$1
  local logs

  logs=$(docker compose logs --no-color "${node}" 2>/dev/null || true)

  case "${logs}" in
    *"${FALLBACK_MARKER}"*) echo yes ;;
    *) echo no ;;
  esac
}

SCENARIO_NAME=$(label)

echo "==> scenario: ${SCENARIO_NAME} (CONVERGENCE_TIMEOUT=${CONVERGENCE_TIMEOUT:-unset}, NETWORK_PROFILE=${NETWORK_PROFILE:-unset})"
echo "==> starting the cluster (node1, then node2, then node3)"
docker compose up -d

echo "==> waiting for both survivors to serve steady requests (up to ${READY_TIMEOUT_SECONDS}s)"
READY=""

for _ in $(seq 1 "${READY_TIMEOUT_SECONDS}"); do
  if curl -fsS -o /dev/null "${NODE2_URL}/ready" 2>/dev/null && curl -fsS -o /dev/null "${NODE3_URL}/ready" 2>/dev/null; then
    READY=yes
    break
  fi

  sleep 1
done

if [ -z "${READY}" ]; then
  echo "FAIL: the survivors did not serve steady requests within ${READY_TIMEOUT_SECONDS}s"
  docker compose logs --tail=100 node1 node2 node3
  exit 1
fi

BEFORE=$(fetch "${NODE2_URL}" "node2-before")
SINGLETON_HOST=$(json_path "${BEFORE}" singleton_last_response)

if [ "${SINGLETON_HOST}" != "${VICTIM}" ]; then
  echo "FAIL: the singleton answers from '${SINGLETON_HOST}', not from ${VICTIM}: the kill would not exercise the issue"
  exit 1
fi

echo "==> the singleton answers from ${SINGLETON_HOST}; killing it"
KILL_MS=$(now_ms)
docker kill --signal KILL "${VICTIM}" >/dev/null
KILL_TIME=$(utc "${KILL_MS}")

echo "==> ${VICTIM} killed at ${KILL_TIME} (it is not restarted)"
echo "==> observing the survivors for ${OBSERVATION_SECONDS}s"
sleep "${OBSERVATION_SECONDS}"

FAILURES=0

for NODE in node2 node3; do
  case "${NODE}" in
    node2) URL="${NODE2_URL}" ;;
    node3) URL="${NODE3_URL}" ;;
  esac

  REPORT=$(fetch "${URL}" "${NODE}")

  CONVERGENCE=$(json_path "${REPORT}" config.convergence_timeout)
  PROFILE=$(json_path "${REPORT}" config.network_profile)
  NODE_LEFT=$(json_path "${REPORT}" node_left_at)
  NODE_LEFT_MS=$(json_path "${REPORT}" node_left_at_ms)
  NODE_LEFT_ADDRESS=$(json_path "${REPORT}" node_left_address)
  CONFIRMED=$(json_path "${REPORT}" node_left_confirmed_at)
  CONFIRMED_MS=$(json_path "${REPORT}" node_left_confirmed_at_ms)
  SINGLETON_FAILURE=$(json_path "${REPORT}" singleton_first_failure)
  SINGLETON_FAILURE_MS=$(json_path "${REPORT}" singleton_first_failure_ms)
  SINGLETON_RECOVERED=$(json_path "${REPORT}" singleton_recovered)
  SINGLETON_RECOVERED_MS=$(json_path "${REPORT}" singleton_recovered_ms)
  SINGLETON_HOST_NOW=$(json_path "${REPORT}" singleton_response_after_recovery)
  GRAIN_FAILURE=$(json_path "${REPORT}" grain_first_failure)
  GRAIN_FAILURE_MS=$(json_path "${REPORT}" grain_first_failure_ms)
  GRAIN_RECOVERED=$(json_path "${REPORT}" grain_recovered)
  GRAIN_RECOVERED_MS=$(json_path "${REPORT}" grain_recovered_ms)
  SINGLETON_OK=$(json_path "${REPORT}" singleton_successes)
  SINGLETON_KO=$(json_path "${REPORT}" singleton_failures)
  GRAIN_OK=$(json_path "${REPORT}" grain_successes)
  GRAIN_KO=$(json_path "${REPORT}" grain_failures)
  RELOCATION_FAILURES=$(json_path "${REPORT}" relocation_failures)
  FALLBACK=$(fallback_used "${NODE}")

  echo ""
  echo "${NODE}"
  row "cluster settings" "convergence ${CONVERGENCE}" "profile ${PROFILE}"
  row "kill (host clock)" "${KILL_TIME}" ""
  row "singleton first failure" "${SINGLETON_FAILURE:-none}" "$(after "${SINGLETON_FAILURE_MS}") after the kill"
  row "departure confirmed" "${CONFIRMED:-none}" "$(after "${CONFIRMED_MS}") after the kill"
  row "NodeLeft ${NODE_LEFT_ADDRESS}" "${NODE_LEFT:-none}" "$(after "${NODE_LEFT_MS}") after the kill"
  row "convergence wait" "$([ "${CONFIRMED_MS}" -gt 0 ] && millis $(( NODE_LEFT_MS - CONFIRMED_MS )) || echo '-')" "bounded by the convergence timeout"
  row "singleton recovered" "${SINGLETON_RECOVERED:-none}" "$(after "${SINGLETON_RECOVERED_MS}") after the kill"
  row "grain first failure" "${GRAIN_FAILURE:-none}" "$(after "${GRAIN_FAILURE_MS}") after the kill"
  row "grain recovered" "${GRAIN_RECOVERED:-none}" "$(after "${GRAIN_RECOVERED_MS}") after the kill"
  row "singleton outage" "$([ "${SINGLETON_RECOVERED_MS}" -gt 0 ] && millis $(( SINGLETON_RECOVERED_MS - SINGLETON_FAILURE_MS )) || echo 'not recovered')" ""
  row "grain outage" "$([ "${GRAIN_FAILURE_MS}" -gt 0 ] && { [ "${GRAIN_RECOVERED_MS}" -gt 0 ] && millis $(( GRAIN_RECOVERED_MS - GRAIN_FAILURE_MS )) || echo 'not recovered'; } || echo '0.000s (no failure)')" ""
  row "convergence fallback used" "${FALLBACK}" ""
  row "singleton host now" "${SINGLETON_HOST_NOW:-unchanged}" ""
  row "requests" "singleton ${SINGLETON_OK} ok / ${SINGLETON_KO} failed" "grains ${GRAIN_OK} ok / ${GRAIN_KO} failed"

  case "${NODE}" in
    node2)
      NODE2_LEFT=$(delay "${NODE_LEFT_MS}")
      NODE2_SINGLETON=$(delay "${SINGLETON_RECOVERED_MS}")
      NODE2_GRAIN=$(delay "${GRAIN_RECOVERED_MS}")
      NODE2_FALLBACK="${FALLBACK}"
      ;;
    node3)
      NODE3_LEFT=$(delay "${NODE_LEFT_MS}")
      NODE3_SINGLETON=$(delay "${SINGLETON_RECOVERED_MS}")
      NODE3_GRAIN=$(delay "${GRAIN_RECOVERED_MS}")
      NODE3_FALLBACK="${FALLBACK}"
      ;;
  esac

  if [ "${NODE_LEFT_MS}" -eq 0 ]; then
    echo "  FAIL: ${NODE} never reported NodeLeft for the killed node"
    FAILURES=$(( FAILURES + 1 ))
  elif [ $(( NODE_LEFT_MS - KILL_MS )) -gt "${MAX_NODE_LEFT_DELAY_MS}" ]; then
    echo "  FAIL: ${NODE} reported NodeLeft $(millis $(( NODE_LEFT_MS - KILL_MS ))) after the kill, more than $(millis ${MAX_NODE_LEFT_DELAY_MS})"
    FAILURES=$(( FAILURES + 1 ))
  fi

  if [ "${SINGLETON_RECOVERED_MS}" -eq 0 ]; then
    echo "  FAIL: ${NODE} never reached the singleton again"
    FAILURES=$(( FAILURES + 1 ))
  elif [ $(( SINGLETON_RECOVERED_MS - KILL_MS )) -gt "${MAX_SINGLETON_RECOVERY_MS}" ]; then
    echo "  FAIL: ${NODE} reached the singleton again $(millis $(( SINGLETON_RECOVERED_MS - KILL_MS ))) after the kill, more than $(millis ${MAX_SINGLETON_RECOVERY_MS})"
    FAILURES=$(( FAILURES + 1 ))
  fi

  if [ "${GRAIN_FAILURE_MS}" -gt 0 ]; then
    if [ "${GRAIN_RECOVERED_MS}" -eq 0 ]; then
      echo "  FAIL: ${NODE} never reached its grains again"
      FAILURES=$(( FAILURES + 1 ))
    elif [ $(( GRAIN_RECOVERED_MS - GRAIN_FAILURE_MS )) -gt "${MAX_GRAIN_OUTAGE_MS}" ]; then
      echo "  FAIL: ${NODE} grains were unavailable for $(millis $(( GRAIN_RECOVERED_MS - GRAIN_FAILURE_MS ))), more than $(millis ${MAX_GRAIN_OUTAGE_MS})"
      FAILURES=$(( FAILURES + 1 ))
    fi
  fi

  if [ "${RELOCATION_FAILURES}" -ne 0 ]; then
    echo "  FAIL: ${NODE} observed ${RELOCATION_FAILURES} relocation failures"
    FAILURES=$(( FAILURES + 1 ))
  fi

  if [ "${FALLBACK}" = "yes" ]; then
    echo "  FAIL: ${NODE} published NodeLeft on the bounded wait: the cluster never converged on the departure"
    FAILURES=$(( FAILURES + 1 ))
  fi
done

if [ -n "${SUMMARY_FILE:-}" ]; then
  printf '{"scenario":"%s","node2_node_left_ms":%s,"node3_node_left_ms":%s,"node2_singleton_ms":%s,"node3_singleton_ms":%s,"node2_grain_ms":%s,"node3_grain_ms":%s,"node2_fallback":"%s","node3_fallback":"%s"}\n' \
    "${SCENARIO_NAME}" \
    "${NODE2_LEFT}" "${NODE3_LEFT}" \
    "${NODE2_SINGLETON}" "${NODE3_SINGLETON}" \
    "${NODE2_GRAIN}" "${NODE3_GRAIN}" \
    "${NODE2_FALLBACK}" "${NODE3_FALLBACK}" >> "${SUMMARY_FILE}"
fi

echo ""
echo "reports: ${REPORT_DIR}"

if [ "${FAILURES}" -ne 0 ]; then
  echo ""
  echo "FAIL: ${FAILURES} assertion(s) failed in scenario ${SCENARIO_NAME}"
  exit 1
fi

echo ""
echo "PASS: scenario ${SCENARIO_NAME}: both survivors reported the departure within $(millis ${MAX_NODE_LEFT_DELAY_MS}) of the kill, reached the singleton again within $(millis ${MAX_SINGLETON_RECOVERY_MS}), and never used the convergence fallback"
