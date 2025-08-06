#!/bin/bash

# Script to create a test scenario with configurable scale
# Usage: ./create_test_scenario.sh [num_projects] [test_dir]

NUM_PROJECTS=${1:-100}
TEST_DIR=${2:-.test-data}
DBS_PER_PROJECT=3
BRANCHES_PER_DB=2
TENANTS_PER_BRANCH=10

echo "Creating test scenario:"
echo "  Projects: $NUM_PROJECTS"
echo "  Databases per project: $DBS_PER_PROJECT"
echo "  Branches per database: $BRANCHES_PER_DB"
echo "  Tenants per branch: $TENANTS_PER_BRANCH"
echo "  Total databases: $((NUM_PROJECTS * DBS_PER_PROJECT * BRANCHES_PER_DB * TENANTS_PER_BRANCH))"
echo "  Directory: $TEST_DIR"
echo ""

# Create a sample structure for demonstration
mkdir -p "$TEST_DIR"

# Create just the first few projects as examples
for p in $(seq 0 2); do
    PROJECT="project$(printf "%05d" $p)"
    
    for db in users orders analytics; do
        for branch in main develop; do
            TENANT_DIR="$TEST_DIR/$PROJECT/databases/$db/branches/$branch/tenants"
            mkdir -p "$TENANT_DIR"
            
            # Create a few tenant databases
            for t in $(seq 0 2); do
                TENANT="tenant$(printf "%03d" $t)"
                DB_PATH="$TENANT_DIR/$TENANT.db"
                
                # Create minimal SQLite database
                sqlite3 "$DB_PATH" <<EOF
CREATE TABLE metadata (
    key TEXT PRIMARY KEY,
    value TEXT,
    updated_at INTEGER
);
INSERT INTO metadata VALUES 
    ('project', '$PROJECT', $(date +%s)),
    ('database', '$db', $(date +%s)),
    ('branch', '$branch', $(date +%s)),
    ('tenant', '$TENANT', $(date +%s));
EOF
            done
        done
    done
done

echo "Created sample structure. Full structure would contain:"
echo "  $NUM_PROJECTS project directories"
echo "  $((NUM_PROJECTS * DBS_PER_PROJECT * BRANCHES_PER_DB)) branch directories"
echo "  $((NUM_PROJECTS * DBS_PER_PROJECT * BRANCHES_PER_DB * TENANTS_PER_BRANCH)) SQLite databases"

# Show the structure
echo ""
echo "Sample structure:"
find "$TEST_DIR" -name "*.db" | head -20 | sort