# Proposal: Simple Pattern-Based Recovery for Litestream Multi-Database Support

## Executive Summary

This proposal outlines a straightforward bulk recovery system for Litestream that can efficiently restore thousands of databases using glob patterns. The system provides parallel restoration without complexity, making it easy to recover all databases under a given path with a single command.

## Problem Statement

Current Litestream recovery limitations:
- Can only restore one database at a time
- No support for bulk recovery operations
- Sequential recovery of many databases is time-consuming
- Must run individual restore commands for each database

## Proposed Solution

### 1. New `restore-pattern` Command

Add a new command that restores databases matching a pattern. Works in two modes:

#### Mode A: From Configuration (databases that exist in config)
```bash
# Restore all configured databases matching pattern
litestream restore-pattern "/data/**/*.db" -config litestream.yml

# Restore with custom parallelism  
litestream restore-pattern "/data/**/*.db" --parallel 20

# Restore to different base directory
litestream restore-pattern "/data/**/*.db" --output-dir "/restored"
```

#### Mode B: From S3 URL Pattern (discover from S3)
```bash
# Discover and restore all databases under S3 prefix
litestream restore-pattern "s3://mybucket/backups/project1/**/*.db"

# Restore specific project that doesn't exist locally
litestream restore-pattern "s3://mybucket/backups/myproject/" --output-dir "/data"

# With progress tracking
litestream restore-pattern "s3://mybucket/backups/**/*.db" --progress
```

### 2. Discovery Mechanism

#### For Configuration Mode:
- Read litestream.yml and find all configured databases
- Filter by glob pattern against database paths
- Use existing replica configuration for each database

#### For S3 URL Mode:
- List objects in S3 bucket with given prefix
- Identify database backups by LTX file structure
- Infer database paths from S3 object keys
- Create temporary replica clients for restoration

### 3. Simple Output Options

- **Default**: Restore to original locations (config mode) or inferred paths (S3 mode)
- **`--output-dir`**: Restore to different base directory, preserving relative structure
- **`--if-db-not-exists`**: Skip databases that already exist at destination

### 4. Parallel Execution

- Configurable worker pool (default: 10 parallel restores)
- Each database restored independently  
- No blocking between databases
- Automatic retry on transient failures (S3 rate limits, network issues)

### 5. Progress Tracking

```bash
$ litestream restore-pattern "/data/**/*.db" --progress

Discovering databases... found 1,543
Checking S3 availability... 1,541 available

Restoring databases:
[████████████████████████░░░░░░] 82% (1,264/1,541)
Speed: 127 MB/s | ETA: 4m | Errors: 2
```

### 6. Implementation Architecture

```go
// Simple recovery manager
type PatternRestoreCommand struct {
    Pattern      string
    OutputDir    string  
    Parallelism  int
    ShowProgress bool
    ConfigPath   string // optional, for config mode
}

// Workflow
func (c *PatternRestoreCommand) Run(ctx context.Context) error {
    var databases []DatabaseInfo
    
    // 1. Discover databases based on pattern type
    if isS3URL(c.Pattern) {
        // List S3 objects and identify database backups
        databases = discoverFromS3(ctx, c.Pattern)
    } else {
        // Read config and filter by pattern
        databases = discoverFromConfig(c.ConfigPath, c.Pattern)
    }
    
    // 2. Create worker pool
    pool := NewWorkerPool(c.Parallelism)
    
    // 3. Submit restore jobs
    for _, db := range databases {
        pool.Submit(func() {
            restoreDatabase(ctx, db, c.OutputDir)
        })
    }
    
    // 4. Wait for completion with progress
    pool.WaitWithProgress(c.ShowProgress)
}
```

### 7. Error Handling

- Continue on individual database failures (don't stop entire recovery)
- Log failed databases to a file for manual retry
- Summary report at the end showing success/failure counts
- Automatic retry with exponential backoff for transient errors

### 8. Configuration Integration

```yaml
# litestream.yml
restore-pattern:
  # Default parallelism for pattern restores
  parallel-workers: 10
  
  # Retry configuration
  max-retries: 3
  retry-delay: 5s
```

## Benefits

1. **Simple to Use** - One command to restore all databases
2. **Fast** - Parallel processing significantly reduces recovery time
3. **Reliable** - Automatic retries and error reporting
4. **Flexible** - Works with any directory structure via glob patterns
5. **No Assumptions** - Doesn't require specific naming conventions

## Implementation Plan

### Phase 1: Core Implementation (Week 1)
- Pattern discovery using existing glob support
- Basic parallel restoration using worker pool
- Integration with existing restore logic

### Phase 2: Progress & Error Handling (Week 2)
- Progress tracking and ETA calculation
- Error collection and reporting
- Retry logic for transient failures

### Phase 3: Testing & Documentation (Week 3)
- Testing with various database counts (10, 100, 1000+)
- CLI documentation
- Example usage patterns

## Resource Requirements

- **Memory**: ~1MB per 1000 databases for tracking
- **CPU**: Minimal (mostly I/O bound)
- **Network**: Configurable parallel S3 connections (default: 10)

## Success Metrics

1. **Recovery Time**: 10-100x faster than sequential restoration
2. **Reliability**: > 99% successful recovery rate with retries
3. **Simplicity**: Single command for bulk recovery

## Example Usage

### Restoring a Single Project from S3

```bash
# Project doesn't exist locally - discover and restore from S3
$ litestream restore-pattern "s3://mybucket/backups/myproject/" --output-dir "/data/myproject"
Discovering databases in S3... found 42
Restoring 42 databases to /data/myproject/
Completed in 2m 15s
```

### Restoring Multiple Projects

```bash
# Restore all databases under /data from config
$ litestream restore-pattern "/data/**/*.db" -config litestream.yml
Restored 1,543 databases in 12m 34s

# Restore all projects from S3 bucket
$ litestream restore-pattern "s3://mybucket/backups/**/*.db" --output-dir "/recovered"
Discovered 5,234 databases across 127 projects
Restoring with 10 parallel workers...
Completed in 48m 12s

# Restore with custom settings
$ litestream restore-pattern "/apps/*/tenant-*.db" \
  --parallel 20 \
  --output-dir "/recovery" \
  --progress

# Check for errors
$ cat litestream-restore-errors.log
Failed to restore: /apps/broken/tenant-123.db - backup not found
Failed to restore: /apps/test/tenant-999.db - checksum mismatch
```

## Conclusion

This simplified pattern-based recovery system provides an efficient, easy-to-use solution for restoring multiple databases without unnecessary complexity. It leverages Litestream's existing restore capabilities while adding parallel execution and progress tracking for bulk operations.