#!/bin/bash

# Build the ultra-simple replicator

echo "Building ultra-simple replicator..."

# Build for current platform
go build -o ultrasimple ./cmd/ultrasimple

echo "Build complete: ./ultrasimple"
echo ""
echo "Usage examples:"
echo "  # Dry run to test pattern matching"
echo "  ./ultrasimple -dry-run -pattern '/data/*/databases/*.db'"
echo ""
echo "  # Run with S3 uploads"
echo "  ./ultrasimple -bucket my-backups -pattern '/data/*/databases/*.db'"
echo ""
echo "  # Custom interval and concurrency"
echo "  ./ultrasimple -bucket my-backups -interval 1m -concurrent 50"
echo ""
echo "  # With explicit AWS credentials"
echo "  ./ultrasimple -bucket my-backups -access-key KEY -secret-key SECRET"