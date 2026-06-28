#!/usr/bin/env bash
set -e

# Generate gRPC code from proto files
# Requires: protoc, protoc-gen-go, protoc-gen-go-grpc, protoc-gen-grpc-gateway

cd "$(dirname "$0")"

echo "Generating gRPC code from luckyagent.proto..."

protoc \
  --go_out=../grpc \
  --go_opt=paths=source_relative \
  --go-grpc_out=../grpc \
  --go-grpc_opt=paths=source_relative \
  --grpc-gateway_out=../grpc \
  --grpc-gateway_opt=paths=source_relative \
  --grpc-gateway_opt=logtostderr=true \
  -I. \
  -I./google \
  luckyagent.proto

echo "Proto generation complete!"
echo ""
echo "Generated files:"
ls -lh ../grpc/*.pb.go
