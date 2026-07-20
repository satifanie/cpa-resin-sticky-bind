#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if [[ ! -d CLIProxyAPI-src ]]; then
  git clone --depth 1 https://github.com/router-for-me/CLIProxyAPI ./CLIProxyAPI-src
fi

if ! grep -q 'replace github.com/router-for-me/CLIProxyAPI/v7' go.mod; then
  printf '\nreplace github.com/router-for-me/CLIProxyAPI/v7 => ./CLIProxyAPI-src\n' >> go.mod
fi

go test ./internal/stickybind/ -count=1
mkdir -p bin
CGO_ENABLED=1 go build -buildmode=c-shared -trimpath -o "bin/cpa-resin-sticky-bind.so" .
echo "built bin/cpa-resin-sticky-bind.so"
