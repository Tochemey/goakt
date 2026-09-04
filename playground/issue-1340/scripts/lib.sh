#!/usr/bin/env bash
# Helpers shared by demo.sh and compare.sh: reading JSON with whichever tool
# the machine has, and rendering times and durations the way the reports do.

if command -v jq >/dev/null 2>&1; then
  JSON_TOOL=jq
elif command -v python3 >/dev/null 2>&1; then
  JSON_TOOL=python3
else
  echo "FAIL: this demo needs jq or python3 to read the JSON reports"
  exit 1
fi

# now_ms prints the current time in epoch milliseconds.
now_ms() {
  if [ "${JSON_TOOL}" = "jq" ]; then
    jq -n 'now * 1000 | floor'
  else
    python3 -c 'import time; print(int(time.time() * 1000))'
  fi
}

# utc renders epoch milliseconds the way the reports render their timestamps.
utc() {
  if [ "${JSON_TOOL}" = "jq" ]; then
    jq -rn --argjson ms "$1" '($ms / 1000 | floor | strftime("%Y-%m-%dT%H:%M:%S")) + "." + ("00\($ms % 1000)"[-3:]) + "Z"'
  else
    python3 -c 'import datetime, sys; ms = int(sys.argv[1]); print(datetime.datetime.fromtimestamp(ms / 1000, datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%S.") + f"{ms % 1000:03d}Z")' "$1"
  fi
}

# json_path prints one value of a JSON file, addressed by a dotted path such as
# config.network_profile, or an empty string when the path holds nothing.
json_path() {
  local file=$1 path=$2

  if [ "${JSON_TOOL}" = "jq" ]; then
    jq -r --arg p "${path}" 'getpath($p | split(".")) // "" | tostring' "${file}"
  else
    python3 -c '
import json, sys

value = json.load(open(sys.argv[1]))
for key in sys.argv[2].split("."):
    if not isinstance(value, dict):
        value = ""
        break
    value = value.get(key, "")

print("" if value is None else value)
' "${file}" "${path}"
  fi
}

# millis prints a duration in milliseconds as seconds with three decimals.
millis() {
  local value=$1 sign=""

  if [ "${value}" -lt 0 ]; then
    sign="-"
    value=$(( -value ))
  fi

  printf '%s%d.%03ds' "${sign}" $(( value / 1000 )) $(( value % 1000 ))
}
