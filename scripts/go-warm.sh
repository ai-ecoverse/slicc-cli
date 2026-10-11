#!/usr/bin/env bash
set -uo pipefail

version="${1:-}"
version="${version#v}"
if [ -z "$version" ]; then
  echo "usage: scripts/go-warm.sh <version>" >&2
  exit 2
fi
module="github.com/ai-ecoverse/slicc-cli"
tag="v$version"
tries=5
pause=15

warn() {
  echo "::warning::go-warm: $*"
}

if ! git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
  warn "tag $tag is not on origin; nothing to warm"
  exit 0
fi

scratch="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/slicc-go-warm.XXXXXX")"
trap 'chmod -R u+w "$scratch" 2>/dev/null; rm -rf "$scratch"' EXIT

warmed=""
for i in $(seq 1 "$tries"); do
  if GOPROXY=https://proxy.golang.org GOFLAGS=-modcacherw GOPATH="$scratch/gopath" GOMODCACHE="$scratch/mod" \
    GONOPROXY= GOPRIVATE= GONOSUMDB= GOENV=off go list -m "$module@$tag"; then
    warmed=1
    break
  fi
  [ "$i" -lt "$tries" ] && sleep "$pause"
done
[ -n "$warmed" ] || warn "proxy.golang.org did not serve $module@$tag after $tries tries"

if ! curl -fsS --retry 3 --retry-delay 5 --max-time 30 -X POST "https://pkg.go.dev/fetch/$module@$tag" -o /dev/null; then
  warn "pkg.go.dev did not accept a fetch of $module@$tag"
fi
exit 0
