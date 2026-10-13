#!/bin/sh
set -eu
unset VERSION INSTALL_DIR SLICC_INSTALL_BASE_URL

root="$1"
base="${2%/}"
version="$3"
here="$(cd "$(dirname "$0")" && pwd)"
installer="$here/../../../../install.sh"
[ -f "$installer" ] || { echo "no install.sh at $installer" >&2; exit 1; }
fails=0

fail() {
  printf 'FAIL: %s\n' "$*" >&2
  fails=$((fails + 1))
}

hash_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

run() {
  out="$root/out.txt"
  if (cd "$root" && env "$@" >"$out" 2>&1); then
    code=0
  else
    code=$?
  fi
  cat "$out"
}

has() {
  grep -F -- "$1" "$out" >/dev/null 2>&1
}

only_slicc() {
  left="$(ls -A "$1")"
  [ "$left" = slicc ] || fail "$1 holds more than slicc: $left"
}

inst() {
  run "HOME=$home" "PATH=$cleanpath" "SLICC_INSTALL_BASE_URL=$base" "$@"
}

stage() {
  mkdir -p "$1"
  cp "$root"/release/* "$1/"
}

if [ "${SLICC_CHECK_WGET:-0}" = 1 ] && command -v curl >/dev/null 2>&1; then
  fail "curl is installed, so the wget path isn't tested"
fi

stage "$root/latest/download"
stage "$root/download/v$version"
stage "$root/tampered/latest/download"
for f in "$root"/tampered/latest/download/slicc-*; do
  printf 'x' >>"$f"
done
stage "$root/missing/latest/download"
grep -v ' slicc-' "$root/release/SHA256SUMS" >"$root/missing/latest/download/SHA256SUMS" || true

home="$root/home"
mkdir -p "$home"
bindir="$home/.local/bin"
cleanpath="/usr/bin:/bin:/usr/sbin:/sbin"

echo "== default install"
inst sh "$installer"
[ "$code" = 0 ] || fail "default install exited $code"
has "installed slicc $version at " || fail "no installed line"
has "isn't on PATH; add it with: export PATH=" || fail "no PATH hint"
[ "$("$bindir/slicc" --version)" = "slicc $version" ] || fail "--version of $bindir/slicc"
[ "$(stat -c %a "$bindir/slicc" 2>/dev/null || stat -f %Lp "$bindir/slicc")" = 755 ] || fail "mode isn't 755"
only_slicc "$bindir"
first="$(hash_of "$bindir/slicc")"

echo "== re-run is idempotent"
inst sh "$installer"
[ "$code" = 0 ] || fail "re-run exited $code"
has "installed slicc $version at " || fail "re-run: no installed line"
[ "$(hash_of "$bindir/slicc")" = "$first" ] || fail "re-run changed the binary"
only_slicc "$bindir"

echo "== piped, with --version and --dir"
custom="$root/custom dir/bin"
if (cd "$root" && env "HOME=$home" "PATH=$cleanpath" "SLICC_INSTALL_BASE_URL=$base" sh -s -- --version "$version" --dir "$custom" <"$installer" >"$root/out.txt" 2>&1); then code=0; else code=$?; fi
out="$root/out.txt"
cat "$out"
[ "$code" = 0 ] || fail "piped install exited $code"
has "/download/v$version" || fail "--version didn't pick the release"
[ "$("$custom/slicc" --version)" = "slicc $version" ] || fail "--dir install"
only_slicc "$custom"

echo "== INSTALL_DIR and VERSION, on PATH"
envdir="$root/envdir"
inst "PATH=$envdir:$cleanpath" "INSTALL_DIR=$envdir" "VERSION=v$version" sh "$installer"
[ "$code" = 0 ] || fail "INSTALL_DIR install exited $code"
[ "$("$envdir/slicc" --version)" = "slicc $version" ] || fail "INSTALL_DIR install"
has "isn't on PATH" && fail "PATH hint although $envdir is on PATH"
only_slicc "$envdir"

echo "== tampered asset is refused"
run "HOME=$home" "PATH=$cleanpath" "SLICC_INSTALL_BASE_URL=$base/tampered" sh "$installer"
[ "$code" != 0 ] || fail "tampered asset was installed"
has "doesn't match SHA256SUMS" || fail "no checksum message"
[ "$(hash_of "$bindir/slicc")" = "$first" ] || fail "tampered asset replaced the binary"
only_slicc "$bindir"
run "HOME=$home" "PATH=$cleanpath" "SLICC_INSTALL_BASE_URL=$base/tampered" "INSTALL_DIR=$root/never" sh "$installer"
[ "$code" != 0 ] || fail "tampered asset was installed into a new dir"
[ -e "$root/never/slicc" ] && fail "tampered asset left $root/never/slicc"

echo "== missing SHA256SUMS entry is refused"
run "HOME=$home" "PATH=$cleanpath" "SLICC_INSTALL_BASE_URL=$base/missing" "INSTALL_DIR=$root/never" sh "$installer"
[ "$code" != 0 ] || fail "asset without an entry was installed"
has "SHA256SUMS has no entry for slicc-" || fail "no missing-entry message"
[ -e "$root/never/slicc" ] && fail "missing entry left $root/never/slicc"

echo "== --help and unknown options"
run "PATH=$cleanpath" sh "$installer" --help
[ "$code" = 0 ] || fail "--help exited $code"
has "--version X" || fail "--help lacks --version"
run "PATH=$cleanpath" sh "$installer" --bogus
[ "$code" != 0 ] || fail "--bogus exited 0"

if [ "$fails" -gt 0 ]; then
  echo "$fails check(s) failed" >&2
  exit 1
fi
echo "all installer checks passed"
