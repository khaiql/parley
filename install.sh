#!/bin/sh
set -eu

repo="khaiql/parley"
install_dir="${HOME}/.parley/bin"
install_path="${install_dir}/parley"

fail() {
  printf '%s\n' "$*" >&2
  exit 1
}

os="$(uname -s)"
arch="$(uname -m)"

case "$os" in
  Darwin)
    goreleaser_os="Darwin"
    ;;
  Linux)
    goreleaser_os="Linux"
    ;;
  *)
    fail "unsupported operating system: $os"
    ;;
esac

case "$arch" in
  x86_64|amd64)
    goreleaser_arch="x86_64"
    ;;
  arm64|aarch64)
    goreleaser_arch="arm64"
    ;;
  *)
    fail "unsupported architecture: $arch"
    ;;
esac

archive="parley_${goreleaser_os}_${goreleaser_arch}.tar.gz"
url="https://github.com/${repo}/releases/latest/download/${archive}"

command -v tar >/dev/null 2>&1 || fail "tar is required"

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/parley-install.XXXXXX")"
trap 'rm -rf "$tmpdir"' EXIT HUP INT TERM

download_to="${tmpdir}/${archive}"
if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$url" -o "$download_to"
elif command -v wget >/dev/null 2>&1; then
  wget -q -O "$download_to" "$url"
else
  fail "curl or wget is required"
fi

tar -xzf "$download_to" -C "$tmpdir"

if [ ! -f "${tmpdir}/parley" ]; then
  fail "archive did not contain parley binary"
fi

mkdir -p "$install_dir"
cp "${tmpdir}/parley" "$install_path"
chmod 0755 "$install_path"

printf '%s\n' "$install_path"
