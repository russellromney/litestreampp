package main

import (
	"bytes"
	"context"
	"log"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/benbjohnson/litestream/ultrasimple"
)

// RealS3Client implements the S3Client interface with actual AWS SDK
type RealS3Client struct {
	s3 *s3.S3
	bucket string
}

func NewRealS3Client(region, bucket string) (*RealS3Client, error) {
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(region),
	})
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

func (c *RealS3Client) List(prefix string) ([]string, error) {
	var keys []string
	err := c.s3.ListObjectsV2Pages(&s3.ListObjectsV2Input{
		Bucket: aws.String(c.bucket),
		Prefix: aws.String(prefix),
	}, func(page *s3.ListObjectsV2Output, lastPage bool) bool {
		for _, obj := range page.Contents {
			keys = append(keys, *obj.Key)
		}
		return !lastPage
	})
	return keys, err
}

func (c *RealS3Client) Delete(keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	
	// Build delete objects
	objects := make([]*s3.ObjectIdentifier, len(keys))
	for i, key := range keys {
		objects[i] = &s3.ObjectIdentifier{
			Key: aws.String(key),
		}
	}
	
	_, err := c.s3.DeleteObjects(&s3.DeleteObjectsInput{
		Bucket: aws.String(c.bucket),
		Delete: &s3.Delete{
			Objects: objects,
			Quiet:   aws.Bool(true),
		},
	})
	return err
}

func main() {
	// Configuration
	pattern := "/data/*/databases/*/branches/*/tenants/*.db"
	region := "us-east-1"
	bucket := "my-litestream-backups"
	
	// Create S3 client
	s3Client, err := NewRealS3Client(region, bucket)
	if err != nil {
		log.Fatalf("Failed to create S3 client: %v", err)
	}
	
	// Create replicator
	config := ultrasimple.S3Config{
		Region:        region,
		Bucket:        bucket,
		PathTemplate:  "{{project}}/{{database}}/{{branch}}/{{tenant}}",
		MaxConcurrent: 100,
		RetentionDays: 30,  // Keep backups for 30 days
	}
	
	replicator := ultrasimple.New(pattern, config, s3Client)
	
	// Run replicator
	ctx := context.Background()
	log.Printf("Starting ultra-simple replicator")
	log.Printf("Pattern: %s", pattern)
	log.Printf("S3: s3://%s", bucket)
	log.Printf("Interval: 15 seconds")
	log.Printf("Retention: %d days", config.RetentionDays)
	log.Printf("Features: Next-hour naming (natural rate limiting), automatic cleanup")
	
	if err := replicator.Run(ctx, 15*time.Second); err != nil {
		log.Fatalf("Replicator error: %v", err)
	}
}