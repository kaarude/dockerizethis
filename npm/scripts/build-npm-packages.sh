#!/usr/bin/env bash
# Assemble the npm packages that make `npx dockerizethis` work.
#
#   build-npm-packages.sh <version> --goreleaser <dist-dir>
#   build-npm-packages.sh <version> --local <dockerize-binary>
#
# --goreleaser consumes the archives a `goreleaser release`/`--snapshot` run
# left in dist/ (dockerizethis_<ver>_<goos>_<goarch>.tar.gz / .zip).
# --local packages one already-built binary under the host platform only —
# enough to exercise the whole npx path without a release.
#
# Output: npm/dist/<pkg>/ staging dirs plus npm/dist/packed/*.tgz ready for
# `npm publish`. Platform packages that are missing simply drop out of the
# root package's optionalDependencies, so partial builds still pack.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
NPM_DIR="$REPO_ROOT/npm"
OUT="$NPM_DIR/dist"
PACKED="$OUT/packed"

usage() { sed -n '2,15p' "$0" >&2; exit 2; }

[ $# -ge 2 ] || usage
VERSION="$1"; shift

# node platform/cpu names, keyed by goreleaser GOOS_GOARCH.
node_platform() {
  case "$1" in
    darwin) echo darwin ;;
    linux) echo linux ;;
    windows) echo win32 ;;
    *) return 1 ;;
  esac
}
node_cpu() {
  case "$1" in
    amd64) echo x64 ;;
    arm64) echo arm64 ;;
    *) return 1 ;;
  esac
}

stage_platform_pkg() {
  # $1: goos, $2: goarch, $3: path to the dockerize binary
  local os cpu pkg dir bin
  os="$(node_platform "$1")"
  cpu="$(node_cpu "$2")"
  pkg="dockerizethis-${os}-${cpu}"
  dir="$OUT/$pkg"
  bin="dockerize"
  [ "$os" = "win32" ] && bin="dockerize.exe"
  mkdir -p "$dir/bin"
  cp "$3" "$dir/bin/$bin"
  chmod +x "$dir/bin/$bin"
  cp "$REPO_ROOT/LICENSE" "$dir/LICENSE"
  cat > "$dir/package.json" <<EOF
{
  "name": "$pkg",
  "version": "$VERSION",
  "description": "Prebuilt dockerize binary for $os/$cpu.",
  "license": "MIT",
  "repository": { "type": "git", "url": "git+https://github.com/kaarude/dockerizethis.git" },
  "os": ["$os"],
  "cpu": ["$cpu"],
  "files": ["bin"]
}
EOF
  echo "staged $pkg"
}

stage_from_goreleaser() {
  local dist="$1" archive base tmp goos goarch bin
  shopt -s nullglob
  for archive in "$dist"/dockerizethis_*_*.tar.gz "$dist"/dockerizethis_*_*.zip; do
    base="$(basename "$archive")"
    base="${base%.tar.gz}"; base="${base%.zip}"
    # dockerizethis_<version>_<goos>_<goarch> — version may itself contain
    # dots/dashes but never underscores.
    goos="$(echo "$base" | awk -F_ '{print $(NF-1)}')"
    goarch="$(echo "$base" | awk -F_ '{print $NF}')"
    tmp="$(mktemp -d)"
    case "$archive" in
      *.tar.gz) tar -xzf "$archive" -C "$tmp" ;;
      *.zip) unzip -q -o "$archive" -d "$tmp" ;;
    esac
    bin="$(find "$tmp" -name 'dockerize' -o -name 'dockerize.exe' | head -1)"
    [ -n "$bin" ] || { echo "no dockerize binary in $archive" >&2; exit 1; }
    stage_platform_pkg "$goos" "$goarch" "$bin"
    rm -rf "$tmp"
  done
}

stage_root_pkg() {
  local dir="$OUT/dockerizethis" pkgs
  mkdir -p "$dir"
  cp -R "$NPM_DIR/dockerizethis/." "$dir/"
  cp "$REPO_ROOT/LICENSE" "$dir/LICENSE"
  pkgs="$(cd "$OUT" && ls -d dockerizethis-*-* 2>/dev/null | tr '\n' ' ' || true)"
  VERSION="$VERSION" PKGS="$pkgs" node - "$dir/package.json" <<'EOF'
const fs = require("node:fs");
const file = process.argv[2];
const pkg = JSON.parse(fs.readFileSync(file, "utf8"));
pkg.version = process.env.VERSION;
pkg.optionalDependencies = {};
for (const name of (process.env.PKGS || "").trim().split(/\s+/).filter(Boolean)) {
  pkg.optionalDependencies[name] = process.env.VERSION;
}
fs.writeFileSync(file, JSON.stringify(pkg, null, 2) + "\n");
EOF
  echo "staged dockerizethis ($(echo $pkgs | wc -w | tr -d ' ') platform package(s))"
}

pack_all() {
  mkdir -p "$PACKED"
  rm -f "$PACKED"/*.tgz
  for dir in "$OUT"/*/; do
    [ -f "$dir/package.json" ] || continue
    (cd "$dir" && npm pack --pack-destination "$PACKED" >/dev/null)
  done
  ls -1 "$PACKED"
}

rm -rf "$OUT"
mkdir -p "$OUT"

case "${1:-}" in
  --goreleaser)
    [ -d "${2:-}" ] || usage
    stage_from_goreleaser "$2" ;;
  --local)
    [ -f "${2:-}" ] || usage
    case "$(uname -s)-$(uname -m)" in
      Darwin-arm64) stage_platform_pkg darwin arm64 "$2" ;;
      Darwin-x86_64) stage_platform_pkg darwin amd64 "$2" ;;
      Linux-aarch64) stage_platform_pkg linux arm64 "$2" ;;
      Linux-x86_64) stage_platform_pkg linux amd64 "$2" ;;
      *) echo "unsupported host $(uname -s)-$(uname -m)" >&2; exit 1 ;;
    esac ;;
  *) usage ;;
esac

stage_root_pkg
pack_all
