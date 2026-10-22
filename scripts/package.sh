#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

version="${1:-${SLICC_RELEASE_VERSION:-}}"
if [ -z "$version" ]; then
  echo "usage: scripts/package.sh <version>" >&2
  exit 2
fi
version="${version#v}"
out="artifacts/release"
targets="darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/arm64 windows/amd64"
work="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/slicc-package.XXXXXX")"

cleanup() {
  rm -rf "$work"
}
trap cleanup EXIT

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$@"
  else
    shasum -a 256 "$@"
  fi
}

pin_go() {
  local want
  want="$(awk '$1 == "toolchain" { t = $2 } $1 == "go" { g = $2 } END { if (t) print t; else if (g ~ /^[0-9]+\.[0-9]+\.[0-9]+$/) print "go" g }' go.mod)"
  if [ -z "$want" ]; then
    echo "::error::go.mod names no Go release: it needs a toolchain line or a go line with a patch version (go 1.26.5)" >&2
    exit 1
  fi
  unset GOFLAGS GOEXPERIMENT GOAMD64 GOARM64 GOWASM GOFIPS140
  export GOENV=off GOTOOLCHAIN="$want" GOWORK=off
  echo "building with $(go version) (GOTOOLCHAIN=$GOTOOLCHAIN from go.mod)"
}

build() {
  pin_go
  rm -rf "$out"
  mkdir -p "$out" dist
  for target in $targets; do
    local goos="${target%/*}" goarch="${target#*/}" ext=""
    [ "$goos" = "windows" ] && ext=".exe"
    echo "building slicc-$goos-$goarch$ext ($version)"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -buildvcs=false \
      -ldflags "-s -w -X main.version=$version" \
      -o "$out/slicc-$goos-$goarch$ext" ./cmd/slicc
  done
  echo "building slicc-wasip1-wasm.wasm ($version)"
  CGO_ENABLED=0 GOOS=wasip1 GOARCH=wasm go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X main.version=$version" \
    -o "$out/slicc-wasip1-wasm.wasm" ./cmd/slicc
  printf '%s\n' "$version" > dist/VERSION
}

rcodesign_version=0.29.0

rcodesign() {
  if [ -n "${RCODESIGN:-}" ]; then
    "$RCODESIGN" "$@"
    return
  fi
  local tool="$work/rcodesign/rcodesign"
  if [ ! -x "$tool" ]; then
    local asset sum
    case "$(uname -s)/$(uname -m)" in
      Linux/x86_64) asset=x86_64-unknown-linux-musl sum=dbe85cedd8ee4217b64e9a0e4c2aef92ab8bcaaa41f20bde99781ff02e600002 ;;
      Linux/aarch64) asset=aarch64-unknown-linux-musl sum=4af92c87ddf52f5f2d1258a3b4e56c7dcb8f1b2468df744976c5f139e031961f ;;
      Darwin/*) asset=macos-universal sum=d98372d5524226ccf9dc0eda03d4e4f5826182dabb2fc3f2bd303ed9113a748d ;;
      *) echo "::error::no rcodesign $rcodesign_version for $(uname -s)/$(uname -m); set RCODESIGN" >&2; exit 1 ;;
    esac
    mkdir -p "$work/rcodesign"
    curl -fsSL --retry 3 -o "$work/rcodesign.tar.gz" \
      "https://github.com/indygreg/apple-platform-rs/releases/download/apple-codesign%2F$rcodesign_version/apple-codesign-$rcodesign_version-$asset.tar.gz"
    echo "$sum  $work/rcodesign.tar.gz" > "$work/rcodesign.sum"
    (cd "$work" && sha256 -c rcodesign.sum)
    tar -xzf "$work/rcodesign.tar.gz" -C "$work/rcodesign" --strip-components=1
  fi
  "$tool" "$@"
}

signing_pem() {
  local raw="$work/raw.p12" pem="$work/signing.pem"
  printf '%s' "$APPLE_CERTIFICATE_BASE64" | base64 --decode > "$raw"
  if ! openssl pkcs12 -in "$raw" -nodes -out "$pem" -passin env:APPLE_DEVELOPER_CERTIFICATE_PASSWORD 2>/dev/null; then
    openssl pkcs12 -in "$raw" -nodes -out "$pem" -passin env:APPLE_DEVELOPER_CERTIFICATE_PASSWORD -legacy
  fi
  rm -f "$raw"
  chmod 600 "$pem"
  echo "$pem"
}

sign_macos() {
  local missing="" name
  for name in APPLE_CERTIFICATE_BASE64 APPLE_DEVELOPER_CERTIFICATE_PASSWORD APPLE_API_KEY_ID APPLE_API_KEY_ISSUER_ID APPLE_API_KEY_P8_BASE64; do
    [ -n "${!name:-}" ] || missing="$missing $name"
  done
  if [ -n "$missing" ]; then
    if [ "${SLICC_SIGN:-1}" = "required" ]; then
      echo "::error::signing and notarizing the darwin binaries needs$missing"
      exit 1
    fi
    echo "::warning::no$missing; the darwin binaries stay unsigned"
    return
  fi
  local pem key="$work/AuthKey.p8" api="$work/api-key.json"
  pem="$(signing_pem)"
  printf '%s' "$APPLE_API_KEY_P8_BASE64" | base64 --decode > "$key"
  rcodesign encode-app-store-connect-api-key -o "$api" "$APPLE_API_KEY_ISSUER_ID" "$APPLE_API_KEY_ID" "$key" >/dev/null
  rm -f "$key"
  for bin in slicc-darwin-arm64 slicc-darwin-amd64; do
    echo "signing $bin"
    rcodesign sign --pem-file "$pem" --binary-identifier ai.sliccy.slicc --code-signature-flags runtime --for-notarization "$out/$bin"
    rcodesign verify "$out/$bin"
  done
  local zip="$work/notarize.zip"
  (cd "$out" && zip -q "$zip" slicc-darwin-arm64 slicc-darwin-amd64)
  echo "notarizing slicc-darwin-arm64 and slicc-darwin-amd64"
  rcodesign notary-submit --api-key-file "$api" --wait --max-wait-seconds 1800 "$zip"
  rm -f "$pem" "$api" "$zip"
}

checksums() {
  cp install.sh "$out/install.sh"
  chmod 755 "$out/install.sh"
  (cd "$out" && sha256 slicc-* install.sh > "$work/SHA256SUMS" && mv "$work/SHA256SUMS" SHA256SUMS)
  cat "$out/SHA256SUMS"
}

build
if [ "${SLICC_SIGN:-1}" != "0" ]; then
  sign_macos
else
  echo "signing skipped (SLICC_SIGN=$SLICC_SIGN)"
fi
checksums
