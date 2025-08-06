# Using Ultra-Simple Replicator as a Standalone Tool

## Quick Start

### 1. Build the tool

```bash
cd ultrasimple
./build.sh
```

This creates an executable `ultrasimple` in the current directory.

### 2. Test with dry run

```bash
# See what databases would be discovered
./ultrasimple -dry-run -pattern "/path/to/your/databases/*.db"
```

### 3. Run with S3

```bash
# Basic usage (uses AWS default credentials)
./ultrasimple -bucket my-backup-bucket -pattern "/data/*/databases/*.db"

# With explicit credentials
./ultrasimple \
  -bucket my-backup-bucket \
  -access-key YOUR_ACCESS_KEY \
  -secret-key YOUR_SECRET_KEY \
  -pattern "/data/*/databases/*.db"
```

## Command Line Options

```
-pattern string
    Database discovery pattern (default "/data/*/databases/*/branches/*/tenants/*.db")

-interval duration
    Scan and sync interval (default 30s)

-bucket string
    S3 bucket name (required unless -dry-run)

-region string
    AWS region (default "us-east-1")

-path string
    S3 path template (default "{{project}}/{{database}}/{{branch}}/{{tenant}}")

-concurrent int
    Maximum concurrent uploads (default 100)

-access-key string
    AWS access key (uses default credentials if not set)

-secret-key string
    AWS secret key (uses default credentials if not set)

-dry-run
    Scan only, don't upload
```

## Examples

### Development Testing
```bash
# See what would be uploaded without actually uploading
./ultrasimple -dry-run -pattern "./test-data/**/*.db"
```

### Production with Custom Path
```bash
./ultrasimple \
  -bucket production-backups \
  -pattern "/var/lib/myapp/*/data/*.db" \
  -path "myapp/{{project}}/{{database}}" \
  -interval 1m
```

### High-Volume with Reduced Concurrency
```bash
./ultrasimple \
  -bucket high-volume-backups \
  -pattern "/data/tenants/*/databases/*.db" \
  -concurrent 50 \
  -interval 5m
```

## Running as a Service

### systemd Service

Create `/etc/systemd/system/ultrasimple.service`:

```ini
[Unit]
Description=Ultra-Simple Database Replicator
After=network.target

[Service]
Type=simple
User=litestream
ExecStart=/usr/local/bin/ultrasimple -bucket my-backups -pattern "/data/*/databases/*.db"
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

Enable and start:
```bash
sudo systemctl enable ultrasimple
sudo systemctl start ultrasimple
sudo systemctl status ultrasimple
```

### Docker

Create a Dockerfile:
```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o ultrasimple ./cmd/ultrasimple

FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=builder /app/ultrasimple /usr/local/bin/
ENTRYPOINT ["ultrasimple"]
```

Run:
```bash
docker build -t ultrasimple .
docker run -v /data:/data ultrasimple \
  -bucket my-backups \
  -pattern "/data/*/databases/*.db"
```

## Monitoring

The tool logs statistics every scan cycle:
```
2024/01/15 10:30:00 Scan complete: 1000 databases, 25 synced (took 157ms)
```

For production monitoring, parse these logs or extend the tool to expose metrics.

## Cost Estimation

With default 30-second interval:
- 100K databases, 1% change rate = 48K uploads/day
- S3 PUT cost: $0.005 per 1,000 requests
- Daily cost: ~$0.24
- Monthly cost: ~$7.20

Compare to 1-second sync: ~$13,000/month

## Tips

1. **Start with dry run** to verify your pattern matches expected databases
2. **Monitor logs** initially to ensure proper operation
3. **Adjust interval** based on your replication lag tolerance
4. **Set concurrent uploads** based on your network capacity
5. **Use IAM roles** instead of keys when running on AWS

## Troubleshooting

### No databases found
- Check your pattern with `-dry-run`
- Ensure the user has read permissions
- Try an absolute path pattern

### S3 upload errors
- Verify bucket exists and credentials have write access
- Check AWS region is correct
- Reduce `-concurrent` if getting rate limited

### High memory usage
- Each database tracking uses ~240 bytes
- 100K databases = ~24MB
- Reduce pattern scope if needed