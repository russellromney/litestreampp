# S3 Integration for Litestream Multi-Database Demos

## Important: Current Demo Limitations

The basic demos (`demo_quick.go` and `demo_10k_databases.go`) **DO NOT** include S3 replication. They only test:
- Database discovery and tracking
- Hot/cold tier transitions
- Memory management
- Connection pooling

## What's Missing for S3 Replication

To actually replicate to S3, you need:

1. **S3 Credentials**
   - AWS_ACCESS_KEY_ID
   - AWS_SECRET_ACCESS_KEY
   - AWS_REGION

2. **S3 Bucket**
   - Must exist and be writable
   - Proper IAM permissions

3. **Replica Configuration**
   ```go
   replicaConfig := &litestream.ReplicaConfig{
       Type:   "s3",
       Bucket: "your-bucket",
       Path:   "litestream/{{project}}/{{database}}/",
       Region: "us-east-1",
       SyncInterval: 1 * time.Second,
   }
   ```

## Demo with S3 Replication

Use `demo_with_s3.go` for actual S3 replication testing:

### Option 1: Real AWS S3

```bash
# Set AWS credentials
export AWS_ACCESS_KEY_ID=your-key-id
export AWS_SECRET_ACCESS_KEY=your-secret-key
export AWS_REGION=us-east-1
export LITESTREAM_S3_BUCKET=your-bucket-name

# Run demo
go run demo_with_s3.go
```

### Option 2: LocalStack (S3 Emulator)

```bash
# Setup LocalStack
./setup_localstack.sh

# Set environment
export LITESTREAM_S3_BUCKET=litestream-test
export LITESTREAM_S3_ENDPOINT=http://localhost:4566
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_REGION=us-east-1

# Run demo
go run demo_with_s3.go

# Check uploaded files
aws --endpoint-url=http://localhost:4566 s3 ls s3://litestream-test/ --recursive
```

## What Happens with S3 Replication

When properly configured, Litestream will:

1. **For Hot Databases**:
   - Stream WAL segments to S3 in real-time
   - Upload format: `s3://bucket/path/wal/00000000-00000000.wal.lz4`
   - Create periodic snapshots

2. **For Cold Databases**:
   - Only create snapshots periodically
   - No active WAL streaming
   - Minimal S3 operations

3. **S3 Structure**:
   ```
   s3://your-bucket/
   └── litestream-demo/
       ├── project0/
       │   └── db0/
       │       ├── generations/
       │       │   └── 0000000000000000/
       │       │       ├── snapshots/
       │       │       │   └── 0000000000000000.snapshot.lz4
       │       │       └── wal/
       │       │           ├── 0000000000000000-0000000000000001.wal.lz4
       │       │           └── 0000000000000001-0000000000000002.wal.lz4
       │       └── metadata.json
       └── project1/
           └── ...
   ```

## Performance Impact of S3

With S3 replication enabled:
- **Network Usage**: ~1-10 MB/s for 100 active databases
- **S3 API Calls**: ~100-1000/minute for hot databases
- **Latency**: Adds 50-200ms to write operations
- **Cost**: ~$0.0004 per 1000 PUT requests

## Configuration in Production

For production use, configure in YAML:

```yaml
multi-db:
  enabled: true
  patterns:
    - "/data/*/databases/*.db"
  max-hot-databases: 1000
  replica-template:
    type: s3
    bucket: ${S3_BUCKET}
    path: "litestream/{{project}}/{{database}}/"
    region: ${AWS_REGION}
    access-key-id: ${AWS_ACCESS_KEY_ID}
    secret-access-key: ${AWS_SECRET_ACCESS_KEY}
    sync-interval: 1s
    retention: 24h
    retention-check-interval: 1h
```

## Testing Considerations

1. **LocalStack Limitations**:
   - No real persistence (data lost on restart)
   - No real S3 performance characteristics
   - Good for functional testing only

2. **Real S3 Testing**:
   - Use a dedicated test bucket
   - Set lifecycle rules to auto-delete old data
   - Monitor costs (especially PUT requests)

3. **Network Requirements**:
   - Stable internet connection
   - Low latency to S3 region
   - Sufficient bandwidth for WAL streaming

## Troubleshooting

### No Files in S3
- Check AWS credentials are set
- Verify bucket exists and is writable
- Check logs for S3 errors
- Ensure databases are in hot tier

### High S3 Costs
- Reduce sync-interval for cold databases
- Increase retention check interval
- Use S3 lifecycle rules
- Consider S3 Intelligent-Tiering

### Slow Replication
- Check network bandwidth
- Use S3 Transfer Acceleration
- Increase worker pool size
- Consider multi-region setup