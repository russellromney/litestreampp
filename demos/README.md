# Litestream Multi-Database Demo Scripts

Demonstration scripts showcasing Litestream's ability to efficiently manage thousands of SQLite databases using the new hot/cold tier system.

## Quick Start

```bash
# Run the quick demo (100 databases) - LOCAL ONLY, NO S3
./run_demo.sh

# Run the 10K database demo - LOCAL ONLY, NO S3
./run_demo.sh 10k

# Run demo with S3 replication (requires AWS/LocalStack setup)
go run demo_with_s3.go

# Show help
./run_demo.sh help
```

## ⚠️ Important: S3 Replication Not Included

The basic demos (`demo_quick.go` and `demo_10k_databases.go`) test **local functionality only**:
- ✅ Database discovery and hot/cold management
- ✅ Memory optimization and connection pooling
- ❌ **NO S3 replication** (requires additional setup)

For S3 replication testing, see:
- `demo_with_s3.go` - Demo with actual S3 configuration
- `S3_INTEGRATION.md` - Complete S3 setup guide
- `setup_localstack.sh` - Test S3 locally without AWS

## Storage Location

**Important**: Demo databases are created on disk at `~/.litestream-demos/` to ensure actual disk I/O testing, not memory-backed storage. This provides realistic performance metrics for production workloads.

## Creation Optimization

The demos use optimized creation methods for speed:

### Template Copying (Fastest)
- Creates one template SQLite database
- Copies template to all locations in parallel
- **Speed**: ~9,000 databases/second
- **Use case**: Identical schema across all databases

### Parallel Workers
- Creates databases using worker pool
- Each worker creates complete database
- **Speed**: ~2,500 databases/second
- **Use case**: Different schemas or initial data

### Hybrid Approach
- Creates template per project
- Parallel copying within projects
- **Speed**: ~8,700 databases/second
- **Use case**: Project-specific templates

## Demo Scripts

### 1. Quick Demo (demo_quick.go)
- **Databases**: 100
- **Duration**: ~30 seconds
- **Purpose**: Quick validation and testing
- **Memory Usage**: ~5-10MB

### 2. 10K Database Demo (demo_10k_databases.go)
- **Databases**: 10,000
- **Duration**: Continuous (Ctrl+C to stop)
- **Purpose**: Production-scale testing
- **Memory Usage**: ~120-150MB
- **Creation Time**: ~1-2 seconds (optimized with template copying)

### 3. Fast Setup Demo (demo_fast_setup.go)
- **Databases**: 10,000
- **Creation Methods**: Template copy, parallel, or hybrid
- **Creation Speed**: ~9,000 databases/second (copy method)
- **Purpose**: Benchmark different creation strategies

## Expected Results

| Databases | Traditional Memory | Optimized Memory | Savings | Disk Space |
|-----------|-------------------|------------------|---------|------------|
| 100       | 50 MB            | 5 MB             | 90%     | ~10 MB     |
| 1,000     | 500 MB           | 15 MB            | 97%     | ~100 MB    |
| 10,000    | 5,000 MB         | 150 MB           | 97%     | ~1 GB      |

## Cleanup

### Automatic Cleanup
All demo scripts automatically clean up their temporary files when they exit normally:
- Database files are removed
- Demo directories are deleted
- Resources are released

### Manual Cleanup
If a demo is interrupted or crashes, use the cleanup utility:

```bash
# Interactive cleanup utility
./cleanup.sh

# Or manually remove directories
rm -rf ~/.litestream-demos/
```

### Features
- **Automatic**: Cleanup on normal exit via defer statements
- **Signal Handling**: Graceful shutdown on Ctrl+C
- **Cleanup Utility**: Interactive script to remove all demo files
- **Process Management**: Terminates hanging demo processes
EOF < /dev/null