#!/bin/bash
set -e

APPROVED_SERVICES=(
  "auth/v1"
  "common/v1"
  "customer/v1"
  "file/v1"
  "integration/v1"
  "project/v1"
  "retask/agent/v1"
  "retask/common/v1"
  "retask/project/v1"
  "retask/sandbox/v1"
  "retask/task/v1"
  "workspace/v1"
)
# Add new approved services here ↑

echo "=== Run: buf generate ."
buf generate .

echo "=== Run: protoc-go-inject-tag"
for svc in "${APPROVED_SERVICES[@]}"; do
  for f in proto-gen/${svc}/*.pb.go; do
    [ -f "$f" ] || continue
    echo "- ${f}"
    protoc-go-inject-tag -input="${f}"
  done
done

echo "=== Completed."
