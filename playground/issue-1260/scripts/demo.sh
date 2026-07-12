#!/usr/bin/env bash
# Self-validating crash-recovery demo for issue #1260.
#
# Flow:
#   1. wait for the 3-pod StatefulSet to be ready (pods started sequentially)
#   2. spawn 60 relocatable workers spread round-robin across the pods
#   3. verify all 60 exist in the cluster registry
#   4. crash a pod through its /crash endpoint (os.Exit: no graceful shutdown,
#      no PeerState snapshot, kill -9 semantics)
#   5. wait for the leader to publish RelocationDerived (registry-derived
#      recovery; the rebalance-epoch fallback alone can take ~30s)
#   6. verify all 60 workers exist again and no relocation failure was reported
set -euo pipefail

STS=issue1260
WORKERS=60
VICTIM=${STS}-1
SURVIVOR=${STS}-0
# recovery includes failure detection, epoch completion, quiescence, and the
# derivation scan whose registry reads must first drain connection attempts to
# the dead address; four minutes bounds the whole chain with margin
RECOVERY_TIMEOUT_SECONDS=240

http() {
  local pod=$1 path=$2
  kubectl exec "${pod}" -- wget -qO- "http://localhost:8080${path}"
}

echo "==> waiting for the StatefulSet to be ready"
kubectl rollout status "sts/${STS}" --timeout=300s

echo "==> spawning ${WORKERS} relocatable workers (round-robin placement)"
http "${SURVIVOR}" "/spawn?count=${WORKERS}"

echo "==> verifying all workers exist before the crash"
BEFORE=$(http "${SURVIVOR}" "/verify?count=${WORKERS}")
echo "${BEFORE}"

if ! echo "${BEFORE}" | grep -q '"complete":true'; then
  echo "FAIL: workers missing before the crash"
  exit 1
fi

echo "==> crashing ${VICTIM} (os.Exit, no graceful shutdown snapshot)"
http "${VICTIM}" "/crash" || true

# Delete the pod object right away so the crashed node is replaced at a NEW
# address instead of restarting in place at the same one. The force delete
# lands while the replacement container is still bootstrapping, before it
# could either shut down gracefully (its signal handler is only installed
# after startup completes) or purge its previous incarnation's registry
# records. This reproduces a pod being rescheduled after a crash, which is
# the registry-derived recovery scenario of issue #1260.
kubectl delete pod "${VICTIM}" --grace-period=0 --force --wait=false

echo "==> waiting for the leader to publish RelocationDerived (up to ${RECOVERY_TIMEOUT_SECONDS}s)"
DERIVED=""

for _ in $(seq 1 $((RECOVERY_TIMEOUT_SECONDS / 5))); do
  sleep 5
  EVENTS=$(http "${SURVIVOR}" "/events" || true)

  if echo "${EVENTS}" | grep -q 'RelocationDerived'; then
    DERIVED=yes
    break
  fi
done

if [ -z "${DERIVED}" ]; then
  echo "FAIL: no RelocationDerived event within ${RECOVERY_TIMEOUT_SECONDS}s"
  echo "events observed by ${SURVIVOR}:"
  http "${SURVIVOR}" "/events"
  exit 1
fi

echo "==> RelocationDerived observed; giving relocation a moment to finish"
sleep 10

# registry reads can transiently time out right after a crash while connection
# attempts to the dead address drain; retry the verification briefly
echo "==> verifying all workers exist after the crash"
AFTER=""

for _ in $(seq 1 12); do
  if AFTER=$(http "${SURVIVOR}" "/verify?count=${WORKERS}" 2>/dev/null); then
    if echo "${AFTER}" | grep -q '"complete":true'; then
      break
    fi
  fi
  sleep 10
done

echo "${AFTER}"

if ! echo "${AFTER}" | grep -q '"complete":true'; then
  echo "FAIL: crash recovery lost workers (issue #1260 regression)"
  exit 1
fi

echo "==> events observed by ${SURVIVOR}"
EVENTS=$(http "${SURVIVOR}" "/events")
echo "${EVENTS}"

if echo "${EVENTS}" | grep -q 'RelocationFailed'; then
  echo "FAIL: relocation reported failures"
  exit 1
fi

echo ""
echo "PASS: crash recovery on the default configuration is complete (${WORKERS}/${WORKERS} workers recovered, RelocationDerived observed)"
