#!/bin/bash

# run_demo.sh - Helper script to run Litestream multi-database demos

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Demo selection
DEMO_TYPE=${1:-quick}

echo -e "${GREEN}=== Litestream Multi-Database Demo Runner ===${NC}"
echo

# Check Go installation
if ! command -v go &> /dev/null; then
    echo -e "${RED}Error: Go is not installed${NC}"
    echo "Please install Go from https://golang.org/dl/"
    exit 1
fi

# Check SQLite driver
if ! go list -m github.com/mattn/go-sqlite3 &> /dev/null 2>&1; then
    echo -e "${YELLOW}Installing SQLite driver...${NC}"
    go get github.com/mattn/go-sqlite3
fi

# Function to cleanup
cleanup() {
    echo -e "\n${YELLOW}Cleaning up demo files...${NC}"
    
    # Kill any running demo processes
    pkill -f "go run demo_" 2>/dev/null || true
    
    # Remove demo directories from home directory
    HOME_DEMO_DIR="$HOME/.litestream-demos"
    if [ -d "$HOME_DEMO_DIR" ]; then
        echo -e "  Removing $HOME_DEMO_DIR"
        rm -rf "$HOME_DEMO_DIR"
    fi
    
    # Also clean any old /tmp directories
    for dir in /tmp/litestream-*-demo/; do
        if [ -d "$dir" ]; then
            echo -e "  Removing $dir (old location)"
            rm -rf "$dir"
        fi
    done
    
    echo -e "${GREEN}✓ Cleanup complete${NC}"
}

# Function to ensure cleanup even on forced exit
emergency_cleanup() {
    echo -e "\n${RED}Emergency cleanup triggered${NC}"
    cleanup
    exit 1
}

# Set trap for cleanup on exit
trap cleanup EXIT
trap emergency_cleanup INT TERM

# Run selected demo
case $DEMO_TYPE in
    quick)
        echo -e "${GREEN}Running Quick Demo (100 databases)${NC}"
        echo "This will take about 30 seconds..."
        echo
        go run demo_quick.go
        ;;
    
    10k)
        echo -e "${GREEN}Running 10K Database Demo${NC}"
        echo -e "${YELLOW}This will create 10,000 databases and may take several minutes${NC}"
        echo "Press Ctrl+C to stop the demo at any time"
        echo
        read -p "Continue? (y/n) " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            go run demo_10k_databases.go
        else
            echo "Demo cancelled"
            exit 0
        fi
        ;;
    
    stress)
        echo -e "${GREEN}Running Stress Test (10K databases with heavy writes)${NC}"
        echo -e "${YELLOW}WARNING: This is a heavy stress test${NC}"
        echo "System requirements:"
        echo "  - 4GB+ RAM"
        echo "  - 2GB+ free disk space"
        echo "  - May impact system performance"
        echo
        read -p "Continue? (y/n) " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            # Run 10k demo with aggressive parameters
            WRITE_PERCENTAGE=5 WRITE_INTERVAL=1s go run demo_10k_databases.go
        else
            echo "Stress test cancelled"
            exit 0
        fi
        ;;
    
    help|--help|-h)
        echo "Usage: ./run_demo.sh [demo_type]"
        echo
        echo "Available demos:"
        echo "  quick   - Quick test with 100 databases (default)"
        echo "  10k     - Full demo with 10,000 databases"
        echo "  stress  - Stress test with heavy write load"
        echo "  help    - Show this help message"
        echo
        echo "Examples:"
        echo "  ./run_demo.sh         # Run quick demo"
        echo "  ./run_demo.sh 10k     # Run 10K database demo"
        echo "  ./run_demo.sh stress  # Run stress test"
        exit 0
        ;;
    
    *)
        echo -e "${RED}Error: Unknown demo type '$DEMO_TYPE'${NC}"
        echo "Use './run_demo.sh help' for available options"
        exit 1
        ;;
esac

echo -e "\n${GREEN}✓ Demo completed successfully!${NC}"