#!/bin/sh
# Stage a pinned release. Linking and coordinator upgrades are explicit user steps.
set -eu
umask 077

fail() { printf '%s\n' "$*" >&2; exit 1; }
[ "$#" -eq 1 ] || fail "usage: sh install-herdr.sh vX.Y.Z[-prerelease]"
tag=$1
printf '%s\n' "$tag" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || fail "invalid release tag: $tag"
version=${tag#v}

case "$(uname -s)" in
  Darwin) os=Darwin ;;
  Linux) os=Linux ;;
  *) fail "unsupported operating system: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=x86_64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) fail "unsupported architecture: $(uname -m)" ;;
esac

command -v tar >/dev/null 2>&1 || fail "tar is required"
command -v curl >/dev/null 2>&1 || fail "curl is required"
if command -v sha256sum >/dev/null 2>&1; then
  checksum() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  checksum() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  fail "sha256sum or shasum is required"
fi

base="${HOME}/.local/share/parley/herdr"
mkdir -p "$base"
base=$(cd "$base" && pwd -P)
lock="$base/.install-lock"
mkdir "$lock" 2>/dev/null || fail "another install is active; if abandoned, remove $lock"
stage=""
trap 'rm -rf "$stage" "$lock"' EXIT
trap 'exit 1' HUP INT TERM
stage=$(mktemp -d "$base/.install.XXXXXX")
archive="parley-herdr_${tag}_${os}_${arch}.tar.gz"
url="https://github.com/khaiql/parley/releases/download/${tag}"
curl -fsSL "$url/$archive" -o "$stage/$archive" || fail "release asset unavailable: $tag/$archive"
curl -fsSL "$url/checksums.txt" -o "$stage/checksums.txt" || fail "release checksums unavailable: $tag"
expected=$(awk -v name="$archive" '$2 == name {print $1}' "$stage/checksums.txt")
[ "${#expected}" -eq 64 ] || fail "missing or invalid checksum for $archive"
[ "$(checksum "$stage/$archive")" = "$expected" ] || fail "checksum mismatch: $archive"

mkdir "$stage/bundle"
tar -xzf "$stage/$archive" -C "$stage/bundle" || fail "cannot extract $archive"
validate() {
  bundle=$1
  [ -f "$bundle/herdr-plugin.toml" ] || fail "bundle has no herdr-plugin.toml"
  grep -q '^\[\[build\]\]' "$bundle/herdr-plugin.toml" && fail "bundle must not run build commands"
  [ "$(sed -n 's/^version = "\([^"]*\)"$/\1/p' "$bundle/herdr-plugin.toml")" = "$version" ] || fail "manifest release mismatch: $tag"
  [ -x "$bundle/bin/parley" ] || fail "bundle has no executable bin/parley"
  header=$(od -An -tx1 -N20 "$bundle/bin/parley" | tr -d '[:space:]')
  [ "${#header}" -eq 40 ] || fail "invalid helper executable header"
  case "$os/$arch:$header" in
    Linux/x86_64:7f454c460201*3e00|Linux/arm64:7f454c460201*b700) ;;
    Darwin/x86_64:cffaedfe07000001*|Darwin/arm64:cffaedfe0c000001*) ;;
    *) fail "wrong helper architecture or executable format: expected $os/$arch" ;;
  esac
  [ -s "$bundle/skills/parley/SKILL.md" ] || fail "bundle has no Parley skill"
  [ -x "$bundle/skills/parley/scripts/ensure-parley" ] || fail "bundle has no executable skill bootstrap"
  info=$("$bundle/bin/parley" version) || fail "cannot run helper for $os/$arch (wrong architecture or unsupported host)"
  actual=$(printf '%s\n' "$info" | sed -n 's/.*"version": *"\([^"]*\)".*/\1/p')
  [ "$actual" = "$version" ] || fail "binary release mismatch: expected $version, got $actual"
  helper=$(printf '%s\n' "$info" | sed -n 's/.*"herdr_helper_version": *\([0-9][0-9]*\).*/\1/p')
  [ "$helper" = 4 ] || fail "incompatible Herdr helper: protocol 4 required"
}
validate "$stage/bundle"
target="$base/$tag/${os}_${arch}"
if [ -e "$target" ] || [ -L "$target" ]; then
  [ ! -L "$target" ] || fail "refusing to replace linked release directory: $target"
  validate "$target"
  diff -r "$stage/bundle" "$target" >&2 || fail "existing release differs; refusing to overwrite $target"
else
  mkdir -p "$base/$tag"
  mv "$stage/bundle" "$target"
fi
printf '%s\n' "$target"
