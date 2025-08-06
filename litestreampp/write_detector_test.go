package litestreampp_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/benbjohnson/litestream/litestreampp"
)

func TestWriteDetector(t *testing.T) {
	t.Run("BasicDetection", func(t *testing.T) {
		// Create test databases
		tmpDir := t.TempDir()
		db1 := filepath.Join(tmpDir, "db1.db")
		db2 := filepath.Join(tmpDir, "db2.db")
		db3 := filepath.Join(tmpDir, "db3.db")

		// Create initial files
		createTestFile(t, db1, "content1")
		createTestFile(t, db2, "content2")
		createTestFile(t, db3, "content3")

		// Track promotion/demotion
		promoted := make(map[string]int)
		demoted := make(map[string]int)

		// Create detector with short intervals for testing
		detector := litestreampp.NewWriteDetector(
			100*time.Millisecond, // scan interval
			200*time.Millisecond, // hot duration
			10,                   // max hot DBs
		)

		detector.SetCallbacks(
			func(path string) error {
				promoted[path]++
				return nil
			},
			func(path string) error {
				demoted[path]++
				return nil
			},
		)

		// Add databases to track
		detector.AddDatabase(db1)
		detector.AddDatabase(db2)
		detector.AddDatabase(db3)

		// Start detector
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		detector.Start(ctx)
		defer detector.Stop()

		// Wait for initial scan
		time.Sleep(150 * time.Millisecond)

		// Modify db1
		createTestFile(t, db1, "modified content")

		// Wait for detection
		time.Sleep(150 * time.Millisecond)

		// db1 should be hot now
		if !detector.IsHot(db1) {
			t.Error("db1 should be hot after modification")
		}
		if promoted[db1] < 1 {
			t.Error("db1 should have been promoted")
		}

		// db2 and db3 should still be cold
		if detector.IsHot(db2) {
			t.Error("db2 should be cold")
		}
		if detector.IsHot(db3) {
			t.Error("db3 should be cold")
		}

		// Wait for hot duration to expire
		time.Sleep(300 * time.Millisecond)

		// db1 should be cold again
		if detector.IsHot(db1) {
			t.Error("db1 should be cold after hot duration expired")
		}
		if demoted[db1] < 1 {
			t.Error("db1 should have been demoted")
		}
	})

	t.Run("MultipleModifications", func(t *testing.T) {
		tmpDir := t.TempDir()
		var dbs []string
		for i := 0; i < 5; i++ {
			db := filepath.Join(tmpDir, fmt.Sprintf("db%d.db", i))
			createTestFile(t, db, fmt.Sprintf("content%d", i))
			dbs = append(dbs, db)
		}

		detector := litestreampp.NewWriteDetector(
			100*time.Millisecond,
			200*time.Millisecond,
			3, // max 3 hot DBs
		)

		// Track promotions
		var promotedCount int
		detector.SetCallbacks(
			func(path string) error {
				promotedCount++
				return nil
			},
			nil,
		)

		// Add all databases
		for _, db := range dbs {
			detector.AddDatabase(db)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		detector.Start(ctx)
		defer detector.Stop()

		// Modify all databases
		for i, db := range dbs {
			createTestFile(t, db, fmt.Sprintf("modified%d", i))
		}

		// Wait for detection
		time.Sleep(150 * time.Millisecond)

		// Check hot count (should be limited to 3)
		total, hot, cold := detector.GetStatistics()
		if total != 5 {
			t.Errorf("expected 5 total databases, got %d", total)
		}
		if hot > 3 {
			t.Errorf("expected max 3 hot databases, got %d", hot)
		}
		if cold != total-hot {
			t.Errorf("cold count mismatch: %d != %d - %d", cold, total, hot)
		}
	})

	t.Run("GlobPatterns", func(t *testing.T) {
		tmpDir := t.TempDir()
		
		// Create directory structure
		dir1 := filepath.Join(tmpDir, "project1", "databases", "db1", "branches", "main", "tenants")
		dir2 := filepath.Join(tmpDir, "project2", "databases", "db1", "branches", "main", "tenants")
		os.MkdirAll(dir1, 0755)
		os.MkdirAll(dir2, 0755)

		// Create test databases
		db1 := filepath.Join(dir1, "tenant1.db")
		db2 := filepath.Join(dir1, "tenant2.db")
		db3 := filepath.Join(dir2, "tenant1.db")
		createTestFile(t, db1, "content1")
		createTestFile(t, db2, "content2")
		createTestFile(t, db3, "content3")

		detector := litestreampp.NewWriteDetector(
			100*time.Millisecond,
			200*time.Millisecond,
			10,
		)

		// Use glob pattern
		pattern := filepath.Join(tmpDir, "*", "databases", "*", "branches", "*", "tenants", "*.db")
		err := detector.AddDatabases([]string{pattern})
		if err != nil {
			t.Fatalf("failed to add databases: %v", err)
		}

		// Check that all databases were discovered
		total, _, _ := detector.GetStatistics()
		if total != 3 {
			t.Errorf("expected 3 databases discovered, got %d", total)
		}
	})

	t.Run("DeletedDatabase", func(t *testing.T) {
		tmpDir := t.TempDir()
		db1 := filepath.Join(tmpDir, "db1.db")
		createTestFile(t, db1, "content")

		var demotedCount int
		detector := litestreampp.NewWriteDetector(
			100*time.Millisecond,
			200*time.Millisecond,
			10,
		)
		detector.SetCallbacks(
			func(path string) error { return nil },
			func(path string) error {
				demotedCount++
				return nil
			},
		)

		detector.AddDatabase(db1)

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		detector.Start(ctx)
		defer detector.Stop()

		// Modify to make it hot
		createTestFile(t, db1, "modified")
		time.Sleep(150 * time.Millisecond)

		if !detector.IsHot(db1) {
			t.Error("db1 should be hot after modification")
		}

		// Delete the database
		os.Remove(db1)

		// Wait for next scan
		time.Sleep(150 * time.Millisecond)

		// Database should be removed and demoted
		if detector.IsHot(db1) {
			t.Error("deleted db1 should not be hot")
		}
		if demotedCount < 1 {
			t.Error("deleted database should have been demoted")
		}

		total, _, _ := detector.GetStatistics()
		if total != 0 {
			t.Errorf("deleted database should be removed from tracking, got %d", total)
		}
	})
}

func TestWriteDetectorConcurrency(t *testing.T) {
	// Test concurrent access to the detector
	tmpDir := t.TempDir()
	var dbs []string
	for i := 0; i < 20; i++ {
		db := filepath.Join(tmpDir, fmt.Sprintf("db%d.db", i))
		createTestFile(t, db, fmt.Sprintf("content%d", i))
		dbs = append(dbs, db)
	}

	detector := litestreampp.NewWriteDetector(
		50*time.Millisecond,
		100*time.Millisecond,
		10,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	detector.Start(ctx)
	defer detector.Stop()

	// Concurrently add databases and check status
	done := make(chan bool)
	for i := 0; i < 5; i++ {
		go func(start int) {
			for j := start; j < start+4; j++ {
				detector.AddDatabase(dbs[j])
				time.Sleep(10 * time.Millisecond)
				detector.IsHot(dbs[j])
				detector.GetStatistics()
			}
			done <- true
		}(i * 4)
	}

	// Wait for all goroutines
	for i := 0; i < 5; i++ {
		<-done
	}

	// Verify final state
	total, _, _ := detector.GetStatistics()
	if total != 20 {
		t.Errorf("expected 20 databases, got %d", total)
	}
}

// Helper function to create a test file
func createTestFile(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	// Small delay to ensure mtime changes are detectable
	time.Sleep(10 * time.Millisecond)
}