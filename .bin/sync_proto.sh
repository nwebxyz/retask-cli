#!/bin/bash
# Copies approved proto files from local api-contracts/ into proto/
# Run from repo root. Requires api-contracts/ to be present locally (gitignored).
set -e

if [ ! -d "api-contracts" ]; then
  echo "ERROR: api-contracts/ not found. Clone it locally first (it is gitignored)."
  exit 1
fi

APPROVED=(
  "auth/v1"
  "common/v1"
  "customer/v1"
  "file/v1"
  "integration/v1"
  "project/v1"
  "retask/common/v1"
  "retask/agent/v1"
  "retask/project/v1"
  "retask/sandbox/v1"
  "retask/task/v1"
  "workspace/v1"
)
# Add new approved services here ↑

for svc in "${APPROVED[@]}"; do
  mkdir -p "proto/${svc}"
  cp api-contracts/proto/${svc}/*.proto proto/${svc}/
done

echo "Synced ${#APPROVED[@]} services. Run .bin/build_proto.sh to regenerate."
