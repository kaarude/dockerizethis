#!/bin/sh
# End-to-end check: run the built binary against every fixture under
# testdata/fixtures/ and assert it emits a Dockerfile and .dockerignore.
set -eu
cd "$(dirname "$0")/.."

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

go build -o "$tmp/dockerizethis" ./cmd/dockerizethis

found=0
failed=0
for fixture in testdata/fixtures/*/; do
    [ -d "$fixture" ] || continue
    found=$((found + 1))
    name=$(basename "$fixture")
    work="$tmp/work/$name"
    mkdir -p "$work"
    cp -R "$fixture/." "$work/"
    if ! output=$("$tmp/dockerizethis" --yes --verify=none "$work" 2>&1); then
        echo "FAIL $name: dockerizethis exited non-zero:" >&2
        echo "$output" >&2
        failed=$((failed + 1))
        continue
    fi
    for artifact in Dockerfile .dockerignore; do
        if [ ! -f "$work/$artifact" ]; then
            echo "FAIL $name: $artifact not emitted" >&2
            failed=$((failed + 1))
        fi
    done
    [ -f "$work/Dockerfile" ] && [ -f "$work/.dockerignore" ] && echo "PASS $name"
done

if [ "$found" -eq 0 ]; then
    echo "no fixtures found under testdata/fixtures/" >&2
    exit 1
fi
if [ "$failed" -gt 0 ]; then
    echo "$failed fixture(s) failed" >&2
    exit 1
fi
echo "e2e: $found fixture(s) checked"
