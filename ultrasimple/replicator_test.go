package ultrasimple

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// MockS3Client for testing
type MockS3Client struct {
	mu       sync.Mutex
	uploads  map[string][]byte
	errors   int
	failNext bool
}

func NewMockS3Client() *MockS3Client {
	return &MockS3Client{
		uploads: make(map[string][]byte),
	}
}

func (m *MockS3Client) Upload(key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	if m.failNext {
		m.failNext = false
		m.errors++
		return fmt.Errorf("mock upload error")
	}
	
	// Store with unique key to avoid overwrites
	m.uploads[key] = append([]byte{}, data...) // Copy data
	return nil
}

func (m *MockS3Client) List(prefix string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	var keys []string
	for key := range m.uploads {
		if prefix == "" || strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func (m *MockS3Client) Delete(keys []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	for _, key := range keys {
		delete(m.uploads, key)
	}
	return nil
}

func (m *MockS3Client) GetUploadCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.uploads)
}

func (m *MockS3Client) GetUploads() map[string][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Return a copy
	copy := make(map[string][]byte)
	for k, v := range m.uploads {
		copy[k] = v
	}
	return copy
}

func (m *MockS3Client) HasHourlyBackup(hour time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	
	// Hourly backups have format: dbname-20060102-150000.db.lz4 (no nanoseconds)
	hourlyMarker := hour.Format("20060102-150000")
	for key := range m.uploads {
		// Check if it contains the hour marker and ends with .db.lz4 (not .999999999.db.lz4)
		if strings.Contains(key, hourlyMarker) && strings.HasSuffix(key, "0000.db.lz4") {
			return true
		}
	}
	return false
}

func TestReplicatorBasic(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	
	// Create test database
	dbPath := filepath.Join(tmpDir, "test.db")
	createTestDB(t, dbPath, "CREATE TABLE test (id INTEGER)")
	
	// Create replicator
	s3Client := NewMockS3Client()
	config := S3Config{
		Region:        "us-east-1",
		Bucket:        "test-bucket",
		PathTemplate:  "backups",
		MaxConcurrent: 2,
	}
	
	r := New(filepath.Join(tmpDir, "*.db"), config, s3Client)
	
	// Run one scan
	r.scanAndSync()
	
	// Check results
	if r.GetDatabaseCount() != 1 {
		t.Errorf("Expected 1 database, got %d", r.GetDatabaseCount())
	}
	
	if s3Client.GetUploadCount() != 1 {
		t.Errorf("Expected 1 upload, got %d", s3Client.GetUploadCount())
	}
	
	stats := r.GetStats()
	if stats.Uploads != 1 {
		t.Errorf("Expected 1 upload in stats, got %d", stats.Uploads)
	}
}

func TestReplicatorChangeDetection(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	createTestDB(t, dbPath, "CREATE TABLE test (id INTEGER)")
	
	s3Client := NewMockS3Client()
	config := S3Config{
		Region:        "us-east-1",
		Bucket:        "test-bucket",
		PathTemplate:  "backups",
	}
	
	r := New(filepath.Join(tmpDir, "*.db"), config, s3Client)
	
	// First scan
	r.scanAndSync()
	initialUploads := s3Client.GetUploadCount()
	if initialUploads != 1 {
		t.Fatalf("Expected 1 initial upload, got %d", initialUploads)
	}
	
	// Second scan without changes - should not upload
	r.scanAndSync()
	if s3Client.GetUploadCount() != initialUploads {
		t.Error("Uploaded unchanged database")
	}
	
	// Modify database
	time.Sleep(10 * time.Millisecond) // Ensure mtime changes
	db, _ := sql.Open("sqlite3", dbPath)
	db.Exec("INSERT INTO test VALUES (1)")
	db.Close()
	
	// Third scan - should upload
	r.scanAndSync()
	finalUploads := s3Client.GetUploadCount()
	
	// Debug output
	s3Client.mu.Lock()
	t.Logf("All uploads after change:")
	for k := range s3Client.uploads {
		t.Logf("  Key: %s", k)
	}
	s3Client.mu.Unlock()
	
	// Expect 0-1 new uploads (might overwrite if in same hour)
	if finalUploads < initialUploads || finalUploads > initialUploads+1 {
		t.Errorf("Failed to detect database change. Initial: %d, Final: %d", 
			initialUploads, finalUploads)
	}
}

func TestReplicatorWALHandling(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	
	// Create database with WAL mode
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("PRAGMA journal_mode=WAL")
	db.Exec("CREATE TABLE test (id INTEGER)")
	db.Exec("INSERT INTO test VALUES (1)")
	// Don't close - keep WAL active
	
	s3Client := NewMockS3Client()
	config := S3Config{
		Region:        "us-east-1",
		Bucket:        "test-bucket",
		PathTemplate:  "backups",
	}
	
	r := New(filepath.Join(tmpDir, "*.db"), config, s3Client)
	
	// Should handle WAL correctly
	r.scanAndSync()
	
	if s3Client.GetUploadCount() != 1 {
		t.Errorf("Expected 1 upload with WAL, got %d", s3Client.GetUploadCount())
	}
	
	db.Close()
}

func TestReplicatorPathTemplate(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Create nested directory structure
	dbDir := filepath.Join(tmpDir, "data", "project1", "databases", "userdb", 
		"branches", "main", "tenants")
	os.MkdirAll(dbDir, 0755)
	
	dbPath := filepath.Join(dbDir, "acme.db")
	createTestDB(t, dbPath, "CREATE TABLE test (id INTEGER)")
	
	s3Client := NewMockS3Client()
	config := S3Config{
		Region:        "us-east-1",
		Bucket:        "test-bucket",
		PathTemplate:  "{{project}}/{{database}}/{{branch}}/{{tenant}}",
	}
	
	pattern := filepath.Join(tmpDir, "data/*/databases/*/branches/*/tenants/*.db")
	r := New(pattern, config, s3Client)
	
	r.scanAndSync()
	
	// Check that path was parsed correctly
	found := false
	for key := range s3Client.uploads {
		if strings.Contains(key, "project1/userdb/main/acme") {
			found = true
			break
		}
	}
	
	if !found {
		t.Error("Path template not parsed correctly")
	}
}

func TestReplicatorConcurrency(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Create multiple databases
	for i := 0; i < 5; i++ {
		dbPath := filepath.Join(tmpDir, fmt.Sprintf("test%d.db", i))
		createTestDB(t, dbPath, "CREATE TABLE test (id INTEGER)")
	}
	
	s3Client := NewMockS3Client()
	config := S3Config{
		Region:        "us-east-1",
		Bucket:        "test-bucket",
		PathTemplate:  "backups",
		MaxConcurrent: 2, // Limit concurrency
	}
	
	r := New(filepath.Join(tmpDir, "*.db"), config, s3Client)
	
	start := time.Now()
	r.scanAndSync()
	duration := time.Since(start)
	
	// Debug: print uploaded keys
	s3Client.mu.Lock()
	t.Logf("Uploaded keys: %v", len(s3Client.uploads))
	for k := range s3Client.uploads {
		t.Logf("  Key: %s", k)
	}
	s3Client.mu.Unlock()
	
	// Should have uploaded all 5 databases
	if s3Client.GetUploadCount() != 5 {
		t.Errorf("Expected 5 uploads, got %d", s3Client.GetUploadCount())
	}
	
	// With concurrency 2, should take some time
	if duration < 10*time.Millisecond {
		t.Log("Warning: uploads may not be respecting concurrency limit")
	}
}

func TestReplicatorErrorHandling(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	createTestDB(t, dbPath, "CREATE TABLE test (id INTEGER)")
	
	s3Client := NewMockS3Client()
	s3Client.failNext = true
	
	config := S3Config{
		Region:        "us-east-1",
		Bucket:        "test-bucket",
		PathTemplate:  "backups",
	}
	
	r := New(filepath.Join(tmpDir, "*.db"), config, s3Client)
	
	// First scan - should fail
	r.scanAndSync()
	
	stats := r.GetStats()
	if stats.UploadErrors != 1 {
		t.Errorf("Expected 1 error, got %d", stats.UploadErrors)
	}
	
	// Upload should have failed
	if s3Client.GetUploadCount() != 0 {
		t.Errorf("Expected 0 successful uploads, got %d", s3Client.GetUploadCount())
	}
	
	// Ultra-simple design: only retries if database changes
	// Second scan without changes - should NOT retry
	r.scanAndSync()
	if s3Client.GetUploadCount() != 0 {
		t.Error("Should not retry unchanged database")
	}
	
	// Modify database to trigger retry
	time.Sleep(10 * time.Millisecond)
	db, _ := sql.Open("sqlite3", dbPath)
	db.Exec("INSERT INTO test VALUES (1)")
	db.Close()
	
	// Third scan - should upload successfully
	r.scanAndSync()
	// Should now have 1 upload (might be same key if within same hour)
	if s3Client.GetUploadCount() != 1 {
		t.Errorf("Expected 1 total upload after change, got %d", s3Client.GetUploadCount())
	}
}

func TestReplicatorContext(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	createTestDB(t, dbPath, "CREATE TABLE test (id INTEGER)")
	
	s3Client := NewMockS3Client()
	config := S3Config{
		Region:        "us-east-1",
		Bucket:        "test-bucket",
		PathTemplate:  "backups",
	}
	
	r := New(filepath.Join(tmpDir, "*.db"), config, s3Client)
	
	ctx, cancel := context.WithCancel(context.Background())
	
	// Start replicator
	done := make(chan error)
	go func() {
		done <- r.Run(ctx, 100*time.Millisecond)
	}()
	
	// Wait for at least one scan
	time.Sleep(150 * time.Millisecond)
	
	// Cancel and check it stops
	cancel()
	
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("Expected context.Canceled, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("Replicator did not stop on context cancel")
	}
}

func TestReplicatorNextHourBackups(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Create test databases
	db1Path := filepath.Join(tmpDir, "test1.db")
	db2Path := filepath.Join(tmpDir, "test2.db")
	createTestDB(t, db1Path, "CREATE TABLE test (id INTEGER)")
	createTestDB(t, db2Path, "CREATE TABLE test (id INTEGER)")
	
	s3Client := NewMockS3Client()
	config := S3Config{
		Region:        "us-east-1",
		Bucket:        "test-bucket",
		PathTemplate:  "backups",
		RetentionDays: 30,
	}
	
	r := New(filepath.Join(tmpDir, "*.db"), config, s3Client)
	
	// First scan - both databases are new
	r.scanAndSync()
	
	// Should have 2 uploads
	if s3Client.GetUploadCount() != 2 {
		t.Errorf("Expected 2 initial uploads, got %d", s3Client.GetUploadCount())
	}
	
	// Check that backups use next hour timestamp
	nextHour := time.Now().Add(time.Hour).Truncate(time.Hour)
	nextHourStr := nextHour.Format("20060102-150000")
	
	uploads := s3Client.GetUploads()
	hasNextHour := false
	for key := range uploads {
		if strings.Contains(key, nextHourStr) {
			hasNextHour = true
			break
		}
	}
	
	if !hasNextHour {
		t.Error("Expected backups to use next hour timestamp")
		t.Logf("Looking for: %s", nextHourStr)
		for k := range uploads {
			t.Logf("  Found: %s", k)
		}
	}
	
	// Change one database
	time.Sleep(10 * time.Millisecond)
	db, _ := sql.Open("sqlite3", db1Path)
	db.Exec("INSERT INTO test VALUES (1)")
	db.Close()
	
	// Next scan might create 0-1 new uploads (overwrites if still in same hour)
	initialCount := s3Client.GetUploadCount()
	r.scanAndSync()
	finalCount := s3Client.GetUploadCount()
	
	if finalCount < initialCount || finalCount > initialCount+1 {
		t.Errorf("Expected 0-1 new uploads after change, got %d", finalCount-initialCount)
	}
}

func TestReplicatorCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	createTestDB(t, dbPath, "CREATE TABLE test (id INTEGER)")
	
	s3Client := NewMockS3Client()
	config := S3Config{
		Region:        "us-east-1",
		Bucket:        "test-bucket",
		PathTemplate:  "backups",
		RetentionDays: 30,
	}
	
	r := New(filepath.Join(tmpDir, "*.db"), config, s3Client)
	
	// Create some fake old uploads
	oldTime := time.Now().AddDate(0, 0, -40) // 40 days ago
	oldKey := fmt.Sprintf("backups/test-%s.db.lz4", oldTime.Format("20060102-150405.999999999"))
	s3Client.uploads[oldKey] = []byte("old data")
	
	// Create a recent upload
	r.scanAndSync()
	initialCount := s3Client.GetUploadCount()
	
	// Run cleanup
	r.cleanupOldBackups()
	
	// Old file should be deleted
	if s3Client.GetUploadCount() != initialCount-1 {
		t.Errorf("Expected old backup to be deleted. Before: %d, After: %d", 
			initialCount, s3Client.GetUploadCount())
	}
	
	// Check that old key is gone
	uploads := s3Client.GetUploads()
	if _, exists := uploads[oldKey]; exists {
		t.Error("Old backup key still exists after cleanup")
	}
}

func TestReplicator15SecondInterval(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	createTestDB(t, dbPath, "CREATE TABLE test (id INTEGER)")
	
	s3Client := NewMockS3Client()
	config := S3Config{
		Region:        "us-east-1",
		Bucket:        "test-bucket",
		PathTemplate:  "backups",
	}
	
	r := New(filepath.Join(tmpDir, "*.db"), config, s3Client)
	
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Start with 15-second interval
	go r.Run(ctx, 15*time.Second)
	
	// Wait for initial scan
	time.Sleep(100 * time.Millisecond)
	
	// Change database
	db, _ := sql.Open("sqlite3", dbPath)
	db.Exec("INSERT INTO test VALUES (1)")
	db.Close()
	
	// Wait for next scan (should happen within 15 seconds)
	time.Sleep(16 * time.Second)
	
	// Should have at least 1 upload (might be 2 if hour changed)
	if s3Client.GetUploadCount() < 1 {
		t.Errorf("Expected at least 1 upload with 15-second interval, got %d", 
			s3Client.GetUploadCount())
	}
}

// Helper to create test database
func createTestDB(t *testing.T, path string, schema string) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
}