#!/bin/bash

# setup_localstack.sh - Setup LocalStack for S3 testing without AWS

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}=== LocalStack Setup for Litestream S3 Testing ===${NC}"
echo

# Check if Docker is installed
if ! command -v docker &> /dev/null; then
    echo -e "${RED}Error: Docker is not installed${NC}"
    echo "Please install Docker from https://www.docker.com/"
    exit 1
fi

# Check if LocalStack is already running
if docker ps | grep -q localstack; then
    echo -e "${YELLOW}LocalStack is already running${NC}"
    echo "To stop it: docker stop localstack"
    echo
else
    echo "Starting LocalStack..."
    docker run -d \
        --name localstack \
        -p 4566:4566 \
        -e SERVICES=s3 \
        -e DEFAULT_REGION=us-east-1 \
        localstack/localstack
    
    echo "Waiting for LocalStack to be ready..."
    sleep 5
    
    echo -e "${GREEN}✓ LocalStack started${NC}"
fi

# Install AWS CLI if not present
if ! command -v aws &> /dev/null; then
    echo -e "${YELLOW}AWS CLI not found. Please install it to interact with LocalStack${NC}"
    echo "  macOS: brew install awscli"
    echo "  Linux: apt-get install awscli or yum install aws-cli"
else
    # Create test bucket
    echo "Creating test bucket..."
    aws --endpoint-url=http://localhost:4566 \
        s3 mb s3://litestream-test \
        2>/dev/null || echo "Bucket may already exist"
    
    echo -e "${GREEN}✓ Test bucket ready${NC}"
fi

echo
echo -e "${GREEN}=== Setup Complete ===${NC}"
echo
echo "To run the S3 demo with LocalStack:"
echo
echo -e "${YELLOW}export LITESTREAM_S3_BUCKET=litestream-test${NC}"
echo -e "${YELLOW}export LITESTREAM_S3_ENDPOINT=http://localhost:4566${NC}"
echo -e "${YELLOW}export AWS_ACCESS_KEY_ID=test${NC}"
echo -e "${YELLOW}export AWS_SECRET_ACCESS_KEY=test${NC}"
echo -e "${YELLOW}export AWS_REGION=us-east-1${NC}"
echo
echo "Then run:"
echo -e "${GREEN}go run demo_with_s3.go${NC}"
echo
echo "To check uploaded files:"
echo -e "${GREEN}aws --endpoint-url=http://localhost:4566 s3 ls s3://litestream-test/ --recursive${NC}"
echo
echo "To stop LocalStack when done:"
echo -e "${YELLOW}docker stop localstack && docker rm localstack${NC}"