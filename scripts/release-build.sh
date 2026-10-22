#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

version="${1:-}"
if ! printf '%s' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "usage: scripts/release-build.sh <x.y.z>" >&2
  exit 2
fi

SLICC_SIGN="${SLICC_SIGN:-required}" bash scripts/package.sh "$version"

case "$(uname -s)/$(uname -m)" in
  Linux/x86_64) host=slicc-linux-amd64 ;;
  Linux/aarch64) host=slicc-linux-arm64 ;;
  Darwin/arm64) host=slicc-darwin-arm64 ;;
  Darwin/x86_64) host=slicc-darwin-amd64 ;;
  *) echo "release-build: FAIL no release binary runs on $(uname -s)/$(uname -m)" >&2; exit 1 ;;
esac
got="$("artifacts/release/$host" --version)"
if [ "$got" != "slicc $version" ]; then
  echo "release-build: FAIL $host --version says \"$got\", not \"slicc $version\"" >&2
  exit 1
fi

node scripts/npm-stage.mjs --version "$version" --companions --wasi
node scripts/npm-stage.mjs --name sliccy --launcher-only --out dist/npm-next \
  --version "7.0.0-next.$version" --deps-version "$version" --companions

echo "release-build: PASS $version, $host runs, $(wc -l < artifacts/release/SHA256SUMS | tr -d ' ') files in SHA256SUMS"
