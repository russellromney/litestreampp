# Litestream Efficiency Analysis - Memory & Resource Bottlenecks

## Current Memory/Resource Intensive Components

### 1. WAL Position Tracking
- **Issue**: Each DB maintains full position map for all replicas
- **Memory**: ~1KB per replica × N replicas per DB
- **Fix**: Share position tracking across DBs, use single position file

### 2. Prometheus Metrics Per Database
```go
// Current: Each DB creates its own metric labels
dbSizeGauge.WithLabelValues(db.Path()).Set(float64(fi.Size()))
walSizeGauge.WithLabelValues(db.Path()).Set(float64(walSize))
```
- **Issue**: Prometheus keeps all label combinations in memory
- **Memory**: ~500 bytes per metric × 10 metrics × 100K DBs = 500MB+
- **Fix**: Aggregate metrics by tier/project instead of per-DB

### 3. Long-Running Read Transaction
```go
// Each DB holds open read transaction to prevent checkpointing
if err := db.acquireReadLock(); err != nil {
    return fmt.Errorf("acquire read lock: %w", err)
}
```
- **Issue**: Each hot DB keeps persistent transaction
- **Memory**: SQLite page cache per connection
- **Fix**: Use shared read lock across multiple DBs or periodic lock/unlock

### 4. File Descriptors
- **Current**: Each DB keeps 2+ file descriptors open (DB + WAL)
- **Issue**: OS limit ~65K file descriptors
- **Fix**: Already addressed with dynamic connections, but could batch operations

### 5. Goroutines Per Database
```go
// Each DB spawns:
go db.monitor()          // Monitor loop
go db.expireSnapshots()  // Snapshot cleanup
go replica.Start()       // Each replica
```
- **Issue**: 3-5 goroutines per hot DB = 5,000 goroutines for 1K DBs
- **Memory**: ~4KB stack per goroutine = 20MB
- **Fix**: Use worker pools instead of per-DB goroutines

### 6. Snapshot Storage
- **Issue**: Snapshots stored per-DB, lots of small files
- **Memory**: File metadata overhead
- **Fix**: Consolidated snapshot storage or memory-mapped files

### 7. Replica Client Connections
```go
// S3 client per replica
type S3ReplicaClient struct {
    client *s3.S3
    // ...
}
```
- **Issue**: Each replica creates own S3 client/connection pool
- **Memory**: ~100KB per S3 client
- **Fix**: Share S3 clients across replicas

### 8. WAL Header Caching
- **Issue**: Each DB reads and caches WAL headers repeatedly
- **Memory**: Redundant caching
- **Fix**: Shared WAL header cache with TTL

### 9. LTX File Tracking
```go
maxLTXFileInfos struct {
    sync.Mutex
    m map[int]*ltx.FileInfo
}
```
- **Issue**: Each DB tracks its own LTX files
- **Memory**: Grows unbounded over time
- **Fix**: Centralized LTX tracking with cleanup

### 10. Buffer Allocations
```go
// Lots of one-off buffer allocations
buf := make([]byte, 8192)
```
- **Issue**: Temporary buffers allocated per operation
- **Memory**: GC pressure
- **Fix**: Buffer pools (sync.Pool)

## Proposed Optimizations

### 1. Shared Resource Manager
```go
type SharedResourceManager struct {
    // Shared across all DBs
    metricsAggregator *MetricsAggregator
    connectionPool    *ConnectionPool
    s3ClientPool      *S3ClientPool
    bufferPool        *sync.Pool
    walHeaderCache    *Cache
}
```

### 2. Worker Pool Architecture
```go
type WorkerPool struct {
    monitorWorkers   chan monitorJob
    snapshotWorkers  chan snapshotJob
    replicaWorkers   chan replicaJob
}

// Instead of per-DB goroutines
pool.Submit(monitorJob{db: db})
```

### 3. File-Based Position Tracking
```go
// Use efficient file-based position tracking
type FilePositionTracker struct {
    file *os.File
    positions map[string]int64
    dirty bool
}

// Batch position updates to reduce I/O
func (f *FilePositionTracker) Flush() error {
    if !f.dirty {
        return nil
    }
    // Write all positions at once
    return f.writePositions()
}
```

### 4. Tiered Metrics
```go
// Instead of per-DB metrics
type TieredMetrics struct {
    hot  MetricSet
    cold MetricSet
    byProject map[string]*MetricSet
}
```

## Potential Memory Savings

| Component | Current (100K DBs) | Optimized | Savings |
|-----------|-------------------|-----------|---------|
| Metrics | 500MB | 5MB | 495MB |
| Goroutines | 20MB (5K) | 1MB (100) | 19MB |
| S3 Clients | 100MB (1K) | 1MB (10) | 99MB |
| WAL Headers | 100MB | 10MB | 90MB |
| Buffers | 50MB | 5MB | 45MB |
| **Total** | **770MB** | **22MB** | **748MB** |

## Implementation Priority

1. **Metrics Aggregation** - Biggest win, easiest to implement
2. **Worker Pools** - Reduces goroutine explosion
3. **Shared S3 Clients** - Simple connection pooling
4. **Buffer Pools** - Easy sync.Pool usage
5. **Connection Pooling** - Dynamic connection management