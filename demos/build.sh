#!/bin/bash
# Build individual demo binaries

echo "Building demo binaries..."

for demo in demo_*.go; do
    name="${demo%.go}"
    echo "Building $name..."
    go build -o "$name" "$demo"
done

echo "Done! Demo binaries created."