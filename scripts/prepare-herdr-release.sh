#!/bin/sh
set -eu
[ "$#" -eq 1 ] || exit 1
printf '%s\n' "$1" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' || exit 1
mkdir -p .release
sed "s/^version = .*/version = \"$1\"/" herdr-plugin.toml > .release/herdr-plugin.toml
