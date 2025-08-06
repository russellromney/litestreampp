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

func TestIntegratedMultiDBManager(t *testing.T) {
	t.Run("BasicIntegration", func(t *testing.T) {
		tmpDir := t.TempDir()
		
		// Create test databases
		db1 := filepath.Join(tmpDir, "db1.db")
		db2 := filepath.Join(tmpDir, "db2.db")
		db3 := filepath.Join(tmpDir, "db3.db")
		
		createTestDB(t, db1)
		createTestDB(t, db2)
		createTestDB(t, db3)
		
		// Create configuration
		config := &litestreampp.MultiDBConfig{
			Enabled:         true,
			Patterns:        []string{filepath.Join(tmpDir, "*.db")},
			MaxHotDatabases: 10,
			ScanInterval:    100 * time.Millisecond,
			HotPromotion: litestreampp.HotPromotionConfig{
				RecentModifyThreshold: 200 * time.Millisecond,
			},
		}
		
		// Create store
		store := litestream.NewStore(nil, litestream.CompactionLevels{})
		
		// Create integrated manager
		manager, err := litestreampp.NewIntegratedMultiDBManager(store, config)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}
		
		// Start manager
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		
		err = manager.Start(ctx)
		if err != nil {
			t.Fatalf("failed to start manager: %v", err)
		}
		defer manager.Stop()
		
		// Wait for initial scan
		time.Sleep(150 * time.Millisecond)
		
		// Check initial state (all should be cold)
		total, hot, cold, _ := manager.GetStatistics()
		if total != 3 {
			t.Errorf("expected 3 total databases, got %d", total)
		}
		if hot != 0 {
			t.Errorf("expected 0 hot databases initially, got %d", hot)
		}
		if cold != 3 {
			t.Errorf("expected 3 cold databases initially, got %d", cold)
		}
		
		// Modify db1 and db2
		modifyTestDB(t, db1)
		modifyTestDB(t, db2)
		
		// Wait for detection
		time.Sleep(150 * time.Millisecond)
		
		// Check hot promotion
		if !manager.IsHot(db1) {
			t.Error("db1 should be hot after modification")
		}
		if !manager.IsHot(db2) {
			t.Error("db2 should be hot after modification")
		}
		if manager.IsHot(db3) {
			t.Error("db3 should remain cold")
		}
		
		total, hot, cold, connStats := manager.GetStatistics()
		if hot != 2 {
			t.Errorf("expected 2 hot databases, got %d", hot)
		}
		if cold != 1 {
			t.Errorf("expected 1 cold database, got %d", cold)
		}
		
		// Log connection stats for verification
		t.Logf("Connection stats: %+v", connStats)
		
		// Wait for hot duration to expire
		time.Sleep(300 * time.Millisecond)
		
		// All should be cold again
		total, hot, cold, _ = manager.GetStatistics()
		if hot != 0 {
			t.Errorf("expected 0 hot databases after expiry, got %d", hot)
		}
		if cold != 3 {
			t.Errorf("expected 3 cold databases after expiry, got %d", cold)
		}
	})
	
	t.Run("MaxHotEnforcement", func(t *testing.T) {
		tmpDir := t.TempDir()
		
		// Create 5 databases
		var dbs []string
		for i := 0; i < 5; i++ {
			db := filepath.Join(tmpDir, fmt.Sprintf("db%d.db", i))
			createTestDB(t, db)
			dbs = append(dbs, db)
		}
		
		// Configuration with max 2 hot DBs
		config := &litestreampp.MultiDBConfig{
			Enabled:         true,
			Patterns:        []string{filepath.Join(tmpDir, "*.db")},
			MaxHotDatabases: 2,
			ScanInterval:    100 * time.Millisecond,
			HotPromotion: litestreampp.HotPromotionConfig{
				RecentModifyThreshold: 500 * time.Millisecond,
			},
		}
		
		store := litestream.NewStore(nil, litestream.CompactionLevels{})
		manager, err := litestreampp.NewIntegratedMultiDBManager(store, config)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}
		
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		
		manager.Start(ctx)
		defer manager.Stop()
		
		// Wait for initial scan
		time.Sleep(150 * time.Millisecond)
		
		// Modify all 5 databases
		for _, db := range dbs {
			modifyTestDB(t, db)
		}
		
		// Wait for detection
		time.Sleep(150 * time.Millisecond)
		
		// Check that only 2 are hot (max limit)
		total, hot, cold, _ := manager.GetStatistics()
		if total != 5 {
			t.Errorf("expected 5 total databases, got %d", total)
		}
		if hot > 2 {
			t.Errorf("expected max 2 hot databases, got %d", hot)
		}
		if cold != total-hot {
			t.Errorf("expected %d cold databases, got %d", total-hot, cold)
		}
		
		// Verify we have exactly the expected hot count
		hotList := manager.GetHotDatabases()
		if len(hotList) != hot {
			t.Errorf("hot list length %d doesn't match hot count %d", len(hotList), hot)
		}
	})
	
	t.Run("PatternRefresh", func(t *testing.T) {
		tmpDir := t.TempDir()
		subDir := filepath.Join(tmpDir, "subdir")
		os.MkdirAll(subDir, 0755)
		
		// Create initial databases
		db1 := filepath.Join(tmpDir, "db1.db")
		createTestDB(t, db1)
		
		config := &litestreampp.MultiDBConfig{
			Enabled:         true,
			Patterns:        []string{filepath.Join(tmpDir, "*.db")},
			MaxHotDatabases: 10,
			ScanInterval:    100 * time.Millisecond,
			HotPromotion: litestreampp.HotPromotionConfig{
				RecentModifyThreshold: 200 * time.Millisecond,
			},
		}
		
		store := litestream.NewStore(nil, litestream.CompactionLevels{})
		manager, err := litestreampp.NewIntegratedMultiDBManager(store, config)
		if err != nil {
			t.Fatalf("failed to create manager: %v", err)
		}
		
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		
		manager.Start(ctx)
		defer manager.Stop()
		
		time.Sleep(150 * time.Millisecond)
		
		// Check initial count
		total, _, _, _ := manager.GetStatistics()
		if total != 1 {
			t.Errorf("expected 1 database initially, got %d", total)
		}
		
		// Create new database
		db2 := filepath.Join(tmpDir, "db2.db")
		createTestDB(t, db2)
		
		// Refresh patterns to discover new database
		err = manager.RefreshPatterns()
		if err != nil {
			t.Fatalf("failed to refresh patterns: %v", err)
		}
		
		time.Sleep(150 * time.Millisecond)
		
		// Check updated count
		total, _, _, _ = manager.GetStatistics()
		if total != 2 {
			t.Errorf("expected 2 databases after refresh, got %d", total)
		}
	})
}