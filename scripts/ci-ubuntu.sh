#!/usr/bin/env bash
set -euo pipefail

echo "[ci-ubuntu] go vet ./..."
go vet ./...

echo "[ci-ubuntu] go build -trimpath ./cmd/conf"
go build -trimpath ./cmd/conf

echo "[ci-ubuntu] go test -race ./..."
go test -race ./...

echo "[ci-ubuntu] go run ./tools/coveragecheck"
go run ./tools/coveragecheck

echo "[ci-ubuntu] go run ./tools/gofmtcheck"
go run ./tools/gofmtcheck

echo "[ci-ubuntu] golangci-lint run"
golangci-lint run
