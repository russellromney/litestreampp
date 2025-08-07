package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/benbjohnson/litestream"
	"github.com/benbjohnson/litestream/s3"
	"github.com/bmatcuk/doublestar/v4"
)

// RestorePatternCommand represents a command to restore multiple databases from backups.
type RestorePatternCommand struct{}

// databaseInfo holds information about a database to restore
type databaseInfo struct {
	Path   string     // Local path or S3 key
	Config *DBConfig  // Config if from filesystem
	S3URL  string     // S3 URL if from S3 discovery
}

// Run executes the pattern restore command.
func (c *RestorePatternCommand) Run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("litestream-restore-pattern", flag.ContinueOnError)
	configPath, noExpandEnv := registerConfigFlag(fs)
	outputDir := fs.String("output-dir", "", "base directory for restored databases")
	parallelism := fs.Int("parallel", 10, "number of parallel restore operations")
	showProgress := fs.Bool("progress", false, "show progress during restore")
	ifDBNotExists := fs.Bool("if-db-not-exists", false, "skip if database already exists")
	fs.Usage = c.Usage
	
	if err := fs.Parse(args); err != nil {
		return err
	} else if fs.NArg() == 0 || fs.Arg(0) == "" {
		return fmt.Errorf("pattern required")
	} else if fs.NArg() > 1 {
		return fmt.Errorf("too many arguments")
	}

	pattern := fs.Arg(0)
	
	var databases []databaseInfo
	
	// Check if pattern is S3 URL or filesystem path
	if isURL(pattern) {
		// S3 discovery mode
		s3Databases, err := c.discoverS3Databases(ctx, pattern, *outputDir)
		if err != nil {
			return fmt.Errorf("S3 discovery failed: %w", err)
		}
		databases = s3Databases
	} else {
		// Filesystem config mode
		if *configPath == "" {
			*configPath = DefaultConfigPath()
		}
		
		config, err := ReadConfigFile(*configPath, !*noExpandEnv)
		if err != nil {
			return fmt.Errorf("cannot read config: %w", err)
		}
		
		// Find databases matching pattern using doublestar for ** support
		for _, dbConfig := range config.DBs {
			// Use doublestar for advanced glob matching
			matched, err := doublestar.Match(pattern, dbConfig.Path)
			if err != nil {
				return fmt.Errorf("invalid pattern: %w", err)
			}
			if matched {
				databases = append(databases, databaseInfo{
					Path:   dbConfig.Path,
					Config: dbConfig,
				})
			}
		}
	}
	
	if len(databases) == 0 {
		return fmt.Errorf("no databases found matching pattern: %s", pattern)
	}
	
	slog.Info("found databases to restore", "count", len(databases))
	
	// Create semaphore for parallelism control
	sem := make(chan struct{}, *parallelism)
	var wg sync.WaitGroup
	var successCount, errorCount int32
	
	// Restore each database
	for _, dbInfo := range databases {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore
		
		go func(info databaseInfo) {
			defer wg.Done()
			defer func() { <-sem }() // Release semaphore
			
			var err error
			if info.S3URL != "" {
				// S3 restoration
				err = c.restoreS3Database(ctx, info.S3URL, info.Path, *outputDir, *ifDBNotExists)
			} else {
				// Config-based restoration
				err = c.restoreDatabase(ctx, info.Config, *outputDir, *ifDBNotExists)
			}
			
			if err != nil {
				atomic.AddInt32(&errorCount, 1)
				slog.Error("failed to restore database", "path", info.Path, "error", err)
			} else {
				atomic.AddInt32(&successCount, 1)
				if *showProgress {
					total := int32(len(databases))
					current := atomic.LoadInt32(&successCount) + atomic.LoadInt32(&errorCount)
					fmt.Printf("Progress: %d/%d databases restored\n", current, total)
				}
			}
		}(dbInfo)
	}
	
	wg.Wait()
	
	// Print summary
	slog.Info("restore pattern completed", 
		"total", len(databases),
		"success", successCount,
		"errors", errorCount)
	
	if errorCount > 0 {
		return fmt.Errorf("failed to restore %d databases", errorCount)
	}
	
	return nil
}

// discoverS3Databases finds databases in S3 matching the pattern
func (c *RestorePatternCommand) discoverS3Databases(ctx context.Context, s3Pattern string, outputDir string) ([]databaseInfo, error) {
	// Parse S3 URL
	u, err := url.Parse(s3Pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid S3 URL: %w", err)
	}
	
	if u.Scheme != "s3" {
		return nil, fmt.Errorf("URL must use s3:// scheme")
	}
	
	bucket := u.Host
	prefix := strings.TrimPrefix(u.Path, "/")
	
	slog.Info("discovering S3 databases", "bucket", bucket, "prefix", prefix)
	
	// Extract pattern from prefix if it contains wildcards
	var pattern string
	basePrefix := prefix
	if strings.ContainsAny(prefix, "*?[") {
		// Find the directory part before wildcards
		parts := strings.Split(prefix, "/")
		var prefixParts []string
		for i, part := range parts {
			if strings.ContainsAny(part, "*?[") {
				pattern = strings.Join(parts[i:], "/")
				break
			}
			prefixParts = append(prefixParts, part)
		}
		basePrefix = strings.Join(prefixParts, "/")
		// Don't add trailing slash if basePrefix is empty
		if basePrefix != "" && !strings.HasSuffix(basePrefix, "/") {
			basePrefix += "/"
		}
	}
	
	// Create S3 client for listing
	client := s3.NewReplicaClient()
	client.Bucket = bucket
	
	// Support custom endpoint for testing (LocalStack, MinIO, etc.)
	if endpoint := os.Getenv("AWS_ENDPOINT"); endpoint != "" {
		client.Endpoint = endpoint
		client.ForcePathStyle = true // Required for LocalStack
	}
	
	// Use environment credentials if available
	if accessKey := os.Getenv("AWS_ACCESS_KEY_ID"); accessKey != "" {
		client.AccessKeyID = accessKey
	}
	if secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY"); secretKey != "" {
		client.SecretAccessKey = secretKey
	}
	if region := os.Getenv("AWS_DEFAULT_REGION"); region != "" {
		client.Region = region
	}
	
	// Initialize the S3 client
	if err := client.Init(ctx); err != nil {
		return nil, fmt.Errorf("cannot initialize S3 client: %w", err)
	}
	
	var databases []databaseInfo
	seenPaths := make(map[string]bool)
	
	// If no wildcards, assume it's a direct database path
	if pattern == "" {
		// Single database restore
		dbPath := prefix
		if strings.HasSuffix(dbPath, "/") {
			dbPath = strings.TrimSuffix(dbPath, "/")
		}
		
		outputPath := path.Base(dbPath)
		if outputDir != "" {
			outputPath = filepath.Join(outputDir, outputPath)
		}
		
		databases = append(databases, databaseInfo{
			Path:  outputPath,
			S3URL: fmt.Sprintf("s3://%s/%s", bucket, dbPath),
		})
	} else {
		// Pattern-based discovery using the new listing method
		slog.Info("listing S3 objects for pattern discovery", "basePrefix", basePrefix, "pattern", pattern)
		
		objectCount := 0
		err = client.ListObjectsWithPrefix(ctx, bucket, basePrefix, func(key string) error {
			objectCount++
			
			// Check if this looks like a Litestream backup
			// Keys are like: prefix/path/to/db.db/generations/xxx/snapshots/xxx.ltx
			if strings.Contains(key, "/generations/") && strings.Contains(key, "/snapshots/") {
				// Extract the database path (everything before /generations/)
				parts := strings.Split(key, "/generations/")
				if len(parts) > 0 {
					dbPath := parts[0]
					
					// Apply pattern matching if specified
					if pattern != "" {
						// Get the relative path from basePrefix for pattern matching
						relPath := strings.TrimPrefix(dbPath, basePrefix)
						matched, err := doublestar.Match(pattern, relPath)
						if err != nil {
							slog.Warn("pattern match error", "pattern", pattern, "path", relPath, "error", err)
							return nil // Continue processing other objects
						}
						if !matched {
							return nil // Skip this object
						}
					}
					
					// Track unique database paths
					if !seenPaths[dbPath] {
						seenPaths[dbPath] = true
						
						// Determine output path
						outputPath := path.Base(dbPath)
						if outputDir != "" {
							outputPath = filepath.Join(outputDir, outputPath)
						} else {
							// Preserve relative structure from basePrefix
							relPath := strings.TrimPrefix(dbPath, basePrefix)
							outputPath = relPath
						}
						
						databases = append(databases, databaseInfo{
							Path:  outputPath,
							S3URL: fmt.Sprintf("s3://%s/%s", bucket, dbPath),
						})
						
						slog.Debug("discovered database", "path", dbPath, "output", outputPath)
					}
				}
			}
			
			// Log progress every 1000 objects
			if objectCount%1000 == 0 {
				slog.Info("S3 discovery progress", "objects_scanned", objectCount, "databases_found", len(databases))
			}
			
			return nil
		})
		
		if err != nil {
			return nil, fmt.Errorf("failed to list S3 objects: %w", err)
		}
	}
	
	slog.Info("S3 discovery complete", "databases_found", len(databases))
	return databases, nil
}

// restoreS3Database restores a database from S3
func (c *RestorePatternCommand) restoreS3Database(ctx context.Context, s3URL string, outputPath string, outputDir string, ifDBNotExists bool) error {
	// Check if output already exists
	if ifDBNotExists {
		if _, err := os.Stat(outputPath); err == nil {
			slog.Info("database already exists, skipping", "path", outputPath)
			return nil
		}
	}
	
	// Create replica from S3 URL
	syncInterval := litestream.DefaultSyncInterval
	replica, err := NewReplicaFromConfig(&ReplicaConfig{
		URL:          s3URL,
		SyncInterval: &syncInterval,
	}, nil)
	if err != nil {
		return fmt.Errorf("cannot create replica: %w", err)
	}
	
	// Create restore options
	opt := litestream.NewRestoreOptions()
	opt.OutputPath = outputPath
	
	// Perform restore
	return replica.Restore(ctx, opt)
}

// restoreDatabase restores a single database from config
func (c *RestorePatternCommand) restoreDatabase(ctx context.Context, dbConfig *DBConfig, outputDir string, ifDBNotExists bool) error {
	// Create database and replica from config
	db, err := NewDBFromConfig(dbConfig)
	if err != nil {
		return fmt.Errorf("cannot create db: %w", err)
	}
	
	if db.Replica == nil {
		return fmt.Errorf("no replica configured for database: %s", dbConfig.Path)
	}
	
	// Determine output path
	outputPath := dbConfig.Path
	if outputDir != "" {
		// Preserve relative structure under output directory
		outputPath = filepath.Join(outputDir, filepath.Base(dbConfig.Path))
	}
	
	// Create restore options
	opt := litestream.NewRestoreOptions()
	opt.OutputPath = outputPath
	
	// Skip if database already exists
	if ifDBNotExists {
		if _, err := os.Stat(outputPath); err == nil {
			slog.Info("database already exists, skipping", "path", outputPath)
			return nil
		}
	}
	
	// Perform restore
	return db.Replica.Restore(ctx, opt)
}

// Usage prints the help screen to STDOUT.
func (c *RestorePatternCommand) Usage() {
	fmt.Printf(`
The restore-pattern command recovers multiple databases matching a pattern.

Usage:

	litestream restore-pattern [arguments] PATTERN

Arguments:

	-config PATH
	    Specifies the configuration file.
	    Defaults to %s

	-no-expand-env
	    Disables environment variable expansion in configuration file.

	-output-dir PATH
	    Base directory for restored databases.
	    Defaults to original paths.

	-parallel NUM
	    Number of parallel restore operations.
	    Defaults to 10.

	-progress
	    Show progress during restore.

	-if-db-not-exists
	    Skip databases that already exist.

Examples:

	# Restore all databases under /data
	$ litestream restore-pattern "/data/**/*.db"

	# Restore to different directory with progress
	$ litestream restore-pattern "/data/**/*.db" -output-dir /restored -progress

	# Restore with custom parallelism
	$ litestream restore-pattern "*.db" -parallel 20

	# Restore from S3 with pattern
	$ litestream restore-pattern "s3://mybucket/backups/**/*.db" -output-dir /restored

	# Restore specific project from S3
	$ litestream restore-pattern "s3://mybucket/project1/*.db"

`[1:],
		DefaultConfigPath(),
	)
}