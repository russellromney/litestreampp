# Litestream Memory Optimization Implementation Spec

## Executive Summary

This spec details the implementation of memory optimizations for Litestream to support 100K+ databases efficiently. The changes focus on reducing per-database memory overhead through shared resources, hierarchical metrics, and dynamic connection management while maintaining backward compatibility.

## Core Changes Overview

### 1. Metrics System Overhaul
Replace per-database Prometheus metrics with hierarchical aggregation to reduce memory from 500MB to 3MB for 100K databases.

### 2. Worker Pool Architecture  
Replace per-database goroutines (3-5 per DB) with shared worker pools, reducing goroutine count from 300K+ to ~350 total.

### 3. Connection Pool Management
Implement dynamic connection pooling with 5-second idle timeout, keeping connections only for actively syncing databases.

### 4. Shared S3 Client Pool
Replace per-replica S3 clients with a shared pool of 200 clients, reducing memory from 100MB to 20MB.

### 5. Hot/Cold Tier System
Implement write-based hot/cold detection with 15-second scanning intervals, promoting only modified databases to hot tier.

## Implementation Plan

### Phase 1: Core Infrastructure (Week 1)

#### 1.1 Shared Resource Manager
- Create `shared_resources.go` with pools for workers, S3 clients, and buffers
- Implement worker pools for monitor (100), snapshot (50), and replica (200) tasks
- Add buffer pool using sync.Pool for temporary allocations

#### 1.2 Hierarchical Metrics
- Modify existing metrics in `db.go` to remove per-database labels
- Create new aggregated metrics structure tracking project/database levels only
- Update metric recording to aggregate at collection time

### Phase 2: Connection and Client Management (Week 2)

#### 2.1 Connection Pool
- Implement connection pool in existing code (not new file)
- Add 5-second idle timeout with background cleanup goroutine
- Use read-only connections for hot databases
- Track connection usage statistics

#### 2.2 S3 Client Pool  
- Modify replica code to use shared S3 clients
- Implement pool with 200 clients maximum
- Add client checkout/return mechanism
- Handle regional client requirements

### Phase 3: Hot/Cold Tier Logic (Week 3)

#### 3.1 Write Detection System
- Add 15-second scan loop to check mtime/size changes
- Implement hot promotion for modified databases
- Add automatic demotion after 15 seconds of inactivity
- Persist hot database list to S3 for failover

#### 3.2 Dynamic Database Lifecycle
- Modify DB struct to support lazy initialization
- Add connection open/close based on hot/cold state
- Implement state transitions without data loss

### Phase 4: Integration and Testing (Week 4)

#### 4.1 Backward Compatibility
- Ensure existing single-database configs work unchanged
- Add multi-db detection in replicate command
- Test migration from standard to multi-db mode

#### 4.2 Performance Testing
- Create test harness for 10K+ databases
- Measure memory usage vs baseline
- Verify 1-second sync intervals maintained
- Test failover/recovery scenarios

## Technical Details

### Memory Targets
- Cold database: 240 bytes (just tracking info)
- Hot database: ~50KB (down from 500KB)
- 100K total with 1K hot: 123MB total

### Resource Limits
- Max connections: 1000 (configurable)
- Worker goroutines: ~350 total
- S3 clients: 200 shared
- File descriptors: 2 × hot databases

### Configuration Changes
Add new `multi-db` section to config while maintaining backward compatibility:
- Pattern-based database discovery
- Configurable scan intervals (default 15s)
- Resource limits (max hot databases)
- Write detection parameters

## Testing Strategy

### Unit Tests
- Test each pool implementation (connection, S3, worker)
- Verify metric aggregation logic
- Test hot/cold state transitions

### Integration Tests  
- Multi-database discovery
- Connection lifecycle management
- Failover with hot list recovery
- Memory usage validation

### Load Tests
- 10K databases with varying write patterns
- Measure CPU usage of scanning
- Verify connection pool efficiency
- Test S3 throughput limits

## Risk Mitigation

### Data Loss Prevention
- Never close connections during active transactions
- Buffer writes during recovery
- Maintain WAL position tracking

### Performance Degradation
- Monitor scan loop CPU usage
- Implement parallel scanning if needed
- Add circuit breakers for overload

### Backward Compatibility
- Detect single vs multi-db mode automatically
- Preserve all existing functionality
- No changes to existing APIs

## Success Criteria

1. Support 100K+ databases on single instance
2. Memory usage < 200MB for 100K databases with 1K hot
3. Maintain 1-second sync intervals for hot databases
4. No regression in single-database performance
5. Complete backward compatibility

## Non-Goals

- Batch operations (keeping operations independent)
- Distributed coordination between instances
- Changes to SQLite interaction patterns
- Major API or configuration changes

This implementation focuses on practical optimizations that provide immediate benefit without adding unnecessary complexity.