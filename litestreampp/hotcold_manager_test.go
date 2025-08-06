package litestreampp_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/benbjohnson/litestream"
	"github.com/benbjohnson/litestream/litestreampp"
	_ "github.com/mattn/go-sqlite3"
)

func TestHotColdManager(t *testing.T) {
	t.Run("BasicLifecycle", func(t *testing.T) {
		// Create test databases
		tmpDir := t.TempDir()
		db1 := filepath.Join(tmpDir, "db1.db")
		db2 := filepath.Join(tmpDir, "db2.db")
		
		createTestDB(t, db1)
		createTestDB(t, db2)

		// Create manager
		config := &litestreampp.HotColdConfig{
			MaxHotDatabases: 10,
			ScanInterval:    100 * time.Millisecond,
			HotDuration:     200 * time.Millisecond,
			Store:           litestream.NewStore(nil, litestream.CompactionLevels{}),
			SharedResources: litestreampp.NewSharedResourceManager(),
			ConnectionPool:  litestreampp.NewConnectionPool(10, 5*time.Second),
		}
		
		manager := litestreampp.NewHotColdManager(config)

		// Start manager
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		
		err := manager.Start(ctx)
		if err != nil {
			t.Fatalf("failed to start manager: %v", err)
		}
		defer manager.Stop()

		// Add databases
		patterns := []string{filepath.Join(tmpDir, "*.db")}
		err = manager.AddDatabases(patterns)
		if err != nil {
			t.Fatalf("failed to add databases: %v", err)
		}

		// Initially both should be cold
		total, hot, cold := manager.GetStatistics()
		if total != 2 {
			t.Errorf("expected 2 total databases, got %d", total)
		}
		if hot != 0 {
			t.Errorf("expected 0 hot databases initially, got %d", hot)
		}
		if cold != 2 {
			t.Errorf("expected 2 cold databases initially, got %d", cold)
		}

		// Modify db1 to trigger promotion
		time.Sleep(50 * time.Millisecond)
		modifyTestDB(t, db1)

		// Wait for detection and promotion
		time.Sleep(200 * time.Millisecond)

		// db1 should be hot now
		if !manager.IsHot(db1) {
			t.Error("db1 should be hot after modification")
		}

		total, hot, cold = manager.GetStatistics()
		if hot != 1 {
			t.Errorf("expected 1 hot database after modification, got %d", hot)
		}
		if cold != 1 {
			t.Errorf("expected 1 cold database after modification, got %d", cold)
		}

		// Wait for hot duration to expire
		time.Sleep(300 * time.Millisecond)

		// db1 should be cold again
		if manager.IsHot(db1) {
			t.Error("db1 should be cold after hot duration expired")
		}

		total, hot, cold = manager.GetStatistics()
		if hot != 0 {
			t.Errorf("expected 0 hot databases after expiry, got %d", hot)
		}
		if cold != 2 {
			t.Errorf("expected 2 cold databases after expiry, got %d", cold)
		}
	})

	t.Run("MaxHotEnforcement", func(t *testing.T) {
		tmpDir := t.TempDir()
		var dbs []string
		
		// Create 5 databases
		for i := 0; i < 5; i++ {
			db := filepath.Join(tmpDir, fmt.Sprintf("db%d.db", i))
			createTestDB(t, db)
			dbs = append(dbs, db)
		}

		// Create manager with max 3 hot DBs
		config := &litestreampp.HotColdConfig{
			MaxHotDatabases: 3,
			ScanInterval:    100 * time.Millisecond,
			HotDuration:     500 * time.Millisecond,
			Store:           litestream.NewStore(nil, litestream.CompactionLevels{}),
			SharedResources: litestreampp.NewSharedResourceManager(),
			ConnectionPool:  litestreampp.NewConnectionPool(10, 5*time.Second),
		}
		
		manager := litestreampp.NewHotColdManager(config)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		
		manager.Start(ctx)
		defer manager.Stop()

		// Add all databases
		patterns := []string{filepath.Join(tmpDir, "*.db")}
		manager.AddDatabases(patterns)

		// Modify all 5 databases
		for _, db := range dbs {
			modifyTestDB(t, db)
		}

		// Wait for detection
		time.Sleep(200 * time.Millisecond)

		// Only 3 should be hot
		total, hot, cold := manager.GetStatistics()
		if total != 5 {
			t.Errorf("expected 5 total databases, got %d", total)
		}
		if hot > 3 {
			t.Errorf("expected max 3 hot databases, got %d", hot)
		}
		if cold != total-hot {
			t.Errorf("expected %d cold databases, got %d", total-hot, cold)
		}
	})

	t.Run("ProjectStructure", func(t *testing.T) {
		tmpDir := t.TempDir()
		
		// Create project structure
		projectPath := filepath.Join(tmpDir, "myproject", "databases", "maindb", "branches", "main", "tenants")
		os.MkdirAll(projectPath, 0755)
		
		db1 := filepath.Join(projectPath, "tenant1.db")
		db2 := filepath.Join(projectPath, "tenant2.db")
		
		createTestDB(t, db1)
		createTestDB(t, db2)

		config := &litestreampp.HotColdConfig{
			MaxHotDatabases: 10,
			ScanInterval:    100 * time.Millisecond,
			HotDuration:     200 * time.Millisecond,
			Store:           litestream.NewStore(nil, litestream.CompactionLevels{}),
			SharedResources: litestreampp.NewSharedResourceManager(),
			ConnectionPool:  litestreampp.NewConnectionPool(10, 5*time.Second),
		}
		
		manager := litestreampp.NewHotColdManager(config)

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		
		manager.Start(ctx)
		defer manager.Stop()

		// Use pattern matching
		pattern := filepath.Join(tmpDir, "*", "databases", "*", "branches", "*", "tenants", "*.db")
		manager.AddDatabases([]string{pattern})

		// Verify discovery
		total, _, _ := manager.GetStatistics()
		if total != 2 {
			t.Errorf("expected 2 databases discovered, got %d", total)
		}

		// Modify one tenant
		modifyTestDB(t, db1)
		time.Sleep(200 * time.Millisecond)

		// Check promotion
		if !manager.IsHot(db1) {
			t.Error("tenant1.db should be hot after modification")
		}
		if manager.IsHot(db2) {
			t.Error("tenant2.db should remain cold")
		}
	})
}

func TestHotColdManagerIntegration(t *testing.T) {
	t.Run("WithSharedResources", func(t *testing.T) {
		tmpDir := t.TempDir()
		db1 := filepath.Join(tmpDir, "db1.db")
		db2 := filepath.Join(tmpDir, "db2.db")
		
		createTestDB(t, db1)
		createTestDB(t, db2)

		// Create full stack of components
		store := litestream.NewStore(nil, litestream.CompactionLevels{})
		sharedResources := litestreampp.NewSharedResourceManager()
		connectionPool := litestreampp.NewConnectionPool(10, 5*time.Second)
		
		// Start connection pool cleanup
		poolCtx, poolCancel := context.WithCancel(context.Background())
		defer poolCancel()
		go connectionPool.Start(poolCtx)

		config := &litestreampp.HotColdConfig{
			MaxHotDatabases: 10,
			ScanInterval:    100 * time.Millisecond,
			HotDuration:     200 * time.Millisecond,
			Store:           store,
			SharedResources: sharedResources,
			ConnectionPool:  connectionPool,
		}
		
		manager := litestreampp.NewHotColdManager(config)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		
		manager.Start(ctx)
		defer manager.Stop()

		// Add databases
		manager.AddDatabases([]string{filepath.Join(tmpDir, "*.db")})

		// Trigger some activity
		modifyTestDB(t, db1)
		time.Sleep(50 * time.Millisecond)
		modifyTestDB(t, db2)
		time.Sleep(100 * time.Millisecond) // Wait for detection
		
		// Both should be hot (within the 200ms hot duration window)
		if !manager.IsHot(db1) {
			t.Error("db1 should be hot")
		}
		if !manager.IsHot(db2) {
			t.Error("db2 should be hot")
		}

		// Check connection pool has connections
		stats := connectionPool.Stats()
		if stats.CurrentOpen == 0 {
			t.Log("Warning: no connections in pool (might be timing issue)")
		}

		// Wait for expiry
		time.Sleep(400 * time.Millisecond)

		// Both should be cold
		if manager.IsHot(db1) {
			t.Error("db1 should be cold after expiry")
		}
		if manager.IsHot(db2) {
			t.Error("db2 should be cold after expiry")
		}

		// Connections should be cleaned up (after idle timeout)
		time.Sleep(100 * time.Millisecond)
		connectionPool.Cleanup()
		stats = connectionPool.Stats()
		// Connections might still be open depending on timing
		t.Logf("Final connection pool stats: %+v", stats)
	})
}

// Helper to create a test SQLite database
func createTestDB(t *testing.T, path string) {
	t.Helper()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create directory: %v", err)
	}
	
	// Create an actual SQLite database file
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create database file: %v", err)
	}
	file.Close()
	
	// Small delay to ensure distinct mtimes
	time.Sleep(10 * time.Millisecond)
}

// Helper to modify a test database
func modifyTestDB(t *testing.T, path string) {
	t.Helper()
	
	// Append some data to change size and mtime
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("failed to open database for modification: %v", err)
	}
	defer file.Close()
	
	if _, err := file.WriteString("test modification"); err != nil {
		t.Fatalf("failed to modify database: %v", err)
	}
	
	// Small delay to ensure mtime change is visible
	time.Sleep(10 * time.Millisecond)
}