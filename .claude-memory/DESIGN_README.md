# Litestream Multi-Database Memory

This folder contains the design evolution and final architecture for supporting 100,000+ SQLite databases in Litestream.

## Design Evolution

1. **Initial Proposal** (`consolidated-design.md`) - Complex 3-tier system with hot/warm/cold tiers
2. **Simplified Two-Tier** (`simplified-two-tier-design.md`) - Hot (1s sync) and cold (no sync) 
3. **Implementation Analysis** (`implementation-analysis.md`) - Identified gotchas like WAL handling, DLQ persistence
4. **Refined Plan** (`refined-implementation-plan.md`) - Added fixes for identified issues
5. **Final Ultra-Simple** (`final-ultra-simple-design.md`) - 30-second cycle, no DLQ, massive cost savings

## Key Decisions

### What We're Building
- Single 30-second scan/sync cycle
- Size + mtime change detection  
- WAL-aware database reading
- LZ4 compression for all uploads
- 100 concurrent S3 uploads max
- No DLQ, no batching, no tiers

### Why Ultra-Simple?
- **Cost**: $432/day → $0.24/day (99.94% savings)
- **Code**: ~300 lines vs 3000+ lines
- **Operations**: Zero maintenance required
- **Reliability**: Natural retry every 30 seconds

### Tradeoffs
- 30-second max replication lag (vs 1 second)
- No immediate retry on failures
- Basic monitoring only

## Implementation Status

### Completed
- ✅ Core design and architecture
- ✅ Memory-efficient database tracking (32 bytes)
- ✅ Database discovery with glob patterns
- ✅ Proof of concept implementations
- ✅ Cost analysis and projections

### Ready to Build
The ultra-simple design is finalized and ready for integration into Litestream core.

## Files

- `final-ultra-simple-design.md` - **START HERE** - The final design to implement
- `consolidated-design.md` - Original complex design (historical)
- `simplified-two-tier-design.md` - Intermediate simplification
- `implementation-analysis.md` - Critical analysis of gotchas
- `refined-implementation-plan.md` - Fixed design (before ultra-simple)
- `ultra-simple-implementation.md` - Initial ultra-simple proposal

## Next Steps

Integration points with existing Litestream:
1. Add multi_db configuration section
2. Create UltraSimpleReplicator 
3. Hook into main replicate command
4. Add /stats endpoint