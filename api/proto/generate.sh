#!/usr/bin/env bash
set -e

# Generate gRPC code from proto files
# Requires: protoc, protoc-gen-go, protoc-gen-go-grpc, protoc-gen-grpc-gateway

cd "$(dirname "$0")"

echo "Generating gRPC code from luckyagent.proto..."

include_flags=(
  -I.
  -I./google
)

if [[ -n "${PROTO_INCLUDE:-}" ]]; then
  include_flags+=("-I${PROTO_INCLUDE}")
elif [[ -f /tmp/include/google/protobuf/descriptor.proto ]]; then
  include_flags+=(-I/tmp/include)
elif [[ -f /usr/local/include/google/protobuf/descriptor.proto ]]; then
  include_flags+=(-I/usr/local/include)
elif [[ -f /usr/include/google/protobuf/descriptor.proto ]]; then
  include_flags+=(-I/usr/include)
fi

protoc \
  --go_out=../grpc \
  --go_opt=paths=source_relative \
  --go-grpc_out=../grpc \
  --go-grpc_opt=paths=source_relative \
  --grpc-gateway_out=../grpc \
  --grpc-gateway_opt=paths=source_relative \
  --grpc-gateway_opt=logtostderr=true \
  "${include_flags[@]}" \
  luckyagent.proto

echo "Proto generation complete!"
echo ""
echo "Generated files:"
ls -lh ../grpc/*.pb.go ../grpc/*.pb.gw.go
