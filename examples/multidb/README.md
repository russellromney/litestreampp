# Multi-Database Example

This example demonstrates the new multi-database support in Litestream that can scale to 100,000 databases.

## Running the Example

1. **Build the example:**
```bash
go build -o multidb-demo main.go
```

2. **Run it:**
```bash
./multidb-demo
```

## What it Does

1. Creates a sample directory structure with 24 SQLite databases:
   ```
   data/
     project1/
       databases/
         userdb/
           branches/
             main/
               tenants/
                 acme.db
                 globex.db
                 initech.db
             feature/
               tenants/
                 acme.db
                 globex.db
                 initech.db
         orderdb/
           branches/
             ...
     project2/
       ...
   ```

2. Starts the MultiDBStore with database discovery

3. Shows statistics about discovered databases

4. Activates one database and performs some operations

5. Demonstrates path parsing to extract project/database/branch/tenant

## Key Features Demonstrated

- **Automatic Discovery**: Finds all databases matching the pattern
- **Minimal Memory**: Only ~240 bytes per database when inactive
- **Hot/Cold Tiers**: Active databases get full resources
- **Path Parsing**: Extract metadata from directory structure

## Running Tests

Run the comprehensive test suite:

```bash
# Basic functionality tests
go test -v -run TestMultiDBStore

# Large scale test (10,000 databases)
go test -v -run TestLargeScaleDiscovery

# Memory profiling
go test -v -run TestMemoryProfile

# Full 100K test (takes longer)
go test -v -run TestScaleToHundredThousand
```

## Configuration

In a real deployment, you would configure this in your `litestream.yml`:

```yaml
# Multi-database configuration
multi_db:
  discovery_pattern: "/data/*/databases/*/branches/*/tenants/*.db"
  max_active: 1000
  scan_interval: 30s
  replica_template:
    type: s3
    bucket: my-backup-bucket
    path: "{{project}}/{{database}}/{{branch}}/{{tenant}}"
```

## Performance

With the current implementation:
- Discovery: ~70ms per 1,000 databases
- Memory: ~240 bytes per database
- 100K databases: ~24MB total memory