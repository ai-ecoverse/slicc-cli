#!/bin/sh
set -u
if [ $# -lt 2 ]; then
  echo "usage: verdict.sh <name> <command> [args...]" >&2
  exit 2
fi
name="$1"
shift
dir="$(mktemp -d)" || exit 1
trap 'rm -rf "$dir"' EXIT
{
  "$@" 2>&1
  echo "$?" > "$dir/status"
} | tee "$dir/log"
status="$(cat "$dir/status" 2>/dev/null || true)"
if [ "$status" != 0 ]; then
  echo "verdict: $name failed (exit ${status:-unknown})" >&2
  exit "${status:-1}"
fi
if ! grep -q "^$name: PASS" "$dir/log"; then
  echo "verdict: $name exited 0 without a \"$name: PASS\" line; silence is not a pass" >&2
  exit 1
fi
