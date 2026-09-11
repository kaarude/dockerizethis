#!/bin/sh
# Runs the same checks as CI: vet, tests, and golangci-lint when installed.
set -eu
cd "$(dirname "$0")/.."

go vet ./...
go test ./...

if command -v golangci-lint >/dev/null 2>&1; then
    golangci-lint run
else
    echo "golangci-lint not installed; skipping" >&2
fi
