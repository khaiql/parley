#!/bin/sh
set -eu

repo="khaiql/parley"
install_dir="${HOME}/.local/bin"
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
if command -v sha256sum >/dev/null 2>&1; then
  checksum() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  checksum() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  fail "sha256sum or shasum is required"
fi

tmpdir="$(mktemp -d "${TMPDIR:-/tmp}/parley-install.XXXXXX")"
staged_binary=""
trap 'rm -rf "$tmpdir"; if [ -n "$staged_binary" ]; then rm -f "$staged_binary"; fi' EXIT
trap 'exit 1' HUP INT TERM

download() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$1" -o "$2"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    fail "curl or wget is required"
  fi
}
download_to="${tmpdir}/${archive}"
download "$url" "$download_to"
download "https://github.com/${repo}/releases/latest/download/checksums.txt" "$tmpdir/checksums.txt"
expected=$(awk -v name="$archive" '$2 == name {print $1}' "$tmpdir/checksums.txt")
[ "${#expected}" -eq 64 ] || fail "missing or invalid checksum for $archive"
[ "$(checksum "$download_to")" = "$expected" ] || fail "checksum mismatch: $archive"

tar -xzf "$download_to" -C "$tmpdir"

if [ ! -f "${tmpdir}/parley" ]; then
  fail "archive did not contain parley binary"
fi

mkdir -p "$install_dir"
staged_binary=$(mktemp "$install_dir/.parley.XXXXXX")
cp "${tmpdir}/parley" "$staged_binary"
chmod 0755 "$staged_binary"
mv -f "$staged_binary" "$install_path"
staged_binary=""

printf '%s\n' "$install_path"

printf 'Installed Parley at %s\n' "$install_path" >&2
case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) printf 'To run parley by name, add this to your shell profile: export PATH="%s:$PATH"\n' "$install_dir" >&2 ;;
esac
active=$(command -v parley 2>/dev/null || true)
if [ -n "$active" ] && [ "$active" != "$install_path" ]; then
  printf 'Warning: %s appears earlier on PATH. Use %s or remove the older installation.\n' "$active" "$install_path" >&2
fi
