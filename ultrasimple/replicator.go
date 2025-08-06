package ultrasimple

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Replicator handles multi-database replication with ultra-simple design
type Replicator struct {
	pattern   string
	s3Config  S3Config
	databases map[string]*DatabaseState
	
	s3Client  S3Client
	uploadSem chan struct{}
	
	stats Stats
	mu    sync.RWMutex
}

// DatabaseState tracks a single database
type DatabaseState struct {
	Path         string
	LastModTime  time.Time
	LastSize     int64
	LastSyncTime time.Time
}

// S3Config holds S3 configuration
type S3Config struct {
	Region        string
	Bucket        string
	PathTemplate  string
	MaxConcurrent int
	RetentionDays int // Number of days to retain backups (default 30)
}

// S3Client interface for testing
type S3Client interface {
	Upload(key string, data []byte) error
	List(prefix string) ([]string, error)
	Delete(keys []string) error
}

// Stats tracks replication statistics
type Stats struct {
	Scans         int64
	Uploads       int64
	UploadErrors  int64
	BytesUploaded int64
}

// New creates a new ultra-simple replicator
func New(pattern string, config S3Config, s3Client S3Client) *Replicator {
	if config.MaxConcurrent == 0 {
		config.MaxConcurrent = 100
	}
	if config.RetentionDays == 0 {
		config.RetentionDays = 30
	}
	
	return &Replicator{
		pattern:   pattern,
		s3Config:  config,
		databases: make(map[string]*DatabaseState),
		s3Client:  s3Client,
		uploadSem: make(chan struct{}, config.MaxConcurrent),
	}
}

// Run starts the replication loop
func (r *Replicator) Run(ctx context.Context, interval time.Duration) error {
	log.Printf("Starting ultra-simple replicator (interval: %v, retention: %d days)", interval, r.s3Config.RetentionDays)
	
	// Initial scan
	r.scanAndSync()
	
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	
	// Cleanup ticker - run every hour
	cleanupTicker := time.NewTicker(time.Hour)
	defer cleanupTicker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.scanAndSync()
		case <-cleanupTicker.C:
			r.cleanupOldBackups()
		}
	}
}

// scanAndSync performs a single scan and sync cycle
func (r *Replicator) scanAndSync() {
	start := time.Now()
	
	matches, err := filepath.Glob(r.pattern)
	if err != nil {
		log.Printf("Glob error: %v", err)
		return
	}
	
	r.mu.Lock()
	defer r.mu.Unlock()
	
	var wg sync.WaitGroup
	synced := 0
	
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		
		state, exists := r.databases[path]
		if !exists {
			state = &DatabaseState{
				Path:        path,
				LastModTime: info.ModTime(),
				LastSize:    info.Size(),
			}
			r.databases[path] = state
		}
		
		// Check if changed (size or mtime) or new
		if !exists || info.Size() != state.LastSize || info.ModTime().After(state.LastModTime) {
			synced++
			
			// Update state immediately
			state.LastModTime = info.ModTime()
			state.LastSize = info.Size()
			state.LastSyncTime = time.Now()
			
			// Sync in background
			wg.Add(1)
			go func(dbPath string) {
				defer wg.Done()
				
				r.uploadSem <- struct{}{}
				defer func() { <-r.uploadSem }()
				
				r.syncDatabase(dbPath)
			}(path)
		}
	}
	
	wg.Wait()
	
	atomic.AddInt64(&r.stats.Scans, 1)
	
	log.Printf("Scan complete: %d databases, %d synced (took %v)",
		len(r.databases), synced, time.Since(start))
}

// syncDatabase uploads a single database
func (r *Replicator) syncDatabase(path string) {
	data, err := r.readDatabaseSafely(path)
	if err != nil {
		log.Printf("Read error %s: %v", filepath.Base(path), err)
		return
	}
	
	compressed := compressLZ4(data)
	key := r.generateS3Key(path)
	
	err = r.s3Client.Upload(key, compressed)
	if err != nil {
		log.Printf("Upload error %s: %v", filepath.Base(path), err)
		atomic.AddInt64(&r.stats.UploadErrors, 1)
		return
	}
	
	atomic.AddInt64(&r.stats.Uploads, 1)
	atomic.AddInt64(&r.stats.BytesUploaded, int64(len(compressed)))
}

// readDatabaseSafely reads database with WAL handling
func (r *Replicator) readDatabaseSafely(path string) ([]byte, error) {
	walPath := path + "-wal"
	if info, err := os.Stat(walPath); err == nil && info.Size() > 0 {
		// WAL exists - try to checkpoint
		db, err := sql.Open("sqlite3", path)
		if err != nil {
			return nil, fmt.Errorf("open database: %w", err)
		}
		defer db.Close()
		
		_, err = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		if err != nil {
			log.Printf("Checkpoint failed for %s: %v", path, err)
		}
	}
	
	return os.ReadFile(path)
}

// generateS3Key creates S3 key from path template
func (r *Replicator) generateS3Key(path string) string {
	parts := strings.Split(path, "/")
	
	var project, database, branch, tenant string
	for i, part := range parts {
		if i > 0 && parts[i-1] == "data" {
			project = part
		} else if i > 0 && parts[i-1] == "databases" {
			database = part
		} else if i > 0 && parts[i-1] == "branches" {
			branch = part
		} else if i > 0 && parts[i-1] == "tenants" {
			tenant = strings.TrimSuffix(part, ".db")
		}
	}
	
	key := r.s3Config.PathTemplate
	key = strings.ReplaceAll(key, "{{project}}", project)
	key = strings.ReplaceAll(key, "{{database}}", database)
	key = strings.ReplaceAll(key, "{{branch}}", branch)
	key = strings.ReplaceAll(key, "{{tenant}}", tenant)
	
	// Include database name in the key
	dbName := filepath.Base(path)
	dbName = strings.TrimSuffix(dbName, ".db")
	
	// Use the NEXT hour timestamp (this ensures natural overwriting)
	nextHour := time.Now().Add(time.Hour).Truncate(time.Hour)
	timestamp := nextHour.Format("20060102-150000")
	
	return fmt.Sprintf("%s/%s-%s.db.lz4", key, dbName, timestamp)
}

// GetStats returns current statistics
func (r *Replicator) GetStats() Stats {
	return Stats{
		Scans:         atomic.LoadInt64(&r.stats.Scans),
		Uploads:       atomic.LoadInt64(&r.stats.Uploads),
		UploadErrors:  atomic.LoadInt64(&r.stats.UploadErrors),
		BytesUploaded: atomic.LoadInt64(&r.stats.BytesUploaded),
	}
}

// GetDatabaseCount returns the number of tracked databases
func (r *Replicator) GetDatabaseCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.databases)
}


// cleanupOldBackups removes backups older than retention period
func (r *Replicator) cleanupOldBackups() {
	start := time.Now()
	cutoff := start.AddDate(0, 0, -r.s3Config.RetentionDays)
	
	log.Printf("Starting cleanup of backups older than %s", cutoff.Format("2006-01-02"))
	
	// List all files in the bucket
	allKeys, err := r.s3Client.List("")
	if err != nil {
		log.Printf("Failed to list S3 objects for cleanup: %v", err)
		return
	}
	
	var toDelete []string
	
	for _, key := range allKeys {
		// Extract timestamp from key
		// Format: path/dbname-20060102-150405.999999999.db.lz4
		// or: path/dbname-20060102-150000.snapshot.db.lz4
		// Extract timestamp from key by finding the date pattern
		// Format: path/dbname-20060102-150405.999999999.db.lz4
		// or: path/dbname-20060102-150000.db.lz4 (hourly)
		
		// Find the date pattern (8 digits starting with 20)
		parts := strings.Split(key, "-")
		if len(parts) < 3 {
			continue
		}
		
		var dateStr, timeStr string
		for i := len(parts) - 2; i < len(parts); i++ {
			if i < 0 {
				continue
			}
			part := parts[i]
			if len(part) >= 8 && strings.HasPrefix(part, "20") {
				dateStr = part[:8]
				if i+1 < len(parts) {
					// Time part is in the next segment
					timePart := strings.Split(parts[i+1], ".")[0]
					if len(timePart) >= 6 {
						timeStr = timePart[:6]
					}
				}
				break
			}
		}
		
		if dateStr == "" || timeStr == "" {
			continue
		}
		
		// Parse timestamp
		timestamp, err := time.Parse("20060102150405", dateStr+timeStr)
		if err != nil {
			continue
		}
		
		// Check if older than cutoff
		if timestamp.Before(cutoff) {
			toDelete = append(toDelete, key)
		}
	}
	
	if len(toDelete) == 0 {
		log.Printf("No old backups to clean up")
		return
	}
	
	// Delete in batches of 1000 (S3 limit)
	deleted := 0
	for i := 0; i < len(toDelete); i += 1000 {
		end := i + 1000
		if end > len(toDelete) {
			end = len(toDelete)
		}
		
		batch := toDelete[i:end]
		if err := r.s3Client.Delete(batch); err != nil {
			log.Printf("Failed to delete batch of %d objects: %v", len(batch), err)
		} else {
			deleted += len(batch)
		}
	}
	
	log.Printf("Cleanup complete: deleted %d of %d old backups (took %v)", 
		deleted, len(toDelete), time.Since(start))
}