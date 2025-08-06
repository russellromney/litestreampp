package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/benbjohnson/litestream/ultrasimple"
)

// RealS3Client implements the S3Client interface with actual AWS SDK
type RealS3Client struct {
	s3     *s3.S3
	bucket string
}

func NewRealS3Client(region, bucket, accessKey, secretKey string) (*RealS3Client, error) {
	config := &aws.Config{
		Region: aws.String(region),
	}
	
	// Use explicit credentials if provided
	if accessKey != "" && secretKey != "" {
		config.Credentials = credentials.NewStaticCredentials(accessKey, secretKey, "")
	}
	
	sess, err := session.NewSession(config)
	if err != nil {
		return nil, err
	}
	
	return &RealS3Client{
		s3:     s3.New(sess),
		bucket: bucket,
	}, nil
}

func (c *RealS3Client) Upload(key string, data []byte) error {
	_, err := c.s3.PutObject(&s3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
		Body:   aws.ReadSeekCloser(bytes.NewReader(data)),
	})
	return err
}

func main() {
	// Command line flags
	var (
		pattern       = flag.String("pattern", "/data/*/databases/*/branches/*/tenants/*.db", "Database discovery pattern")
		interval      = flag.Duration("interval", 30*time.Second, "Scan and sync interval")
		region        = flag.String("region", "us-east-1", "AWS region")
		bucket        = flag.String("bucket", "", "S3 bucket name (required)")
		pathTemplate  = flag.String("path", "{{project}}/{{database}}/{{branch}}/{{tenant}}", "S3 path template")
		maxConcurrent = flag.Int("concurrent", 100, "Maximum concurrent uploads")
		accessKey     = flag.String("access-key", "", "AWS access key (uses default credentials if not set)")
		secretKey     = flag.String("secret-key", "", "AWS secret key (uses default credentials if not set)")
		dryRun        = flag.Bool("dry-run", false, "Scan only, don't upload")
	)
	
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Ultra-Simple Multi-Database Replicator for SQLite\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  %s -bucket my-backups -pattern '/data/*/db/*.db'\n\n", os.Args[0])
	}
	
	flag.Parse()
	
	// Validate required flags
	if *bucket == "" && !*dryRun {
		fmt.Fprintf(os.Stderr, "Error: -bucket is required unless -dry-run is set\n")
		flag.Usage()
		os.Exit(1)
	}
	
	// Print configuration
	log.Printf("Ultra-Simple Replicator Starting")
	log.Printf("Pattern: %s", *pattern)
	log.Printf("Interval: %v", *interval)
	if !*dryRun {
		log.Printf("S3: s3://%s/%s", *bucket, *pathTemplate)
		log.Printf("Region: %s", *region)
		log.Printf("Max Concurrent: %d", *maxConcurrent)
	} else {
		log.Printf("Mode: DRY RUN (no uploads)")
	}
	
	// Create S3 client or mock for dry run
	var s3Client ultrasimple.S3Client
	if *dryRun {
		s3Client = &DryRunClient{}
	} else {
		client, err := NewRealS3Client(*region, *bucket, *accessKey, *secretKey)
		if err != nil {
			log.Fatalf("Failed to create S3 client: %v", err)
		}
		s3Client = client
	}
	
	// Create replicator
	config := ultrasimple.S3Config{
		Region:        *region,
		Bucket:        *bucket,
		PathTemplate:  *pathTemplate,
		MaxConcurrent: *maxConcurrent,
	}
	
	replicator := ultrasimple.New(*pattern, config, s3Client)
	
	// Set up signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	
	go func() {
		<-sigChan
		log.Println("Shutting down...")
		cancel()
	}()
	
	// Run replicator
	if err := replicator.Run(ctx, *interval); err != nil && err != context.Canceled {
		log.Fatalf("Replicator error: %v", err)
	}
	
	// Print final stats
	stats := replicator.GetStats()
	log.Printf("Final stats: Scans=%d, Uploads=%d, Errors=%d, Bytes=%d",
		stats.Scans, stats.Uploads, stats.UploadErrors, stats.BytesUploaded)
}

// DryRunClient for testing without actual uploads
type DryRunClient struct{}

func (d *DryRunClient) Upload(key string, data []byte) error {
	log.Printf("[DRY RUN] Would upload: %s (%d bytes compressed)", key, len(data))
	return nil
}