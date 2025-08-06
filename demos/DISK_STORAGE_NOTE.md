# Important: Disk-Based Storage for Demo Scripts

## Why Disk Storage?

The demo scripts have been updated to use **disk-based storage** instead of `/tmp` for the following critical reasons:

1. **Realistic Testing**: `/tmp` is often memory-backed (tmpfs/ramfs), which would not provide realistic I/O performance metrics
2. **Actual Disk I/O**: Production SQLite databases use disk storage, so demos should test real disk operations
3. **Memory Accuracy**: Using `/tmp` would hide the true memory usage by storing data in RAM
4. **Performance Validation**: Disk-based storage validates real-world performance characteristics

## Storage Locations

| Demo | Location | Disk Usage |
|------|----------|------------|
| Quick Demo (100 DBs) | `~/.litestream-demos/quick-demo/` | ~10 MB |
| 10K Demo | `~/.litestream-demos/10k-demo/` | ~1 GB |

## Performance Impact

Using disk storage provides accurate metrics for:
- **Write Latency**: Real disk write operations
- **Read Performance**: Actual file system reads
- **Cache Behavior**: OS page cache interactions
- **IOPS**: True input/output operations per second

## Cleanup

All demos automatically clean up disk storage on exit:
```bash
# Automatic cleanup on normal exit
✓ Cleanup complete - removed ~/.litestream-demos/quick-demo

# Manual cleanup if needed
rm -rf ~/.litestream-demos/

# Or use the cleanup utility
./cleanup.sh
```

## System Requirements

With disk-based storage:
- **Quick Demo**: 10 MB free disk space
- **10K Demo**: 1 GB free disk space
- **SSD Recommended**: For optimal performance testing

## Verification

You can verify databases are on disk:
```bash
# Check file type
file ~/.litestream-demos/quick-demo/project0/databases/db0/branches/main/tenants/tenant0.db
# Output: SQLite 3.x database...

# Check disk usage
du -sh ~/.litestream-demos/
# Output: 800K (for 100 DBs) or 1.0G (for 10K DBs)
```

## Important Notes

1. **Not Using /tmp**: Previous versions used `/tmp` which may be RAM-backed
2. **Home Directory**: Uses `~/.litestream-demos/` for persistent disk storage
3. **Hidden Directory**: Starts with `.` to keep home directory clean
4. **Auto-Cleanup**: Removes directory and parent if empty on exit