#!/bin/sh
set -eu
report=$(slicc-lint-comments 2>&1) && status=0 || status=$?
if [ "$status" -eq 0 ]; then
  printf '%s\n' "$report"
  exit 0
fi
rest=$(printf '%s\n' "$report" | grep -vE '^go\.mod:[0-9]+: comment not allowed: // indirect$' || true)
if [ -n "$rest" ]; then
  printf '%s\n' "$rest" >&2
  exit 1
fi
printf 'ok: no comments besides the go.mod // indirect markers\n'
