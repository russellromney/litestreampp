#!/bin/bash

# benchmark_creation.sh - Benchmark database creation methods

set -e

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

echo -e "${GREEN}=== Database Creation Benchmark ===${NC}"
echo
echo "Comparing methods for creating 10,000 SQLite databases"
echo

# Function to run benchmark
run_benchmark() {
    local method=$1
    local desc=$2
    
    echo -e "${BLUE}Method: $desc${NC}"
    
    # Clean up before test
    rm -rf ~/.litestream-demos/fast-demo 2>/dev/null || true
    
    # Run and capture time
    output=$(go run demo_fast_setup.go $method 2>&1 | grep "Created 10000 databases in" || echo "Failed")
    
    if [[ $output == *"Failed"* ]]; then
        echo -e "  ${YELLOW}Failed to complete${NC}"
    else
        # Extract time
        time=$(echo $output | sed 's/.*in \(.*\)/\1/')
        echo -e "  Time: ${GREEN}$time${NC}"
        
        # Calculate rate
        if [[ $time =~ ([0-9.]+)s ]]; then
            seconds=${BASH_REMATCH[1]}
            rate=$(echo "scale=0; 10000 / $seconds" | bc)
            echo -e "  Rate: ${GREEN}$rate databases/second${NC}"
        fi
    fi
    
    # Check disk usage
    if [ -d ~/.litestream-demos/fast-demo ]; then
        size=$(du -sh ~/.litestream-demos/fast-demo 2>/dev/null | cut -f1)
        echo -e "  Size: $size"
    fi
    
    # Clean up after test
    rm -rf ~/.litestream-demos/fast-demo 2>/dev/null || true
    
    echo
}

# Check if bc is installed for calculations
if ! command -v bc &> /dev/null; then
    echo -e "${YELLOW}Warning: 'bc' not installed. Rate calculations will be skipped.${NC}"
    echo "Install with: brew install bc (macOS) or apt-get install bc (Linux)"
    echo
fi

# Run benchmarks
echo "Running benchmarks..."
echo

run_benchmark "copy" "Template Copying (memory-based)"
run_benchmark "parallel" "Parallel Creation (worker pool)"
run_benchmark "hybrid" "Hybrid (parallel templates)"

# Summary
echo -e "${GREEN}=== Benchmark Complete ===${NC}"
echo
echo "Recommendations:"
echo "  • Use 'copy' method for fastest creation"
echo "  • Use 'hybrid' for balanced performance"
echo "  • Use 'parallel' when templates aren't suitable"
echo
echo "Note: All methods create identical SQLite database files"