// demo_with_s3.go - Demo with actual S3 replication configuration
// Run with: go run demo_with_s3.go
//
// IMPORTANT: This demo requires AWS credentials to be configured:
//   - Set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY environment variables
//   - Or use AWS CLI credentials (~/.aws/credentials)
//   - Or use IAM role if running on EC2
//
// Also requires an S3 bucket to be created and accessible

package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/benbjohnson/litestream"
	_ "github.com/mattn/go-sqlite3"
)

func main() {
	log.SetFlags(log.Ltime)
	fmt.Println("=== Litestream Demo with S3 Replication ===")
	fmt.Println()
	
	// Check for S3 configuration
	bucket := os.Getenv("LITESTREAM_S3_BUCKET")
	if bucket == "" {
		fmt.Println("ERROR: LITESTREAM_S3_BUCKET environment variable not set")
		fmt.Println()
		fmt.Println("Please set:")
		fmt.Println("  export LITESTREAM_S3_BUCKET=your-bucket-name")
		fmt.Println("  export AWS_ACCESS_KEY_ID=your-access-key")
		fmt.Println("  export AWS_SECRET_ACCESS_KEY=your-secret-key")
		fmt.Println("  export AWS_REGION=us-east-1  # optional, defaults to us-east-1")
		fmt.Println()
		fmt.Println("Or use LocalStack for testing:")
		fmt.Println("  docker run -d -p 4566:4566 localstack/localstack")
		fmt.Println("  export LITESTREAM_S3_BUCKET=test-bucket")
		fmt.Println("  export LITESTREAM_S3_ENDPOINT=http://localhost:4566")
		fmt.Println("  export AWS_ACCESS_KEY_ID=test")
		fmt.Println("  export AWS_SECRET_ACCESS_KEY=test")
		os.Exit(1)
	}
	
	// Setup
	homeDir, _ := os.UserHomeDir()
	demoDir := filepath.Join(homeDir, ".litestream-demos", "s3-demo")
	
	// Cleanup function
	cleanup := func() {
		fmt.Println("\nCleaning up demo files...")
		if err := os.RemoveAll(demoDir); err != nil {
			log.Printf("Warning: Failed to clean up %s: %v", demoDir, err)
		} else {
			fmt.Println("✓ Cleanup complete - removed", demoDir)
		}
		// Try to remove parent
		os.Remove(filepath.Dir(demoDir))
	}
	defer cleanup()
	
	// Clean previous runs
	os.RemoveAll(demoDir)
	
	// Create a few test databases
	fmt.Println("Creating test databases...")
	dbPaths := createTestDatabases(demoDir, 10) // Just 10 for S3 testing
	fmt.Printf("✓ Created %d databases\n\n", len(dbPaths))
	
	// Setup Litestream with S3 replication
	fmt.Println("Starting Litestream with S3 replication...")
	fmt.Printf("  Bucket: %s\n", bucket)
	fmt.Printf("  Region: %s\n", getEnvOrDefault("AWS_REGION", "us-east-1"))
	if endpoint := os.Getenv("LITESTREAM_S3_ENDPOINT"); endpoint != "" {
		fmt.Printf("  Endpoint: %s (custom/localstack)\n", endpoint)
	}
	fmt.Println()
	
	manager := setupManagerWithS3(demoDir, bucket)
	
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	
	if err := manager.Start(ctx); err != nil {
		log.Fatalf("Failed to start: %v", err)
	}
	defer manager.Stop()
	
	fmt.Println("✓ Manager started with S3 replication\n")
	
	// Simulate some activity
	fmt.Println("Simulating database writes...")
	for i := 0; i < 3; i++ {
		fmt.Printf("\nRound %d:\n", i+1)
		
		// Write to databases
		for j := 0; j < 3 && j < len(dbPaths); j++ {
			dbPath := dbPaths[j]
			fmt.Printf("  Writing to %s\n", filepath.Base(dbPath))
			writeData(dbPath, fmt.Sprintf("round_%d", i))
		}
		
		// Wait for replication
		fmt.Println("  Waiting for S3 replication...")
		time.Sleep(5 * time.Second)
		
		// In a real implementation, we would check S3 for the replicated files
		fmt.Println("  ✓ Replication should be in progress")
	}
	
	fmt.Println("\n=== Demo Complete ===")
	fmt.Println()
	fmt.Println("Note: In a production setup, you would see:")
	fmt.Println("  - WAL files uploaded to S3")
	fmt.Println("  - Snapshots created periodically")
	fmt.Println("  - Hot databases actively streaming")
	fmt.Println("  - Cold databases with snapshots only")
	fmt.Println()
	fmt.Println("Check your S3 bucket for uploaded files:")
	fmt.Printf("  aws s3 ls s3://%s/ --recursive\n", bucket)
}

func createTestDatabases(baseDir string, count int) []string {
	var paths []string
	
	for i := 0; i < count; i++ {
		dir := filepath.Join(
			baseDir,
			fmt.Sprintf("project%d", i/5),
			"databases",
			fmt.Sprintf("db%d", i%5),
		)
		
		os.MkdirAll(dir, 0755)
		dbPath := filepath.Join(dir, fmt.Sprintf("test%d.db", i))
		
		db, err := sql.Open("sqlite3", dbPath)
		if err != nil {
			log.Printf("Failed to create %s: %v", dbPath, err)
			continue
		}
		
		_, err = db.Exec(`
			CREATE TABLE data (
				id INTEGER PRIMARY KEY,
				value TEXT,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);
			INSERT INTO data (value) VALUES ('initial');
		`)
		db.Close()
		
		if err != nil {
			log.Printf("Failed to initialize %s: %v", dbPath, err)
			continue
		}
		
		paths = append(paths, dbPath)
	}
	
	return paths
}

func setupManagerWithS3(baseDir, bucket string) *litestream.IntegratedMultiDBManager {
	// Create S3 replica configuration
	replicaConfig := &litestream.ReplicaConfig{
		Type:   "s3",
		Bucket: bucket,
		Path:   "litestream-demo/{{project}}/{{database}}/{{branch}}/{{tenant}}",
		Region: getEnvOrDefault("AWS_REGION", "us-east-1"),
		SyncInterval: 1 * time.Second, // Fast sync for demo
		
		// Use environment variables for credentials
		AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
	}
	
	// Check for custom endpoint (LocalStack, MinIO, etc.)
	if endpoint := os.Getenv("LITESTREAM_S3_ENDPOINT"); endpoint != "" {
		replicaConfig.Endpoint = endpoint
	}
	
	config := &litestream.MultiDBConfig{
		Enabled:         true,
		Patterns: []string{
			filepath.Join(baseDir, "*", "databases", "*", "*.db"),
		},
		MaxHotDatabases:  10,
		ScanInterval:     2 * time.Second,
		ReplicaTemplate:  replicaConfig, // THIS IS THE KEY PART!
		HotPromotion: litestream.HotPromotionConfig{
			RecentModifyThreshold: 5 * time.Second,
		},
	}
	
	store := litestream.NewStore(nil, litestream.CompactionLevels{})
	manager, err := litestream.NewIntegratedMultiDBManager(store, config)
	if err != nil {
		log.Fatalf("Failed to create manager: %v", err)
	}
	
	return manager
}

func writeData(dbPath string, value string) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Printf("Failed to open %s: %v", dbPath, err)
		return
	}
	defer db.Close()
	
	_, err = db.Exec("INSERT INTO data (value) VALUES (?)", value)
	if err != nil {
		log.Printf("Failed to write to %s: %v", dbPath, err)
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}