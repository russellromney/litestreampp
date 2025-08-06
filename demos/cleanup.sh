#!/bin/bash

# cleanup.sh - Utility to clean up all Litestream demo files

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}=== Litestream Demo Cleanup Utility ===${NC}"
echo

# Function to get directory size
get_size() {
    if [ -d "$1" ]; then
        du -sh "$1" 2>/dev/null | cut -f1 || echo "unknown"
    else
        echo "0"
    fi
}

# Find all demo directories (now in home directory)
HOME_DEMO_DIR="$HOME/.litestream-demos"
DEMO_DIRS=""

# Check home directory location
if [ -d "$HOME_DEMO_DIR" ]; then
    DEMO_DIRS="$HOME_DEMO_DIR"
fi

# Also check for any old /tmp directories
TMP_DIRS=$(find /tmp -maxdepth 1 -name "litestream-*-demo" -type d 2>/dev/null || true)
if [ -n "$TMP_DIRS" ]; then
    DEMO_DIRS="$DEMO_DIRS $TMP_DIRS"
fi

if [ -z "$DEMO_DIRS" ]; then
    echo -e "${GREEN}✓ No demo directories found. System is clean.${NC}"
    exit 0
fi

# Count and show directories
COUNT=$(echo "$DEMO_DIRS" | wc -l | tr -d ' ')
echo -e "${YELLOW}Found $COUNT demo directory(ies) to clean:${NC}"
echo

# List directories with sizes
TOTAL_SIZE=0
for dir in $DEMO_DIRS; do
    SIZE=$(get_size "$dir")
    echo -e "  • $dir (${SIZE})"
done

echo
read -p "Do you want to remove these directories? (y/n) " -n 1 -r
echo

if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo -e "${YELLOW}Cleanup cancelled${NC}"
    exit 0
fi

# Perform cleanup
echo -e "\n${YELLOW}Cleaning up...${NC}"

for dir in $DEMO_DIRS; do
    echo -n "  Removing $dir... "
    if rm -rf "$dir" 2>/dev/null; then
        echo -e "${GREEN}✓${NC}"
    else
        echo -e "${RED}✗ Failed${NC}"
    fi
done

# Also kill any hanging demo processes
echo -e "\n${YELLOW}Checking for running demo processes...${NC}"
PROCS=$(pgrep -f "demo_.*\.go" || true)

if [ -n "$PROCS" ]; then
    echo "Found running demo processes: $PROCS"
    echo "Killing processes..."
    pkill -f "demo_.*\.go" || true
    echo -e "${GREEN}✓ Processes terminated${NC}"
else
    echo -e "${GREEN}✓ No running demo processes found${NC}"
fi

# Final verification
echo -e "\n${GREEN}=== Cleanup Complete ===${NC}"

# Check if any directories remain
REMAINING=$(find /tmp -maxdepth 1 -name "litestream-*-demo" -type d 2>/dev/null || true)
if [ -z "$REMAINING" ]; then
    echo -e "${GREEN}✓ All demo directories successfully removed${NC}"
else
    echo -e "${RED}⚠ Warning: Some directories could not be removed:${NC}"
    echo "$REMAINING"
fi