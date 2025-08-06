# Ultra-Simple Replicator - Standalone Tool

## What is it?

A standalone tool that implements the ultra-simple design for replicating 100,000+ SQLite databases to S3 with minimal cost and complexity.

## How to use it

### 1. Build
```bash
cd ultrasimple
./build.sh
```

### 2. Run
```bash
# Dry run to test
./ultrasimple -dry-run -pattern "/your/databases/*.db"

# Real S3 upload
./ultrasimple -bucket your-bucket -pattern "/your/databases/*.db"
```

## Key Features

- **Standalone binary** - No Litestream integration needed
- **30-second cycles** - Scans and syncs together
- **Change detection** - Only uploads modified databases
- **WAL-aware** - Handles SQLite safely
- **Cost efficient** - 99.94% cheaper than 1-second sync

## Common Use Cases

### 1. Multi-tenant SaaS
```bash
./ultrasimple \
  -bucket saas-backups \
  -pattern "/data/tenants/*/databases/*.db" \
  -path "{{tenant}}/{{database}}"
```

### 2. Development Backups
```bash
./ultrasimple \
  -bucket dev-backups \
  -pattern "/home/*/projects/*/data/*.db" \
  -interval 5m
```

### 3. High-volume Applications
```bash
./ultrasimple \
  -bucket prod-backups \
  -pattern "/var/lib/myapp/shards/*/data/*.db" \
  -concurrent 50 \
  -interval 1m
```

## Production Deployment

### As a systemd service:
```bash
sudo cp ultrasimple /usr/local/bin/
sudo cp ultrasimple.service /etc/systemd/system/
sudo systemctl enable ultrasimple
sudo systemctl start ultrasimple
```

### With Docker:
```bash
docker build -t ultrasimple .
docker run -d \
  -v /data:/data:ro \
  -e AWS_REGION=us-east-1 \
  ultrasimple -bucket prod-backups -pattern "/data/*/db/*.db"
```

## Monitoring

Watch the logs:
```bash
journalctl -u ultrasimple -f
```

Example output:
```
2024/01/15 10:30:00 Scan complete: 50000 databases, 523 synced (took 78ms)
2024/01/15 10:30:30 Scan complete: 50000 databases, 412 synced (took 82ms)
```

## Cost Example

For 100,000 databases with 1% change rate:
- Uploads per day: 48,000
- S3 PUT cost: $0.24/day
- Monthly cost: ~$7.20

Compare to Litestream's 1-second sync: ~$13,000/month

## When to Use This vs Litestream

**Use Ultra-Simple when:**
- You have many databases (100s to 100,000s)
- 30-second replication lag is acceptable
- Cost is a primary concern
- Databases change infrequently

**Use Litestream when:**
- You have few databases (1-10)
- You need sub-second replication
- You need point-in-time recovery
- You need streaming replication

## Files

- `replicator.go` - Core implementation (245 lines)
- `compress.go` - LZ4 helper (19 lines)
- `cmd/ultrasimple/main.go` - CLI tool
- `USAGE.md` - Detailed usage guide
- `README.md` - Technical documentation

Total implementation: 264 lines of code