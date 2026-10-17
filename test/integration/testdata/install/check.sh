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
stage "$root/broken/latest/download"
stage "$root/alien/latest/download"
stage "$root/wrong/download/v$version"
for f in "$root"/alien/latest/download/slicc-*; do
  printf '#!/bin/sh\necho hello\n' >"$f"
done
for f in "$root"/broken/latest/download/slicc-*; do
  printf '#!/bin/sh\nexit 3\n' >"$f"
done
for f in "$root"/wrong/download/v"$version"/slicc-*; do
  printf '#!/bin/sh\necho "slicc 9.9.9"\n' >"$f"
done
for d in "$root/broken/latest/download" "$root/alien/latest/download" "$root/wrong/download/v$version"; do
  (cd "$d" && for f in slicc-*; do printf '%s  %s\n' "$(hash_of "$f")" "$f"; done >SHA256SUMS)
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

for kind in broken alien; do
  echo "== a $kind asset is refused"
  run "HOME=$home" "PATH=$cleanpath" "SLICC_INSTALL_BASE_URL=$base/$kind" sh "$installer"
  [ "$code" != 0 ] || fail "$kind asset was installed"
  has "doesn't run as slicc on this machine; nothing was installed" || fail "no $kind-asset message"
  [ "$(hash_of "$bindir/slicc")" = "$first" ] || fail "$kind asset replaced the binary"
  only_slicc "$bindir"
done

echo "== an asset with the wrong version is refused"
run "HOME=$home" "PATH=$cleanpath" "SLICC_INSTALL_BASE_URL=$base/wrong" sh "$installer" --version "$version"
[ "$code" != 0 ] || fail "wrong-version asset was installed"
has "says \"slicc 9.9.9\", not slicc $version" || fail "no wrong-version message"
[ "$(hash_of "$bindir/slicc")" = "$first" ] || fail "wrong-version asset replaced the binary"
only_slicc "$bindir"

echo "== a directory at the install path is refused"
mkdir -p "$root/clash/slicc"
run "HOME=$home" "PATH=$cleanpath" "SLICC_INSTALL_BASE_URL=$base" "INSTALL_DIR=$root/clash" sh "$installer"
[ "$code" != 0 ] || fail "installed over a directory"
has "slicc is a directory; nothing was installed" || fail "no directory message"
[ -z "$(ls -A "$root/clash/slicc")" ] || fail "the directory at the install path isn't empty"
only_slicc "$root/clash"

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
echo "install-check: PASS all installer checks passed"
