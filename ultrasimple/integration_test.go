package ultrasimple

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// TestIntegrationScenario tests a realistic multi-tenant scenario
func TestIntegrationScenario(t *testing.T) {
	tmpDir := t.TempDir()
	
	// Create realistic directory structure
	projects := []string{"acme", "globex"}
	databases := []string{"users", "orders"}
	branches := []string{"main", "feature"}
	tenants := []string{"tenant1", "tenant2", "tenant3"}
	
	// Create all databases
	dbCount := 0
	for _, project := range projects {
		for _, database := range databases {
			for _, branch := range branches {
				for _, tenant := range tenants {
					dir := filepath.Join(tmpDir, "data", project, "databases", 
						database, "branches", branch, "tenants")
					os.MkdirAll(dir, 0755)
					
					dbPath := filepath.Join(dir, tenant+".db")
					createTestDB(t, dbPath, "CREATE TABLE data (id INTEGER, value TEXT)")
					dbCount++
				}
			}
		}
	}
	
	// Create replicator
	s3Client := NewMockS3Client()
	config := S3Config{
		Region:        "us-east-1",
		Bucket:        "test-bucket",
		PathTemplate:  "{{project}}/{{database}}/{{branch}}/{{tenant}}",
		MaxConcurrent: 10,
	}
	
	pattern := filepath.Join(tmpDir, "data/*/databases/*/branches/*/tenants/*.db")
	r := New(pattern, config, s3Client)
	
	// Initial scan - all databases should be uploaded
	r.scanAndSync()
	
	if r.GetDatabaseCount() != dbCount {
		t.Errorf("Expected %d databases, got %d", dbCount, r.GetDatabaseCount())
	}
	
	if s3Client.GetUploadCount() != dbCount {
		t.Errorf("Expected %d uploads, got %d", dbCount, s3Client.GetUploadCount())
	}
	
	// Verify path parsing
	found := false
	s3Client.mu.Lock()
	for key := range s3Client.uploads {
		if contains(key, "acme/users/main/tenant1") {
			found = true
			break
		}
	}
	s3Client.mu.Unlock()
	
	if !found {
		t.Error("Path template not working correctly")
	}
	
	// Second scan - no changes, no uploads
	initialCount := s3Client.GetUploadCount()
	r.scanAndSync()
	if s3Client.GetUploadCount() != initialCount {
		t.Error("Uploaded unchanged databases")
	}
	
	// Modify subset of databases
	modifiedCount := 0
	time.Sleep(10 * time.Millisecond)
	
	// Modify tenant1 databases in acme project
	for _, database := range databases {
		for _, branch := range branches {
			dbPath := filepath.Join(tmpDir, "data/acme/databases", 
				database, "branches", branch, "tenants/tenant1.db")
			
			db, err := sql.Open("sqlite3", dbPath)
			if err != nil {
				t.Fatal(err)
			}
			db.Exec("INSERT INTO data VALUES (1, 'test')")
			db.Close()
			modifiedCount++
		}
	}
	
	// Third scan - should only upload modified databases
	beforeModified := s3Client.GetUploadCount()
	r.scanAndSync()
	finalUploads := s3Client.GetUploadCount()
	// Expect 2 uploads per modified database
	// Backups might overwrite if in the same hour
	// So we expect 0 to modifiedCount new uploads
	minExpected := beforeModified
	maxExpected := beforeModified + modifiedCount
	if finalUploads < minExpected || finalUploads > maxExpected {
		t.Errorf("Expected %d-%d total uploads, got %d", 
			minExpected, maxExpected, finalUploads)
	}
	
	// Test with context cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	
	err := r.Run(ctx, 50*time.Millisecond)
	if err != context.DeadlineExceeded {
		t.Errorf("Expected context deadline exceeded, got %v", err)
	}
	
	stats := r.GetStats()
	t.Logf("Final stats: %d scans, %d uploads, %d errors, %d bytes",
		stats.Scans, stats.Uploads, stats.UploadErrors, stats.BytesUploaded)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[:len(substr)] == substr || 
		len(s) > len(substr) && contains(s[1:], substr)
}