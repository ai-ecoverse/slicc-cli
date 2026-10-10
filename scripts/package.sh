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
jsign_version="7.5"
jsign_sha256="602a51c3545a6dc4fb99bd2ea7152b26d1345916d0c93ddfbd5936cb735af91c"
work="$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/slicc-package.XXXXXX")"
keychain=""

cleanup() {
  if [ -n "$keychain" ]; then
    security delete-keychain "$keychain" >/dev/null 2>&1 || true
  fi
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

build() {
  rm -rf "$out"
  mkdir -p "$out" dist
  for target in $targets; do
    local goos="${target%/*}" goarch="${target#*/}" ext=""
    [ "$goos" = "windows" ] && ext=".exe"
    echo "building slicc-$goos-$goarch$ext ($version)"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath \
      -ldflags "-s -w -X main.version=$version" \
      -o "$out/slicc-$goos-$goarch$ext" ./cmd/slicc
  done
  printf '%s\n' "$version" > dist/VERSION
}

import_p12() {
  local raw="$work/raw.p12" pem="$work/cert.pem" norm="$work/normalized.p12"
  printf '%s' "$APPLE_CERTIFICATE_BASE64" | base64 --decode > "$raw"
  if ! openssl pkcs12 -in "$raw" -nodes -out "$pem" -passin "pass:$APPLE_CERTIFICATE_PASSWORD" 2>/dev/null; then
    openssl pkcs12 -in "$raw" -nodes -out "$pem" -passin "pass:$APPLE_CERTIFICATE_PASSWORD" -legacy
  fi
  if ! openssl pkcs12 -export -in "$pem" -out "$norm" -password "pass:$APPLE_CERTIFICATE_PASSWORD" \
    -keypbe AES-256-CBC -certpbe AES-256-CBC -macalg sha256 2>/dev/null; then
    openssl pkcs12 -export -in "$pem" -out "$norm" -password "pass:$APPLE_CERTIFICATE_PASSWORD" \
      -keypbe AES-256-CBC -certpbe AES-256-CBC -macalg sha256 -legacy
  fi
  rm -f "$raw" "$pem"
  local password
  password="$(openssl rand -base64 32)"
  keychain="$work/signing.keychain-db"
  security create-keychain -p "$password" "$keychain"
  security set-keychain-settings -lut 3600 "$keychain"
  security unlock-keychain -p "$password" "$keychain"
  security import "$norm" -P "$APPLE_CERTIFICATE_PASSWORD" -A -t cert -f pkcs12 -k "$keychain"
  security set-key-partition-list -S apple-tool:,apple:,codesign: -k "$password" "$keychain" >/dev/null
  local existing=() line
  while IFS= read -r line; do
    line="${line#"${line%%[![:space:]]*}"}"
    existing+=("${line//\"/}")
  done < <(security list-keychains -d user)
  security list-keychains -d user -s "$keychain" ${existing[@]+"${existing[@]}"}
  rm -f "$norm"
}

notarize() {
  local key="$work/AuthKey.p8" zip="$work/notarize.zip" result="$work/notarize.json"
  printf '%s' "$APPLE_API_KEY_P8_BASE64" | base64 --decode > "$key"
  (cd "$out" && zip -q "$zip" slicc-darwin-arm64 slicc-darwin-amd64)
  echo "notarizing the darwin binaries"
  xcrun notarytool submit "$zip" --key "$key" --key-id "$APPLE_API_KEY_ID" \
    --issuer "$APPLE_API_KEY_ISSUER_ID" --wait --timeout 30m --output-format json > "$result" || true
  local status id
  status="$(node -p "JSON.parse(require('fs').readFileSync('$result','utf8')).status" 2>/dev/null || echo unknown)"
  id="$(node -p "JSON.parse(require('fs').readFileSync('$result','utf8')).id" 2>/dev/null || echo)"
  if [ "$status" != "Accepted" ]; then
    echo "::error::notarization ended with status $status"
    cat "$result" >&2 || true
    if [ -n "$id" ]; then
      xcrun notarytool log "$id" --key "$key" --key-id "$APPLE_API_KEY_ID" --issuer "$APPLE_API_KEY_ISSUER_ID" >&2 || true
    fi
    exit 1
  fi
  echo "notarization accepted ($id)"
}

sign_macos() {
  if [ -z "${APPLE_CERTIFICATE_BASE64:-}" ] || [ -z "${APPLE_CERTIFICATE_PASSWORD:-}" ]; then
    echo "::warning::no Apple signing certificate; the darwin binaries stay unsigned"
    return
  fi
  if [ "$(uname -s)" != "Darwin" ]; then
    echo "::error::signing the darwin binaries needs a macOS runner"
    exit 1
  fi
  import_p12
  local identity
  identity="$(security find-identity -v -p codesigning "$keychain" | grep 'Developer ID Application' | head -1 | awk '{print $2}' || true)"
  if [ -z "$identity" ]; then
    echo "::error::no Developer ID Application identity in the certificate"
    exit 1
  fi
  for bin in slicc-darwin-arm64 slicc-darwin-amd64; do
    echo "signing $bin"
    codesign --force --timestamp --options runtime --identifier ai.sliccy.slicc \
      --keychain "$keychain" --sign "$identity" "$out/$bin"
    codesign --verify --strict --verbose=2 "$out/$bin"
  done
  if [ -z "${APPLE_API_KEY_P8_BASE64:-}" ] || [ -z "${APPLE_API_KEY_ID:-}" ] || [ -z "${APPLE_API_KEY_ISSUER_ID:-}" ]; then
    echo "::warning::no App Store Connect API key; the darwin binaries are signed but not notarized"
    return
  fi
  notarize
}

azure_token() {
  if [ -n "${AZURE_CLIENT_SECRET:-}" ]; then
    printf '%s|%s|%s' "$AZURE_TENANT_ID" "$AZURE_CLIENT_ID" "$AZURE_CLIENT_SECRET"
    return
  fi
  if [ -z "${ACTIONS_ID_TOKEN_REQUEST_URL:-}" ]; then
    echo "::error::no AZURE_CLIENT_SECRET and no GitHub OIDC token (id-token: write)" >&2
    exit 1
  fi
  local assertion
  assertion="$(curl -fsS -H "Authorization: bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
    "$ACTIONS_ID_TOKEN_REQUEST_URL&audience=api://AzureADTokenExchange" | node -p 'JSON.parse(require("fs").readFileSync(0,"utf8")).value')"
  curl -fsS -X POST "https://login.microsoftonline.com/$AZURE_TENANT_ID/oauth2/v2.0/token" \
    --data-urlencode "client_id=$AZURE_CLIENT_ID" \
    --data-urlencode "scope=https://codesigning.azure.net/.default" \
    --data-urlencode "grant_type=client_credentials" \
    --data-urlencode "client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer" \
    --data-urlencode "client_assertion=$assertion" | node -p 'JSON.parse(require("fs").readFileSync(0,"utf8")).access_token'
}

sign_windows() {
  local missing=""
  for name in AZURE_TENANT_ID AZURE_CLIENT_ID AZURE_TRUSTED_SIGNING_ENDPOINT AZURE_TRUSTED_SIGNING_ACCOUNT AZURE_TRUSTED_SIGNING_PROFILE; do
    [ -z "${!name:-}" ] && missing="$missing $name"
  done
  if [ -n "$missing" ]; then
    echo "::warning::no Azure Trusted Signing configuration ($missing ); the windows binaries stay unsigned"
    return
  fi
  local jar="$work/jsign.jar"
  curl -fsSL -o "$jar" "https://github.com/ebourg/jsign/releases/download/$jsign_version/jsign-$jsign_version.jar"
  if [ "$(sha256 "$jar" | awk '{print $1}')" != "$jsign_sha256" ]; then
    echo "::error::jsign-$jsign_version.jar does not match its pinned SHA-256"
    exit 1
  fi
  local token endpoint="${AZURE_TRUSTED_SIGNING_ENDPOINT#https://}"
  token="$(azure_token)"
  echo "::add-mask::$token"
  echo "signing the windows binaries with Azure Trusted Signing"
  java -jar "$jar" --storetype TRUSTEDSIGNING --keystore "${endpoint%/}" \
    --storepass "$token" \
    --alias "$AZURE_TRUSTED_SIGNING_ACCOUNT/$AZURE_TRUSTED_SIGNING_PROFILE" \
    --tsaurl http://timestamp.acs.microsoft.com --tsmode RFC3161 \
    --name slicc --url https://github.com/ai-ecoverse/slicc-cli \
    "$out/slicc-windows-arm64.exe" "$out/slicc-windows-amd64.exe"
}

checksums() {
  (cd "$out" && sha256 slicc-* > "$work/SHA256SUMS" && mv "$work/SHA256SUMS" SHA256SUMS)
  cat "$out/SHA256SUMS"
}

build
if [ "${SLICC_SIGN:-1}" = "1" ]; then
  sign_macos
  sign_windows
else
  echo "signing skipped (SLICC_SIGN=$SLICC_SIGN)"
fi
checksums
