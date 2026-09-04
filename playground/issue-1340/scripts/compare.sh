#!/usr/bin/env bash
# Runs the issue-1340 measurement three times and compares the scenarios:
#
#   default        the framework defaults, which is what the issue measured
#   short-timeout  a convergence timeout of 5s instead of the default 10s
#   local-profile  the Local network profile instead of the default LAN
#
# The comparison shows which of the two settings moves NodeLeft: the bound on
# the convergence wait does not, because it is not what a departure waits for,
# while the network profile does, because it sets how quickly a failure is
# confirmed.
set -euo pipefail

cd "$(dirname "$0")/.."

# shellcheck source=scripts/lib.sh
. ./scripts/lib.sh

SUMMARY_FILE="$(mktemp)"
export SUMMARY_FILE

LINE_FILE="$(mktemp)"

FAILURES=0

# run executes one scenario and keeps going when it fails, so the comparison is
# printed for every run that produced numbers.
run() {
  local name=$1 timeout=$2 profile=$3

  echo ""
  echo "=================== scenario ${name} ==================="

  if SCENARIO="${name}" CONVERGENCE_TIMEOUT="${timeout}" NETWORK_PROFILE="${profile}" ./scripts/demo.sh; then
    return
  fi

  FAILURES=$(( FAILURES + 1 ))
}

# seconds renders a summary value in milliseconds, or a dash when the run did
# not observe it.
seconds() {
  if [ -z "$1" ]; then
    printf -- '-'
    return
  fi

  millis "$1"
}

# fallback renders the two survivors' fallback verdicts as one column.
fallback() {
  if [ "$1" = "$2" ]; then
    echo "$1"
    return
  fi

  echo "node2=$1 node3=$2"
}

run default "" ""
run short-timeout 5s ""
run local-profile "" local

echo ""
echo "=================== comparison ==================="
printf '%-15s %-16s %-16s %-22s %-22s %s\n' "scenario" "node2 NodeLeft" "node3 NodeLeft" "node2 singleton back" "node3 singleton back" "fallback used"

while IFS= read -r line; do
  if [ -z "${line}" ]; then
    continue
  fi

  echo "${line}" > "${LINE_FILE}"

  printf '%-15s %-16s %-16s %-22s %-22s %s\n' \
    "$(json_path "${LINE_FILE}" scenario)" \
    "$(seconds "$(json_path "${LINE_FILE}" node2_node_left_ms)")" \
    "$(seconds "$(json_path "${LINE_FILE}" node3_node_left_ms)")" \
    "$(seconds "$(json_path "${LINE_FILE}" node2_singleton_ms)")" \
    "$(seconds "$(json_path "${LINE_FILE}" node3_singleton_ms)")" \
    "$(fallback "$(json_path "${LINE_FILE}" node2_fallback)" "$(json_path "${LINE_FILE}" node3_fallback)")"
done < "${SUMMARY_FILE}"

echo ""
echo "every delay is counted from the kill; summaries: ${SUMMARY_FILE}"

if [ "${FAILURES}" -ne 0 ]; then
  echo ""
  echo "FAIL: ${FAILURES} of 3 scenarios failed"
  exit 1
fi

echo ""
echo "PASS: the three scenarios ran and none of them used the convergence fallback"
