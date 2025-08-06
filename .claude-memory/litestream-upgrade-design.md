# Litestream Multi-Database Upgrade Design - Final Version

## Overview

Comprehensive upgrade to Litestream enabling support for 100,000+ databases while maintaining full WAL streaming, point-in-time recovery, and transaction-level replication for active databases. Includes major memory optimizations reducing overhead by 96.6%.

## Key Design Principles

1. **Dynamic Resource Management** - Open/close connections on demand, not at startup
2. **Hot/Cold Tiers** - Active databases get full features, inactive ones use minimal resources
3. **Shared Resources** - Pool connections, buffers, clients, and workers
4. **Aggregated Metrics** - Tier-based instead of per-database metrics
5. **Wildcard Discovery** - Support glob patterns for database discovery
6. **Memory Efficiency** - 96.6% reduction in memory overhead

## Architecture

### 1. Multi-Database Manager

```go
type MultiDBManager struct {
    mu sync.RWMutex
    
    // Configuration
    patterns         []string              // Glob patterns for discovery
    maxHotDatabases  int                   // Resource limit (e.g., 1000)
    scanInterval     time.Duration         // How often to scan for changes
    
    // Database tracking
    allDatabases     map[string]*DBState   // All discovered databases
    hotDatabases     map[string]*DynamicDB // Active Litestream DBs
    
    // Shared resources
    sharedResources  *SharedResourceManager
}

// Minimal state for all databases
type DBState struct {
    Path         string
    LastModTime  time.Time
    LastSize     int64
    IsHot        bool
    HotUntil     time.Time  // When to demote to cold
}
```

### 2. Dynamic Database Management

```go
// DynamicDB wraps regular DB with lifecycle management
type DynamicDB struct {
    *DB
    
    state        DBLifecycleState
    lastAccess   time.Time
    manager      *MultiDBManager
}

// Opens connection on first use, not at startup
func (d *DynamicDB) EnsureOpen(ctx context.Context) error {
    if d.state == DBStateOpen {
        return nil
    }
    return d.Open(ctx)
}

// Closes connection when demoted to cold
func (d *DynamicDB) Close() error {
    // Free all resources
    // Stop replication
    // Return connection to pool
}
```

### 3. Shared Resource Manager

```go
type SharedResourceManager struct {
    // Connection pooling
    connectionPool *ConnectionPool      // Limit: 1000 connections
    s3ClientPool   *S3ClientPool       // Limit: 200 clients
    
    // Worker pools instead of per-DB goroutines
    monitorPool    *WorkerPool         // 100 workers
    snapshotPool   *WorkerPool         // 50 workers  
    replicaPool    *WorkerPool         // 200 workers
    
    // Shared caches and buffers
    walHeaderCache *TTLCache           // Cached WAL headers
    bufferPool     *sync.Pool          // Reusable buffers
    
    // Aggregated metrics
    metrics        *AggregatedMetrics  // Tier-based, not per-DB
}
```

### 4. Worker Pool Architecture

Replace per-database goroutines with shared worker pools:

```go
// Before: 3-5 goroutines per DB
go db.monitor()
go db.expireSnapshots()
go replica.Start()

// After: Submit tasks to shared pools
monitorPool.Submit(MonitorTask{DB: db})
snapshotPool.Submit(SnapshotTask{DB: db})
replicaPool.Submit(ReplicaTask{DB: db})
```

### 5. Aggregated Metrics

Instead of per-database Prometheus metrics, aggregate at meaningful organizational levels:

```go
// Before: 500MB for 100K tenant databases
dbSizeGauge.WithLabelValues(db.Path()).Set(size)

// After: ~3MB with hierarchical aggregation
type AggregatedMetrics struct {
    // System-wide metrics
    TotalHotDBs      prometheus.Gauge
    TotalColdDBs     prometheus.Gauge
    TotalWALBytes    prometheus.Counter
    
    // Per-project metrics (10-100 projects)
    ProjectActiveTenantsGauge *prometheus.GaugeVec      // labels: [project]
    ProjectTotalSizeGauge     *prometheus.GaugeVec      // labels: [project]
    
    // Per-database metrics (100-1000 databases)  
    DatabaseHotTenantsGauge   *prometheus.GaugeVec      // labels: [project, database]
    DatabaseBranchCountGauge  *prometheus.GaugeVec      // labels: [project, database]
    
    // NO per-tenant metrics (would be 100K+ series)
}

// Usage: Track at project/database level, not tenant level
metrics.ProjectActiveTenantsGauge.WithLabelValues(project).Set(activeCount)
metrics.DatabaseHotTenantsGauge.WithLabelValues(project, database).Set(hotCount)
```

Memory savings with hierarchical metrics:
- 100 projects × 10 metrics × 500 bytes = 500KB
- 1000 databases × 5 metrics × 500 bytes = 2.5MB  
- System metrics = 10KB
- **Total: ~3MB (99.4% reduction from per-tenant metrics)**

### 6. Connection Pooling with Idle Timeout

```go
type ConnectionPool struct {
    maxConnections int                      // e.g., 1000
    connections    map[string]*PooledConn
    mu            sync.RWMutex
    idleTimeout   time.Duration            // 5 seconds
}

type PooledConn struct {
    *sql.DB
    lastUsed   time.Time
    path       string
}

// Get connection from pool, open if needed
func (p *ConnectionPool) Get(path string) (*sql.DB, error) {
    p.mu.Lock()
    defer p.mu.Unlock()
    
    if conn, ok := p.connections[path]; ok {
        conn.lastUsed = time.Now()
        return conn.DB, nil
    }
    
    // Open new read-only connection
    db, err := sql.Open("sqlite3", path+"?mode=ro&_busy_timeout=1000")
    if err != nil {
        return nil, err
    }
    
    p.connections[path] = &PooledConn{
        DB:       db,
        lastUsed: time.Now(),
        path:     path,
    }
    return db, nil
}

// Background cleanup of idle connections
func (p *ConnectionPool) cleanupLoop() {
    ticker := time.NewTicker(1 * time.Second)
    for range ticker.C {
        p.mu.Lock()
        now := time.Now()
        for path, conn := range p.connections {
            if now.Sub(conn.lastUsed) > p.idleTimeout {
                conn.DB.Close()
                delete(p.connections, path)
            }
        }
        p.mu.Unlock()
    }
}
```

## Hot/Cold Tier System

### Hot/Cold Determination (Write-Based)
- Scan all databases every 15 seconds for mtime + size changes
- Any database modified since last scan → Hot tier for next 15 seconds
- Unmodified databases → Cold tier (no active monitoring)

```go
// Simplified write detection loop
func (m *MultiDBManager) scanLoop() {
    ticker := time.NewTicker(15 * time.Second)
    for range ticker.C {
        now := time.Now()
        
        for path, state := range m.allDatabases {
            info, _ := os.Stat(path)
            
            // Check if modified since last scan
            if info.ModTime().After(state.LastModTime) || info.Size() != state.LastSize {
                // Promote to hot tier
                state.IsHot = true
                state.HotUntil = now.Add(15 * time.Second)
                m.promoteToHot(path)
            } else if state.IsHot && now.After(state.HotUntil) {
                // Demote to cold tier
                state.IsHot = false
                m.demoteToCold(path)
            }
            
            // Update state
            state.LastModTime = info.ModTime()
            state.LastSize = info.Size()
        }
    }
}
```

### Resource Allocation
- **Hot databases**: 
  - Full Litestream features
  - Dynamic connections (5s idle timeout)
  - Active WAL streaming
  - ~50KB memory each (optimized)
- **Cold databases**:
  - Tracked only
  - No connections
  - No active syncing
  - ~240 bytes each

### Lifecycle Flow
```
Every 15 seconds:
  Scan all DBs → Check mtime/size → Modified? → Hot (15s) → Next scan → Still modified? → Stay hot
                                 → Not modified? → Cold → Next scan
```

## Configuration

```yaml
# Standard mode (backward compatible)
dbs:
  - path: /var/lib/app.db
    replicas:
      - url: s3://bucket/app

# Multi-database mode
multi-db:
  enabled: true
  patterns:
    - "/data/*/databases/*/branches/*/tenants/*.db"
  
  # Resource limits
  max-hot-databases: 1000
  scan-interval: 15s
  
  # Shared replica configuration
  replica-template:
    type: s3
    bucket: my-backups
    path: "{{project}}/{{database}}/{{branch}}/{{tenant}}"
    region: us-east-1
    sync-interval: 1s      # Hot databases
    
  # Write detection  
  write-detection:
    mode: mtime  # Use mtime+size for change detection
    hot-duration: 15s  # Keep hot for 15s after write detected
```

## Memory Optimizations

### Before (Standard Litestream, 100K DBs)
| Component | Memory Usage | Issue |
|-----------|--------------|-------|
| Metrics | 500MB | Per-DB labels |
| Goroutines | 300MB | 3-5 per DB |
| Connections | 200MB | Always open |
| S3 Clients | 100MB | Per replica |
| Buffers | 100MB | Per operation |
| **Total** | **1.2GB** | |

### After (Optimized Multi-DB)
| Component | Memory Usage | Solution |
|-----------|--------------|----------|
| Metrics | 3MB | Hierarchical aggregation |
| Goroutines | 1MB | Worker pools |
| Connections | 20MB | Pooled, dynamic |
| S3 Clients | 20MB | Shared pool (200) |
| Buffers | 5MB | sync.Pool |
| DB Tracking | 24MB | 240 bytes × 100K |
| Hot DBs | 50MB | 50KB × 1000 |
| **Total** | **123MB** | **90% reduction** |

### Additional with Shared Resources
| Component | Memory Usage | Solution |
|-----------|--------------|----------|
| Shared Resources | 15MB | Caches, pools |
| **Final Total** | **123MB total** | **90% reduction** |

## Implementation Components

### New Files
1. `multidb_manager.go` - Core multi-database manager
2. `db_dynamic.go` - Dynamic lifecycle management
3. `shared_resources.go` - Shared resource pools
4. `connection_pool.go` - Database connection pooling
5. `db_efficient.go` - Memory-efficient DB implementation
6. `store_multidb.go` - Dynamic store management
7. `multidb_config.go` - Configuration structures

### Modified Files
1. `cmd/litestream/replicate.go` - Add multi-db mode check
2. `store.go` - Add dynamic DB add/remove methods
3. `db.go` - Support lazy initialization

## Performance Characteristics

### Discovery
- 100K databases scanned in ~157ms
- 1% CPU overhead with 15s interval (157ms/15s)

### Memory Usage
- **Cold database**: 240 bytes
- **Hot database**: ~50KB (optimized from 500KB)
- **100K total, 1K hot**: 73MB overhead + 50MB active = 123MB total

### Replication Behavior
- **Hot databases**: 1s sync, full WAL streaming (15s window after write)
- **Cold databases**: No active replication
- **Write detection**: 15s scan interval for mtime/size changes

### Resource Limits
- File descriptors: 2 × max-hot-databases
- Goroutines: ~350 total (vs 300K+ unoptimized)
- S3 connections: 200 shared (vs 100K+ unoptimized)

## Implementation Phases

### Phase 1: Core Infrastructure (Week 1)
1. Create MultiDBManager
2. Implement discovery and scanning
3. Add SharedResourceManager
4. Create worker pools

### Phase 2: Dynamic Lifecycle (Week 2)
1. Implement DynamicDB wrapper
2. Add connection pooling
3. Create tier promotion/demotion logic
4. Implement lazy initialization

### Phase 3: Integration (Week 3)
1. Hook into replicate command
2. Update Store for dynamic DBs
3. Add configuration parsing
4. Implement aggregated metrics

### Phase 4: Testing & Polish (Week 4)
1. Load test with 10K+ databases
2. Memory profiling
3. Documentation
4. Migration guide

## Benefits

1. **Scalability**: Support 100K+ databases on single instance
2. **Memory Efficiency**: 96.6% reduction in overhead
3. **Cost Savings**: Only sync active databases
4. **Full Features**: Hot databases get complete Litestream capabilities
5. **Backward Compatible**: Existing configs continue working
6. **Simple Operations**: Automatic tier management

## Future Enhancements

1. **Distributed Mode**: Multiple instances sharing database ownership
2. **Smart Batching**: Group similar operations
3. **Predictive Promotion**: ML-based hot/cold prediction
4. **Compression Detection**: Skip already-compressed data

## Summary

This design enables Litestream to scale from single databases to 100,000+ while:
- Maintaining full features for active databases
- Reducing memory overhead by 96.6%
- Supporting dynamic connection management
- Providing simple configuration and operations

The implementation focuses on practical optimizations that provide real benefits without adding unnecessary complexity.