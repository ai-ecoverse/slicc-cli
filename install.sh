#!/bin/sh
set -eu

repo="ai-ecoverse/slicc-cli"
bin="slicc"
version="${VERSION:-}"
dir="${INSTALL_DIR:-}"
tmp=""
part=""

say() {
  printf '%s\n' "$*"
}

die() {
  printf 'slicc install: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<EOF
Install slicc from the GitHub releases of $repo.

  curl -fsSL https://github.com/$repo/releases/latest/download/install.sh | sh
  curl -fsSL https://github.com/$repo/releases/latest/download/install.sh | sh -s -- [options]

Options:
  --version X   install release X (default: the latest release; env VERSION)
  --dir D       install into D (default: \$HOME/.local/bin; env INSTALL_DIR)
  --help        print this help
EOF
}

cleanup() {
  if [ -n "$part" ]; then
    rm -f "$part"
  fi
  if [ -n "$tmp" ]; then
    rm -rf "$tmp"
  fi
}

parse() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --help|-h)
        usage
        exit 0
        ;;
      --version)
        [ $# -ge 2 ] || die "--version needs a value"
        version="$2"
        shift 2
        ;;
      --version=*)
        version="${1#--version=}"
        shift
        ;;
      --dir)
        [ $# -ge 2 ] || die "--dir needs a value"
        dir="$2"
        shift 2
        ;;
      --dir=*)
        dir="${1#--dir=}"
        shift
        ;;
      *)
        usage >&2
        die "unknown option $1"
        ;;
    esac
  done
}

detect_os() {
  case "$(uname -s)" in
    Darwin) echo darwin ;;
    Linux) echo linux ;;
    *)
      die "$(uname -s) isn't supported by this installer; use npm (npm install -g @ai-ecoverse/slicc), or on Windows download slicc-windows-<arch>.exe from https://github.com/$repo/releases/latest"
      ;;
  esac
}

detect_arch() {
  arch="$(uname -m)"
  if [ "$1" = darwin ] && [ "$arch" = x86_64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
    arch=arm64
  fi
  case "$arch" in
    x86_64|amd64) echo amd64 ;;
    arm64|aarch64) echo arm64 ;;
    *) die "the $arch CPU isn't supported; slicc ships for amd64 and arm64" ;;
  esac
}

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 -o "$2" "$1" || die "downloading $1 failed"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1" || die "downloading $1 failed"
  else
    die "neither curl nor wget is installed"
  fi
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "neither sha256sum nor shasum is installed"
  fi
}

on_path() {
  case ":${PATH:-}:" in
    *":$1:"*) return 0 ;;
    *) return 1 ;;
  esac
}

main() {
  parse "$@"
  [ -n "$dir" ] || dir="${HOME:?HOME is not set}/.local/bin"
  version="${version#v}"
  os="$(detect_os)"
  arch="$(detect_arch "$os")"
  asset="$bin-$os-$arch"
  base="${SLICC_INSTALL_BASE_URL:-https://github.com/$repo/releases}"
  base="${base%/}"
  if [ -n "$version" ]; then
    url="$base/download/v$version"
  else
    url="$base/latest/download"
  fi

  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/slicc-install.XXXXXX")"

  say "downloading $asset from $url"
  fetch "$url/SHA256SUMS" "$tmp/SHA256SUMS"
  fetch "$url/$asset" "$tmp/$asset"

  want="$(awk -v a="$asset" '$2 == a || $2 == "*" a { print $1; exit }' "$tmp/SHA256SUMS")"
  [ -n "$want" ] || die "SHA256SUMS has no entry for $asset; nothing was installed"
  got="$(sha256 "$tmp/$asset")"
  [ "$got" = "$want" ] || die "$asset doesn't match SHA256SUMS (expected $want, got $got); nothing was installed"

  mkdir -p "$dir" || die "can't create $dir"
  given="$dir"
  dir="$(cd "$dir" && pwd -P)"
  part="$(mktemp "$dir/.$bin.XXXXXX")"
  cp "$tmp/$asset" "$part"
  chmod 755 "$part"
  installed="$("$part" --version 2>/dev/null)" || die "the downloaded $asset doesn't run on this machine; nothing was installed"
  if [ -n "$version" ] && [ "$installed" != "$bin $version" ]; then
    die "the downloaded $asset says \"$installed\", not $bin $version; nothing was installed"
  fi
  mv -f "$part" "$dir/$bin"
  part=""

  say "installed $installed at $dir/$bin"
  if ! on_path "$dir" && ! on_path "$given" && ! on_path "${given%/}"; then
    say "$dir isn't on PATH; add it with: export PATH=\"$dir:\$PATH\""
  fi
}

main "$@"
