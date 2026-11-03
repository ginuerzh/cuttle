#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

go build ./...
go vet ./...
go test ./... -p 1 -count=1
