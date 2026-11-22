#!/bin/sh
# Regenera o código gRPC a partir de api/proto. Requer protoc no PATH; os
# plugins vêm do módulo tools/ (versões fixadas em tools/go.mod), sem instalação global.
set -eu
root="$(cd "$(dirname "$0")/.." && pwd)"
bin="$(mktemp -d)"
trap 'rm -rf "$bin"' EXIT
(cd "$root/tools" && go build -o "$bin/protoc-gen-go" google.golang.org/protobuf/cmd/protoc-gen-go \
  && go build -o "$bin/protoc-gen-go-grpc" google.golang.org/grpc/cmd/protoc-gen-go-grpc)
PATH="$bin:$PATH" protoc -I "$root/api/proto" \
  --go_out="$root/internal/adapter/grpcapi" --go_opt=paths=source_relative \
  --go-grpc_out="$root/internal/adapter/grpcapi" --go-grpc_opt=paths=source_relative \
  "$root"/api/proto/balance/v1/balance.proto
