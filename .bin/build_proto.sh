#!/bin/bash
set -e

echo "=== Run: buf generate ."
buf generate .

echo "=== Run: protoc-go-inject-tag"
for i in $(ls -k api-contracts-gen/*/*/*.pb.go api-contracts-gen/*/*/*/*.pb.go 2>/dev/null); do
    echo "- ${i}"
    protoc-go-inject-tag -input=${i}
done

echo "=== Completed."
